package memberlist

import (
	"fmt"
	"github.com/ugorji/go/codec"
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
	userMsg // User mesg, not handled by us
)

const (
	udpRecvBuf             = 2 * 1024 * 1024
	udpBufSize             = 65536
	udpSendBuf             = 1400
	compoundHeaderOverhead = 2 // Assumed header overhead
	compoundOverhead       = 2 // Assumed overhead per entry in compoundHeader
	userMsgOverhead        = 1
	metaMaxSize            = 128 // Maximum size for nod emeta data
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
	Meta        []byte
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
	Nodes        int
	UserStateLen int // Encodes the byte lengh of user state
}

// pushNodeState is used for pushPullReq when we are
// transfering out node states
type pushNodeState struct {
	Name        string
	Addr        []byte
	Meta        []byte
	Incarnation uint32
	State       int
}

// setUDPRecvBuf is used to resize the UDP receive window. The function
// attempts to set the read buffer to `udpRecvBuf` but backs off until
// the read buffer can be set.
func setUDPRecvBuf(c *net.UDPConn) {
	size := udpRecvBuf
	for {
		if err := c.SetReadBuffer(size); err == nil {
			break
		}
		size = size / 2
	}
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

	remoteNodes, userState, err := readRemoteState(conn)
	if err != nil {
		log.Printf("[ERR] Failed to receive remote state: %s", err)
		return
	}

	if err := m.sendLocalState(conn); err != nil {
		log.Printf("[ERR] Failed to push local state: %s", err)
	}

	// Merge the membership state
	m.mergeState(remoteNodes)

	// Invoke the delegate for user state
	if m.config.UserDelegate != nil {
		m.config.UserDelegate.MergeRemoteState(userState)
	}
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
	case userMsg:
		m.handleUser(buf, from)
	default:
		log.Printf("[ERR] UDP msg type (%d) not supported. From: %s", msgType, from)
	}
}

func (m *Memberlist) handleCompound(buf []byte, from net.Addr) {
	// Decode the parts
	trunc, parts, err := decodeCompoundMessage(buf)
	if err != nil {
		log.Printf("[ERR] Failed to decode compound request: %s", err)
		return
	}

	// Log any truncation
	if trunc > 0 {
		log.Printf("[WARN] Compound request had %d truncated messages", trunc)
	}

	// Handle each message
	for _, part := range parts {
		m.handleCommand(part, from)
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

// handleUser is used to notify channels of incoming user data
func (m *Memberlist) handleUser(buf []byte, from net.Addr) {
	d := m.config.UserDelegate
	if d != nil {
		d.NotifyMsg(buf)
	}
}

// encodeAndSendMsg is used to combine the encoding and sending steps
func (m *Memberlist) encodeAndSendMsg(to net.Addr, msgType int, msg interface{}) error {
	out, err := encode(msgType, msg)
	if err != nil {
		return err
	}
	if err := m.sendMsg(to, out.Bytes()); err != nil {
		return err
	}
	return nil
}

// sendMsg is used to send a UDP message to another host. It will opportunistically
// create a compoundMsg and piggy back other broadcasts
func (m *Memberlist) sendMsg(to net.Addr, msg []byte) error {
	// Check if we can piggy back any messages
	bytesAvail := udpSendBuf - len(msg) - compoundHeaderOverhead
	extra := m.getBroadcasts(compoundOverhead, bytesAvail)

	// Fast path if nothing to piggypack
	if len(extra) == 0 {
		return m.rawSendMsg(to, msg)
	}

	// Join all the messages
	msgs := make([][]byte, 0, 1+len(extra))
	msgs = append(msgs, msg)
	msgs = append(msgs, extra...)

	// Create a compound message
	compound := makeCompoundMessage(msgs)

	// Send the message
	return m.rawSendMsg(to, compound.Bytes())
}

// rawSendMsg is used to send a UDP message to another host without modification
func (m *Memberlist) rawSendMsg(to net.Addr, msg []byte) error {
	_, err := m.udpListener.WriteTo(msg, to)
	return err
}

// sendState is used to initiate a push/pull over TCP with a remote node
func (m *Memberlist) sendAndReceiveState(addr []byte) ([]pushNodeState, []byte, error) {
	// Attempt to connect
	dialer := net.Dialer{Timeout: m.config.TCPTimeout}
	dest := net.TCPAddr{IP: addr, Port: m.config.TCPPort}
	conn, err := dialer.Dial("tcp", dest.String())
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	// Send our state
	if err := m.sendLocalState(conn); err != nil {
		return nil, nil, err
	}

	// Read remote state
	remote, userState, err := readRemoteState(conn)
	if err != nil {
		return nil, nil, err
	}

	// Return the remote state
	return remote, userState, nil
}

// sendLocalState is invoked to send our local state over a tcp connection
func (m *Memberlist) sendLocalState(conn net.Conn) error {
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

	// Get the delegate state
	var userData []byte
	if m.config.UserDelegate != nil {
		userData = m.config.UserDelegate.LocalState()
	}

	// Send our node state
	header := pushPullHeader{Nodes: len(localNodes), UserStateLen: len(userData)}
	hd := codec.MsgpackHandle{}
	enc := codec.NewEncoder(conn, &hd)

	// Begin state push
	conn.Write([]byte{pushPullMsg})

	if err := enc.Encode(&header); err != nil {
		return err
	}
	for i := 0; i < header.Nodes; i++ {
		if err := enc.Encode(&localNodes[i]); err != nil {
			return err
		}
	}

	// Write the user state as well
	if userData != nil {
		conn.Write(userData)
	}
	return nil
}

// recvRemoteState is used to read the remote state from a connection
func readRemoteState(conn net.Conn) ([]pushNodeState, []byte, error) {
	// Read the message type
	buf := []byte{0}
	if _, err := conn.Read(buf); err != nil {
		return nil, nil, err
	}
	msgType := uint8(buf[0])

	// Quit if not push/pull
	if msgType != pushPullMsg {
		err := fmt.Errorf("received invalid msgType (%d)", msgType)
		return nil, nil, err
	}

	// Read the push/pull header
	var header pushPullHeader
	hd := codec.MsgpackHandle{}
	dec := codec.NewDecoder(conn, &hd)
	if err := dec.Decode(&header); err != nil {
		return nil, nil, err
	}

	// Allocate space for the transfer
	remoteNodes := make([]pushNodeState, header.Nodes)

	// Try to decode all the states
	for i := 0; i < header.Nodes; i++ {
		if err := dec.Decode(&remoteNodes[i]); err != nil {
			return remoteNodes, nil, err
		}
	}

	// Read the remote user state into a buffer
	var userBuf []byte
	if header.UserStateLen > 0 {
		userBuf = make([]byte, header.UserStateLen)
		bytes, err := conn.Read(userBuf)
		if err == nil && bytes != header.UserStateLen {
			err = fmt.Errorf("Failed to read full user state (%d / %d)", bytes, header.UserStateLen)
		}
		if err != nil {
			return remoteNodes, nil, err
		}
	}

	return remoteNodes, userBuf, nil
}
