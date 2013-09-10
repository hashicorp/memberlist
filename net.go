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

const (
	udpBufSize = 65536
	udpSendBuf = 1500
)

// ping request sent directly to node
type ping struct {
	SeqNo int
}

// indirect ping sent to an indirect ndoe
type indirectPingReq struct {
	SeqNo  int
	Target string
}

// ack response is sent for a ping
type ackResp struct {
	SeqNo int
}

// suspect is broadcast when we suspect a node is dead
type suspect struct {
	Incarnation int
	Node        string
}

// alive is broadcast when we know a node is alive.
// Overloaded for nodes joining
type alive struct {
	Incarnation int
	Node        string
}

// dead is broadcast when we confirm a node is dead
// Overloaded for nodes leaving
type dead struct {
	Incarnation int
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
