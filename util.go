package memberlist

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/ugorji/go/codec"
	"math"
	"math/rand"
	"net"
	"time"
)

/*
 * Contains an entry for each private block:
 * 10.0.0.0/8
 * 172.16.0.0/12
 * 192.168/16
 */
var privateBlocks []*net.IPNet

func init() {
	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Add each private block
	privateBlocks = make([]*net.IPNet, 3)
	_, block, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		panic(fmt.Sprintf("Bad cidr. Got %v", err))
	}
	privateBlocks[0] = block

	_, block, err = net.ParseCIDR("172.16.0.0/12")
	if err != nil {
		panic(fmt.Sprintf("Bad cidr. Got %v", err))
	}
	privateBlocks[1] = block

	_, block, err = net.ParseCIDR("192.168.0.0/16")
	if err != nil {
		panic(fmt.Sprintf("Bad cidr. Got %v", err))
	}
	privateBlocks[2] = block
}

// Decode uses a GOB decoder on a byte slice
func decode(buf []byte, out interface{}) error {
	r := bytes.NewBuffer(buf)
	hd := codec.MsgpackHandle{}
	dec := codec.NewDecoder(r, &hd)
	return dec.Decode(out)
}

// Encode writes a GOB encoded object to a new bytes buffer
func encode(msgType int, in interface{}) (*bytes.Buffer, error) {
	buf := bytes.NewBuffer(nil)
	buf.WriteByte(uint8(msgType))
	hd := codec.MsgpackHandle{}
	enc := codec.NewEncoder(buf, &hd)
	err := enc.Encode(in)
	return buf, err
}

// Returns a random offset between 0 and n
func randomOffset(n int) int {
	if n == 0 {
		return 0
	}
	return int(rand.Uint32() % uint32(n))
}

// Does a non-blocking notify
func notify(ch chan<- *Node, n *Node) {
	if ch == nil {
		return
	}
	select {
	case ch <- n:
	default:
	}
}

// suspicionTimeout computes the timeout that should be used when
// a node is suspected
func suspicionTimeout(suspicionMult, n int, interval time.Duration) time.Duration {
	nodeScale := math.Ceil(math.Log10(float64(n + 1)))
	timeout := time.Duration(suspicionMult) * time.Duration(nodeScale) * interval
	return timeout
}

// retransmitLimit computes the limit of retransmissions
func retransmitLimit(retransmitMult, n int) int {
	nodeScale := math.Ceil(math.Log10(float64(n + 1)))
	limit := retransmitMult * int(nodeScale)
	return limit
}

// shuffleNodes randomly shuffles the input nodes
func shuffleNodes(nodes []*NodeState) {
	for i := range nodes {
		j := rand.Intn(i + 1)
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
}

// moveDeadNodes moves all the nodes in the dead state
// to the end of the slice and returns the index of the first dead node.
func moveDeadNodes(nodes []*NodeState) int {
	numDead := 0
	n := len(nodes)
	for i := 0; i < n-numDead; i++ {
		if nodes[i].State != StateDead {
			continue
		}

		// Move this node to the end
		nodes[i], nodes[n-numDead-1] = nodes[n-numDead-1], nodes[i]
		numDead++
		i--
	}
	return n - numDead
}

// kRandomNodes is used to select up to k random nodes, excluding a given
// node and any non-alive nodes. It is possible that less than k nodes are returned.
func kRandomNodes(k int, excludes []string, nodes []*NodeState) []*NodeState {
	n := len(nodes)
	kNodes := make([]*NodeState, 0, k)
OUTER:
	// Probe up to 3*n times, with large n this is not necessary
	// since k << n, but with small n we want search to be
	// exhaustive
	for i := 0; i < 3*n && len(kNodes) < k; i++ {
		// Get random node
		idx := randomOffset(n)
		node := nodes[idx]

		// Exclude node if match
		for _, exclude := range excludes {
			if node.Name == exclude {
				continue OUTER
			}
		}

		// Exclude if not alive
		if node.State != StateAlive {
			continue
		}

		// Check if we have this node already
		for j := 0; j < len(kNodes); j++ {
			if node == kNodes[j] {
				continue OUTER
			}
		}

		// Append the node
		kNodes = append(kNodes, node)
	}
	return kNodes
}

// makeCompoundMessage takes a list of messages and generates
// a single compound message containing all of them
func makeCompoundMessage(msgs []*bytes.Buffer) *bytes.Buffer {
	// Create a local buffer
	buf := bytes.NewBuffer(nil)

	// Write out the type
	buf.WriteByte(uint8(compoundMsg))

	// Write out the number of message
	buf.WriteByte(uint8(len(msgs)))

	// Add the message lengths
	for _, m := range msgs {
		binary.Write(buf, binary.BigEndian, uint16(m.Len()))
	}

	// Append the messages
	for _, m := range msgs {
		buf.Write(m.Bytes())
	}

	return buf
}

// decodeCompoundMessage splits a compound message and returns
// the slices of individual messages. Also returns the number
// of truncated messages and any potential error
func decodeCompoundMessage(buf []byte) (trunc int, parts [][]byte, err error) {
	if len(buf) < 1 {
		err = fmt.Errorf("missing compound length byte")
		return
	}
	numParts := uint8(buf[0])
	buf = buf[1:]

	// Check we have enough bytes
	if len(buf) < int(numParts*2) {
		err = fmt.Errorf("truncated len slice")
		return
	}

	// Decode the lengths
	lengths := make([]uint16, numParts)
	for i := 0; i < int(numParts); i++ {
		lengths[i] = binary.BigEndian.Uint16(buf[i*2 : i*2+2])
	}
	buf = buf[numParts*2:]

	// Split each message
	for idx, msgLen := range lengths {
		if len(buf) < int(msgLen) {
			trunc = int(numParts) - idx
			return
		}

		// Extract the slice, seek past on the buffer
		slice := buf[:msgLen]
		buf = buf[msgLen:]
		parts = append(parts, slice)
	}
	return
}

// Returns if the given IP is in a private block
func isPrivateIP(ip_str string) bool {
	ip := net.ParseIP(ip_str)
	for _, priv := range privateBlocks {
		if priv.Contains(ip) {
			return true
		}
	}
	return false
}
