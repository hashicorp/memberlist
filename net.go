package memberlist

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/ugorji/go/codec"
	"io"
	"net"
	"time"
)

// This is the minimum and maximum protocol version that we can
// _understand_. We're allowed to speak at any version within this
// range. This range is inclusive.
const (
	ProtocolVersionMin uint8 = 0
	ProtocolVersionMax       = 1
)

// messageType is an integer ID of a type of message that can be received
// on network channels from other members.
type messageType uint8

// The list of available message types.
const (
	pingMsg messageType = iota
	indirectPingMsg
	ackRespMsg
	suspectMsg
	aliveMsg
	deadMsg
	pushPullMsg
	compoundMsg
	userMsg // User mesg, not handled by us
	compressMsg
	encryptMsg
)

// compressionType is used to specify the compression algorithm
type compressionType uint8

const (
	deflateAlgo compressionType = iota
)

const (
	compoundHeaderOverhead = 2   // Assumed header overhead
	compoundOverhead       = 2   // Assumed overhead per entry in compoundHeader
	metaMaxSize            = 128 // Maximum size for nod emeta data
	udpBufSize             = 65536
	udpRecvBuf             = 2 * 1024 * 1024
	udpSendBuf             = 1400
	userMsgOverhead        = 1
	blockingWarning        = 10 * time.Millisecond // Warn if a UDP packet takes this long to process
	maxPushStateBytes      = 10 * 1024 * 1024
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

	// The versions of the protocol/delegate that are being spoken, order:
	// pmin, pmax, pcur, dmin, dmax, dcur
	Vsn []uint8
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
	State       nodeStateType
	Vsn         []uint8 // Protocol versions
}

// compress is used to wrap an underlying payload
// using a specified compression algorithm
type compress struct {
	Algo compressionType
	Buf  []byte
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
			m.logger.Printf("[ERR] Error accepting TCP connection: %s", err)
			continue
		}
		go m.handleConn(conn)
	}
}

// handleConn handles a single incoming TCP connection
func (m *Memberlist) handleConn(conn *net.TCPConn) {
	m.logger.Printf("[INFO] Responding to push/pull sync with: %s", conn.RemoteAddr())
	defer conn.Close()

	remoteNodes, userState, err := m.readRemoteState(conn)
	if err != nil {
		m.logger.Printf("[ERR] Failed to receive remote state: %s", err)
		return
	}

	if err := m.sendLocalState(conn); err != nil {
		m.logger.Printf("[ERR] Failed to push local state: %s", err)
	}

	if err := m.verifyProtocol(remoteNodes); err != nil {
		m.logger.Printf("[ERR] Push/pull verification failed: %s", err)
		return
	}

	// Merge the membership state
	m.mergeState(remoteNodes)

	// Invoke the delegate for user state
	if m.config.Delegate != nil {
		m.config.Delegate.MergeRemoteState(userState)
	}
}

// udpListen listens for and handles incoming UDP packets
func (m *Memberlist) udpListen() {
	mainBuf := make([]byte, udpBufSize)
	var n int
	var addr net.Addr
	var err error
	var lastPacket time.Time
	for {
		// Do a check for potentially blocking operations
		if !lastPacket.IsZero() && time.Now().Sub(lastPacket) > blockingWarning {
			diff := time.Now().Sub(lastPacket)
			m.logger.Printf(
				"[WARN] Potential blocking operation. Last command took %v",
				diff)
		}

		// Reset buffer
		buf := mainBuf[0:udpBufSize]

		// Read a packet
		n, addr, err = m.udpListener.ReadFrom(buf)
		if err != nil {
			if m.shutdown {
				break
			}
			m.logger.Printf("[ERR] Error reading UDP packet: %s", err)
			continue
		}

		// Check the length
		if n < 1 {
			m.logger.Printf("[ERR] UDP packet too short (%d bytes). From: %s",
				len(buf), addr)
			continue
		}

		// Capture the current time
		lastPacket = time.Now()

		// Ingest this packet
		m.ingestPacket(buf[:n], addr)
	}
}

