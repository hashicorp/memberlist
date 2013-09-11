package memberlist

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"math"
	"math/rand"
	"time"
)

// channelIndex returns the index of a channel in a list or
// -1 if not found
func channelIndex(list []chan<- *Node, ch chan<- *Node) int {
	for i := 0; i < len(list); i++ {
		if list[i] == ch {
			return i
		}
	}
	return -1
}

// channelDelete takes an index and removes the element at that index
func channelDelete(list []chan<- *Node, idx int) []chan<- *Node {
	n := len(list)
	list[idx], list[n-1] = list[n-1], nil
	return list[0 : n-1]
}

// Decode uses a GOB decoder on a byte slice
func decode(buf []byte, out interface{}) error {
	r := bytes.NewBuffer(buf)
	dec := gob.NewDecoder(r)
	return dec.Decode(out)
}

// Encode writes a GOB encoded object to a new bytes buffer
func encode(msgType int, in interface{}) (*bytes.Buffer, error) {
	buf := bytes.NewBuffer(nil)
	binary.Write(buf, binary.BigEndian, uint32(msgType))
	enc := gob.NewEncoder(buf)
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

// Does a non-blocking notify to all listeners
func notifyAll(chans []chan<- *Node, n *Node) {
	for _, c := range chans {
		select {
		case c <- n:
		default:
		}
	}
}

// suspicionTimeout computes the timeout that should be used when
// a node is suspected
func suspicionTimeout(suspicionMult, n int, interval time.Duration) time.Duration {
	nodeScale := math.Ceil(math.Log10(float64(n + 1)))
	timeout := time.Duration(suspicionMult) * time.Duration(nodeScale) * interval
	return timeout
}
