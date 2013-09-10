package memberlist

import (
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

// msgHeader is prefixed to messages to indicate type
type msgHeader struct {
	msgType int
}

// ping request sent directly to node
type ping struct {
	seqNo int
}

// indirect ping sent to an indirect ndoe
type indirectPing struct {
	seqNo  int
	target string
}

// ack response is sent for a ping
type ackResp struct {
	seqNo int
}

// suspect is broadcast when we suspect a node is dead
type suspect struct {
	incarnation int
	node        string
}

// alive is broadcast when we know a node is alive.
// Overloaded for nodes joining
type alive struct {
	incarnation int
	node        string
}

// dead is broadcast when we confirm a node is dead
// Overloaded for nodes leaving
type dead struct {
	incarnation int
	node        string
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
		}
		go m.handleConn(conn)
	}
}

// handleConn handles a single incoming TCP connection
func (m *Memberlist) handleConn(conn *net.TCPConn) {
}

// udpListen listens for and handles incoming UDP packets
func (m *Memberlist) udpListen() {
}