func (m *Memberlist) ingestPacket(buf []byte, from net.Addr) {
	// Check if encryption is enabled
	if m.derivedKey != nil {
		// Verify HMAC first
		if err := hmacVerifyPayload(m.derivedKey, buf); err != nil {
			m.logger.Printf("[WARN] Decode packet failed: %v", err)
			return
		}

		// Decrypt the payload
		n := len(buf) - hmacLength
		plain, err := decryptPayload(m.derivedKey, buf[:n])
		if err != nil {
			m.logger.Printf("[ERR] Decrypt packet failed: %v", err)
			return
		}

		// Continue processing the plaintext buffer
		buf = plain
	}

	// Handle the command
	m.handleCommand(buf, from)
}

func (m *Memberlist) handleCommand(buf []byte, from net.Addr) {
	// Decode the message type
	msgType := messageType(buf[0])
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
	case compressMsg:
		m.handleCompressed(buf, from)
	default:
		m.logger.Printf("[ERR] UDP msg type (%d) not supported. From: %s", msgType, from)
	}
}

func (m *Memberlist) handleCompound(buf []byte, from net.Addr) {
	// Decode the parts
	trunc, parts, err := decodeCompoundMessage(buf)
	if err != nil {
		m.logger.Printf("[ERR] Failed to decode compound request: %s", err)
		return
	}

	// Log any truncation
	if trunc > 0 {
		m.logger.Printf("[WARN] Compound request had %d truncated messages", trunc)
	}

	// Handle each message
	for _, part := range parts {
		m.handleCommand(part, from)
	}
}

func (m *Memberlist) handlePing(buf []byte, from net.Addr) {
	var p ping
	if err := decode(buf, &p); err != nil {
		m.logger.Printf("[ERR] Failed to decode ping request: %s", err)
		return
	}
	ack := ackResp{p.SeqNo}
	if err := m.encodeAndSendMsg(from, ackRespMsg, &ack); err != nil {
		m.logger.Printf("[ERR] Failed to send ack: %s", err)
	}
}

func (m *Memberlist) handleIndirectPing(buf []byte, from net.Addr) {
	var ind indirectPingReq
	if err := decode(buf, &ind); err != nil {
		m.logger.Printf("[ERR] Failed to decode indirect ping request: %s", err)
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
			m.logger.Printf("[ERR] Failed to forward ack: %s", err)
		}
	}
	m.setAckHandler(localSeqNo, respHandler, m.config.ProbeTimeout)

	// Send the ping
	if err := m.encodeAndSendMsg(destAddr, pingMsg, &ping); err != nil {
		m.logger.Printf("[ERR] Failed to send ping: %s", err)
	}
}

func (m *Memberlist) handleAck(buf []byte, from net.Addr) {
	var ack ackResp
	if err := decode(buf, &ack); err != nil {
		m.logger.Printf("[ERR] Failed to decode ack response: %s", err)
		return
	}
	m.invokeAckHandler(ack.SeqNo)
}

func (m *Memberlist) handleSuspect(buf []byte, from net.Addr) {
	var sus suspect
	if err := decode(buf, &sus); err != nil {
		m.logger.Printf("[ERR] Failed to decode suspect message: %s", err)
		return
	}
	m.suspectNode(&sus)
}

func (m *Memberlist) handleAlive(buf []byte, from net.Addr) {
	var live alive
	if err := decode(buf, &live); err != nil {
		m.logger.Printf("[ERR] Failed to decode alive message: %s", err)
		return
	}
	m.aliveNode(&live)
}

func (m *Memberlist) handleDead(buf []byte, from net.Addr) {
	var d dead
	if err := decode(buf, &d); err != nil {
		m.logger.Printf("[ERR] Failed to decode dead message: %s", err)
		return
	}
	m.deadNode(&d)
}

