package memberlist

import (
	"net"
)

// channelIndex returns the index of a channel in a list or
// -1 if not found
func channelIndex(list []chan<- net.Addr, ch chan<- net.Addr) int {
	for i := 0; i < len(list); i++ {
		if list[i] == ch {
			return i
		}
	}
	return -1
}

// channelDelete takes an index and removes the element at that index
func channelDelete(list []chan<- net.Addr, idx int) []chan<- net.Addr {
	n := len(list)
	list[idx], list[n-1] = list[n-1], nil
	return list[0 : n-1]
}
