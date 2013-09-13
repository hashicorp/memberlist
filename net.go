package memberlist

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"log"
	"net"
)

const (
	pingMsg = iota
	indirectPingMsg
	ackRespMsg
	suspectMsg
	aliveMsg
	deadMsg
	pushPullMsg
	compoundMsg
)

const (
	udpBufSize             = 65536
	udpSendBuf             = 1400
	compoundHeaderOverhead = 2 // Assumed header overhead
	compoundOverhead       = 2 // Assumed overhead per entry in compoundHeader
)

// ping request sent directly to node
type ping struct {
	SeqNo uint32
}

// indirect ping sent to an indirect ndoe
type indirectPingReq struct {
	SeqNo  uint32
	Target []byte
}

// ack response is sent for a ping
type ackResp struct {
	SeqNo uint32
}

// suspect is broadcast when we suspect a node is dead
type suspect struct {
	Incarnation uint32
	Node        string
}

// alive is broadcast when we know a node is alive.
// Overloaded for nodes joining
type alive struct {
	Incarnation uint32
	Node        string
	Addr        []byte
}

// dead is broadcast when we confirm a node is dead
// Overloaded for nodes leaving
type dead struct {
	Incarnation uint32
	Node        string
}

// pushPullHeader is used to inform the
// otherside how many states we are transfering
type pushPullHeader struct {
	Nodes int
}

// pushNodeState is used for pushPullReq when we are
// transfering out node states
type pushNodeState struct {
	Name        string
	Addr        []byte
	Incarnation uint32
	State       int
}

// tcpListen listens for and handles incoming connections
func (m *Memberlist) tcpListen() {
	for {
		conn, err := m.tcpListener.AcceptTCP()
		if err != nil {
			if m.shutdown {
				break
			}
			log.Printf("[ERR] Error accepting TCP connection: %s", err)
			continue
		}
		go m.handleConn(conn)
	}
}

// handleConn handles a single incoming TCP connection
func (m *Memberlist) handleConn(conn *net.TCPConn) {
	defer conn.Close()
	buf := []byte{0}

	// Read the message type
	if _, err := conn.Read(buf); err != nil {
		log.Printf("[ERR] Failed to read the msg type: %s", err)
		return
	}
	msgType := uint8(buf[0])

	// Quit if not push/pull
	if msgType != pushPullMsg {
		log.Printf("[ERR] Invalid TCP request type (%d)", msgType)
		return
	}

	// Read the push/pull header
	var header pushPullHeader
	dec := gob.NewDecoder(conn)
	if err := dec.Decode(&header); err != nil {
		log.Printf("[ERR] Failed to decode Push/Pull header: %s", err)
		return
	}

	// Allocate space for the transfer
	remoteNodes := make([]pushNodeState, header.Nodes)

	// Try to decode all the states
	for i := 0; i < header.Nodes; i++ {
		if err := dec.Decode(&remoteNodes[i]); err != nil {
			log.Printf("[ERR] Failed to decode Push/Pull state (idx: %d / %d): %s", i+1, header.Nodes, err)
			return
		}
	}

	// Prepare the local node state
	m.nodeLock.RLock()
	localNodes := make([]pushNodeState, len(m.nodes))
	for idx, n := range m.nodes {
		localNodes[idx].Name = n.Name
		localNodes[idx].Addr = n.Addr
		localNodes[idx].Incarnation = n.Incarnation
		localNodes[idx].State = n.State
	}
	m.nodeLock.RUnlock()

	// Send our node state
	header.Nodes = len(localNodes)
	enc := gob.NewEncoder(conn)

	// Send the push/pull indicator
	conn.Write([]byte{pushPullMsg})

	if err := enc.Encode(&header); err != nil {
		log.Printf("[ERR] Failed to send Push/Pull header: %s", err)
		goto AFTER_SEND
	}
	for i := 0; i < header.Nodes; i++ {
		if err := enc.Encode(&localNodes[i]); err != nil {
			log.Printf("[ERR] Failed to send Push/Pull state (idx: %d / %d): %s", i+1, header.Nodes, err)
			goto AFTER_SEND
		}
	}

AFTER_SEND:
	// Allow the local state to be updated
	m.mergeState(remoteNodes)
}

// udpListen listens for and handles incoming UDP packets
func (m *Memberlist) udpListen() {
	mainBuf := make([]byte, udpBufSize)
	var n int
	var addr net.Addr
	var err error
	for {
		// Reset buffer
		buf := mainBuf[0:udpBufSize]

		// Read a packet
		n, addr, err = m.udpListener.ReadFrom(buf)
		if err != nil {
			if m.shutdown {
				break
			}
			log.Printf("[ERR] Error reading UDP packet: %s", err)
			continue
		}

		// Check the length
		if n < 1 {
			log.Printf("[ERR] UDP packet too short (%d bytes). From: %s", len(buf), addr)
			continue
		}

		// Handle the command
		m.handleCommand(buf[:n], addr)
	}
}

func (m *Memberlist) handleCommand(buf []byte, from net.Addr) {
	// Decode the message type
	msgType := uint8(buf[0])
	buf = buf[1:]

	// Switch on the msgType
	switch msgType {
	case compoundMsg:
		m.handleCompound(buf, from)
	case pingMsg:
		m.handlePing(buf, from)
	case indirectPingMsg:
		m.handleIndirectPing(buf, from)
	case ackRespMsg:
		m.handleAck(buf, from)
	case suspectMsg:
		m.handleSuspect(buf, from)
	case aliveMsg:
		m.handleAlive(buf, from)
	case deadMsg:
		m.handleDead(buf, from)
	default:
		log.Printf("[ERR] UDP msg type (%d) not supported. From: %s", msgType, from)
	}
}