// handleUser is used to notify channels of incoming user data
func (m *Memberlist) handleUser(buf []byte, from net.Addr) {
	d := m.config.Delegate
	if d != nil {
		d.NotifyMsg(buf)
	}
}

// handleCompressed is used to unpack a compressed message
func (m *Memberlist) handleCompressed(buf []byte, from net.Addr) {
	// Try to decode the payload
	payload, err := decompressPayload(buf)
	if err != nil {
		m.logger.Printf("[ERR] Failed to decompress payload: %v", err)
		return
	}

	// Recursively handle the payload
	m.handleCommand(payload, from)
}

// encodeAndSendMsg is used to combine the encoding and sending steps
func (m *Memberlist) encodeAndSendMsg(to net.Addr, msgType messageType, msg interface{}) error {
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
	// Check if we have compression enabled
	if m.config.EnableCompression && m.ProtocolVersion() >= 1 {
		buf, err := compressPayload(msg)
		if err != nil {
			m.logger.Printf("[WARN] Failed to compress payload: %v", err)
		} else {
			msg = buf.Bytes()
		}
	}

	// Check if we have encryption enabled
	if m.derivedKey != nil {
		// Encrypt the payload
		buf, err := encryptPayload(m.derivedKey, msg)
		if err != nil {
			m.logger.Printf("[ERR] Encryption of message failed: %v", err)
			return err
		}

		// Append an HMAC signature
		if err := hmacPayload(m.derivedKey, buf); err != nil {
			m.logger.Printf("[ERR] HMAC signing of message failed: %v", err)
			return err
		}

		msg = buf.Bytes()
	}

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
	m.logger.Printf("[INFO] Initiating push/pull sync with: %s", conn.RemoteAddr())

	// Send our state
	if err := m.sendLocalState(conn); err != nil {
		return nil, nil, err
	}

	// Read remote state
	remote, userState, err := m.readRemoteState(conn)
	if err != nil {
		err := fmt.Errorf("Reading remote state failed: %v", err)
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
		localNodes[idx].Meta = n.Meta
		localNodes[idx].Vsn = []uint8{
			n.PMin, n.PMax, n.PCur,
			n.DMin, n.DMax, n.DCur,
		}
	}
	m.nodeLock.RUnlock()

	// Get the delegate state
	var userData []byte
	if m.config.Delegate != nil {
		userData = m.config.Delegate.LocalState()
	}

	// Create a bytes buffer writer
	bufConn := bytes.NewBuffer(nil)

	// Send our node state
	header := pushPullHeader{Nodes: len(localNodes), UserStateLen: len(userData)}
	hd := codec.MsgpackHandle{}
	enc := codec.NewEncoder(bufConn, &hd)

	// Begin state push
	if _, err := bufConn.Write([]byte{byte(pushPullMsg)}); err != nil {
		return err
	}

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
		if _, err := bufConn.Write(userData); err != nil {
			return err
		}
	}

	// Get the send buffer
	sendBuf := bufConn.Bytes()

	// Check if compresion is enabled
	if m.config.EnableCompression && m.ProtocolVersion() >= 1 {
		compBuf, err := compressPayload(bufConn.Bytes())
		if err != nil {
			m.logger.Printf("[ERROR] Failed to compress local state: %v", err)
		} else {
			sendBuf = compBuf.Bytes()
		}
	}

	// Check if encryption is enabled
	if m.derivedKey != nil {
		crypt, err := m.encryptLocalState(sendBuf)
		if err != nil {
			m.logger.Printf("[ERROR] Failed to encrypt local state: %v", err)
			return err
		}
		sendBuf = crypt
	}

	// Write out the entire send buffer
	if _, err := conn.Write(sendBuf); err != nil {
		return err
	}
	return nil
}

