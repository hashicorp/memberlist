package memberlist

import (
	"bytes"
	"encoding/binary"
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
)

const (
	udpBufSize = 65536
	udpSendBuf = 1500
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
}

// dead is broadcast when we confirm a node is dead
// Overloaded for nodes leaving
type dead struct {
	Incarnation uint32
	Node        string
}

// tcpListen listens for and handles incoming connections
func (m *Memberlist) tcpListen() {
	for {
		conn, err := m.tcpListener.AcceptTCP()
		if err != nil {
			if neterr, ok := err.(net.Error); ok && !neterr.Temporary() {
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
}

// udpListen listens for and handles incoming UDP packets
func (m *Memberlist) udpListen() {
	mainBuf := make([]byte, udpBufSize)
	var n int
	var msgType uint32
	var addr net.Addr
	var err error
	for {
		// Reset buffer
		buf := mainBuf[0:udpBufSize]

		// Read a packet
		n, addr, err = m.udpListener.ReadFrom(buf)
		if err != nil {
			if neterr, ok := err.(net.Error); ok && !neterr.Temporary() {
				break
			}
			log.Printf("[ERR] Error reading UDP packet: %s", err)
			continue
		}

		// Trim the buffer size
		buf = buf[0:n]

		// Check the length
		if len(buf) < 4 {
			log.Printf("[ERR] UDP packet too short (%d bytes). From: %s", len(buf), addr)
			continue
		}

		// Decode the message type
		msgType = binary.BigEndian.Uint32(buf[0:4])
		buf = buf[4:]

		// Switch on the msgType
		switch msgType {
		case pingMsg:
			m.handlePing(buf, addr)
		case indirectPingMsg:
			m.handleIndirectPing(buf, addr)
		case ackRespMsg:
			m.handleAck(buf, addr)
		case suspectMsg:
			m.handleSuspect(buf, addr)
		case aliveMsg:
			m.handleAlive(buf, addr)
		case deadMsg:
			m.handleDead(buf, addr)
		default:
			log.Printf("[ERR] UDP msg type (%d) not supported. From: %s", msgType, addr)
			continue
		}
	}
}

func (m *Memberlist) handlePing(buf []byte, from net.Addr) {
	var p ping
	if err := decode(buf, &p); err != nil {
		log.Printf("[ERR] Failed to decode ping request: %s", err)
		return
	}
	ack := ackResp{p.SeqNo}
	out, err := encode(ackRespMsg, ack)
	if err != nil {
		log.Printf("[ERR] Failed to encode ack response: %s", err)
		return
	}
	if err := m.sendMsg(from, out); err != nil {
		log.Printf("[ERR] Failed to send ack: %s", err)
	}
}

func (m *Memberlist) handleIndirectPing(buf []byte, from net.Addr) {
}

func (m *Memberlist) handleAck(buf []byte, from net.Addr) {
}

func (m *Memberlist) handleSuspect(buf []byte, from net.Addr) {
}

func (m *Memberlist) handleAlive(buf []byte, from net.Addr) {
}

func (m *Memberlist) handleDead(buf []byte, from net.Addr) {
}

// sendMsg is used to send a UDP message to another host
func (m *Memberlist) sendMsg(to net.Addr, msg *bytes.Buffer) error {
	_, err := m.udpListener.WriteTo(msg.Bytes(), to)
	return err
}