func (m *Memberlist) handleCompound(buf []byte, from net.Addr) {
	// Ensure we have at least the length byte
	if len(buf) < 1 {
		log.Printf("[ERR] Failed to decode compound request: missing len prefix")
		return
	}
	numParts := uint8(buf[0])
	buf = buf[1:]

	// Check we have enough bytes
	if len(buf) < int(numParts*2) {
		log.Printf("[ERR] Failed to decode compound request: truncated len slice")
		return
	}

	// Decode the lengths
	lengths := make([]uint16, numParts)
	for i := 0; i < int(numParts); i++ {
		lengths[i] = binary.BigEndian.Uint16(buf[i*2 : i*2+2])
	}
	buf = buf[numParts*2:]

	// Handle each message
	for _, msgLen := range lengths {
		if len(buf) < int(msgLen) {
			log.Printf("[ERR] Failed to decode compound request: truncated message (%d / %d)", len(buf), msgLen)
			return
		}

		// Extract the slice, seek past on the buffer
		slice := buf[:msgLen]
		buf = buf[msgLen:]

		// Handle the command
		m.handleCommand(slice, from)
	}
}

func (m *Memberlist) handlePing(buf []byte, from net.Addr) {
	var p ping
	if err := decode(buf, &p); err != nil {
		log.Printf("[ERR] Failed to decode ping request: %s", err)
		return
	}
	ack := ackResp{p.SeqNo}
	if err := m.encodeAndSendMsg(from, ackRespMsg, &ack); err != nil {
		log.Printf("[ERR] Failed to send ack: %s", err)
	}
}

func (m *Memberlist) handleIndirectPing(buf []byte, from net.Addr) {
	var ind indirectPingReq
	if err := decode(buf, &ind); err != nil {
		log.Printf("[ERR] Failed to decode indirect ping request: %s", err)
		return
	}

	// Send a ping to the correct host
	localSeqNo := m.nextSeqNo()
	ping := ping{SeqNo: localSeqNo}
	destAddr := &net.UDPAddr{IP: ind.Target, Port: m.config.UDPPort}

	// Setup a response handler to relay the ack
	respHandler := func() {
		ack := ackResp{ind.SeqNo}
		if err := m.encodeAndSendMsg(from, ackRespMsg, &ack); err != nil {
			log.Printf("[ERR] Failed to forward ack: %s", err)
		}
	}
	m.setAckHandler(localSeqNo, respHandler, m.config.RTT)

	// Send the ping
	if err := m.encodeAndSendMsg(destAddr, pingMsg, &ping); err != nil {
		log.Printf("[ERR] Failed to send ping: %s", err)
	}
}

func (m *Memberlist) handleAck(buf []byte, from net.Addr) {
	var ack ackResp
	if err := decode(buf, &ack); err != nil {
		log.Printf("[ERR] Failed to decode ack response: %s", err)
		return
	}
	m.invokeAckHandler(ack.SeqNo)
}

func (m *Memberlist) handleSuspect(buf []byte, from net.Addr) {
	var sus suspect
	if err := decode(buf, &sus); err != nil {
		log.Printf("[ERR] Failed to decode suspect message: %s", err)
		return
	}
	m.suspectNode(&sus)
}

func (m *Memberlist) handleAlive(buf []byte, from net.Addr) {
	var live alive
	if err := decode(buf, &live); err != nil {
		log.Printf("[ERR] Failed to decode alive message: %s", err)
		return
	}
	m.aliveNode(&live)
}

func (m *Memberlist) handleDead(buf []byte, from net.Addr) {
	var d dead
	if err := decode(buf, &d); err != nil {
		log.Printf("[ERR] Failed to decode dead message: %s", err)
		return
	}
	m.deadNode(&d)
}

// encodeAndSendMsg is used to combine the encoding and sending steps
func (m *Memberlist) encodeAndSendMsg(to net.Addr, msgType int, msg interface{}) error {
	out, err := encode(msgType, msg)
	if err != nil {
		return err
	}
	if err := m.sendMsg(to, out); err != nil {
		return err
	}
	return nil
}

// sendMsg is used to send a UDP message to another host. It will opportunistically
// create a compoundMsg and piggy back other broadcasts
func (m *Memberlist) sendMsg(to net.Addr, msg *bytes.Buffer) error {
	// Check if we can piggy back any messages
	bytesAvail := udpSendBuf - msg.Len() - compoundHeaderOverhead
	extra := m.getBroadcasts(compoundOverhead, bytesAvail)

	// If there is no extra, then just use the raw
	if len(extra) == 0 {
		return m.rawSendMsg(to, msg)
	}

	// Create a local buffer
	var buf bytes.Buffer

	// Write out the type
	(&buf).WriteByte(uint8(compoundMsg))

	// Write out the number of message
	(&buf).WriteByte(uint8(1 + len(extra)))

	// Add the message lengths
	binary.Write(&buf, binary.BigEndian, uint16(msg.Len()))
	for _, b := range extra {
		binary.Write(&buf, binary.BigEndian, uint16(b.Len()))
	}

	// Append the messages
	buf.Write(msg.Bytes())
	for _, b := range extra {
		buf.Write(b.Bytes())
	}

	// Send the message
	return m.rawSendMsg(to, msg)
}

// rawSendMsg is used to send a UDP message to another host without modification
func (m *Memberlist) rawSendMsg(to net.Addr, msg *bytes.Buffer) error {
	_, err := m.udpListener.WriteTo(msg.Bytes(), to)
	return err
}