// encryptLocalState is used to help encrypt local state before sending
func (m *Memberlist) encryptLocalState(sendBuf []byte) ([]byte, error) {
	cipherText, err := encryptPayload(m.derivedKey, sendBuf)
	if err != nil {
		return nil, err
	}

	// Encode the length of the ciphertext
	sizeBuf := make([]byte, 4)
	msgLen := cipherText.Len() + hmacLength + 5
	binary.BigEndian.PutUint32(sizeBuf, uint32(msgLen))

	// Prefix cipherText with msgType and length
	buf := bytes.NewBuffer(nil)
	buf.WriteByte(byte(encryptMsg))
	buf.Write(sizeBuf)
	buf.Write(cipherText.Bytes())

	// Append an HMAC signature
	if err := hmacPayload(m.derivedKey, buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decryptRemoteState is used to help decrypt the remote state
func (m *Memberlist) decryptRemoteState(bufConn io.Reader) ([]byte, error) {
	// Read in enough to determine message length
	cipherText := bytes.NewBuffer(nil)
	cipherText.WriteByte(byte(encryptMsg))
	_, err := io.CopyN(cipherText, bufConn, 4)
	if err != nil {
		return nil, err
	}

	// Ensure we aren't asked to download too much. This is to guard against
	// an attack vector where a huge amount of state is sent
	moreBytes := binary.BigEndian.Uint32(cipherText.Bytes()[1:5])
	if moreBytes > maxPushStateBytes {
		return nil, fmt.Errorf("Remote node state is larger than limit (%d)", moreBytes)
	}

	// Read in the rest of the payload, with the HMAC
	_, err = io.CopyN(cipherText, bufConn, int64(moreBytes)-5)
	if err != nil {
		return nil, err
	}

	// Verify the HMAC
	if err := hmacVerifyPayload(m.derivedKey, cipherText.Bytes()); err != nil {
		return nil, err
	}

	// Decrypt the cipherText
	n := cipherText.Len() - hmacLength
	cipherBytes := cipherText.Bytes()[5:n]
	return decryptPayload(m.derivedKey, cipherBytes)
}

// recvRemoteState is used to read the remote state from a connection
func (m *Memberlist) readRemoteState(conn net.Conn) ([]pushNodeState, []byte, error) {
	// Created a buffered reader
	var bufConn io.Reader = bufio.NewReader(conn)

	// Read the message type
	buf := [1]byte{0}
	if _, err := bufConn.Read(buf[:]); err != nil {
		return nil, nil, err
	}
	msgType := messageType(buf[0])

	// Check if the message is encrypted
	if msgType == encryptMsg {
		if m.derivedKey == nil {
			return nil, nil,
				fmt.Errorf("Remote state is encrypted and SecretKey is not configured")
		}

		plain, err := m.decryptRemoteState(bufConn)
		if err != nil {
			return nil, nil, err
		}

		// Reset message type and bufConn
		msgType = messageType(plain[0])
		bufConn = bytes.NewReader(plain[1:])
	}

	// Get the msgPack decoders
	hd := codec.MsgpackHandle{}
	dec := codec.NewDecoder(bufConn, &hd)

	// Check if we have a compressed message
	if msgType == compressMsg {
		var c compress
		if err := dec.Decode(&c); err != nil {
			return nil, nil, err
		}
		decomp, err := decompressBuffer(&c)
		if err != nil {
			return nil, nil, err
		}

		// Reset the message type
		msgType = messageType(decomp[0])

		// Create a new bufConn
		bufConn = bytes.NewReader(decomp[1:])

		// Create a new decoder
		dec = codec.NewDecoder(bufConn, &hd)
	}

	// Quit if not push/pull
	if msgType != pushPullMsg {
		err := fmt.Errorf("received invalid msgType (%d)", msgType)
		return nil, nil, err
	}

	// Read the push/pull header
	var header pushPullHeader
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
		bytes, err := bufConn.Read(userBuf)
		if err == nil && bytes != header.UserStateLen {
			err = fmt.Errorf(
				"Failed to read full user state (%d / %d)",
				bytes, header.UserStateLen)
		}
		if err != nil {
			return remoteNodes, nil, err
		}
	}

	return remoteNodes, userBuf, nil
}
