package memberlist

import (
	"net"
	"testing"
)

func TestChannelIndex(t *testing.T) {
	ch1 := make(chan net.Addr)
	ch2 := make(chan net.Addr)
	ch3 := make(chan net.Addr)
	list := []chan<- net.Addr{ch1, ch2, ch3}

	if channelIndex(list, ch1) != 0 {
		t.Fatalf("bad index")
	}
	if channelIndex(list, ch2) != 1 {
		t.Fatalf("bad index")
	}
	if channelIndex(list, ch3) != 2 {
		t.Fatalf("bad index")
	}

	ch4 := make(chan net.Addr)
	if channelIndex(list, ch4) != -1 {
		t.Fatalf("bad index")
	}
}

func TestChannelIndex_Empty(t *testing.T) {
	ch := make(chan net.Addr)
	if channelIndex(nil, ch) != -1 {
		t.Fatalf("bad index")
	}
}

func TestChannelDelete(t *testing.T) {
	ch1 := make(chan net.Addr)
	ch2 := make(chan net.Addr)
	ch3 := make(chan net.Addr)
	list := []chan<- net.Addr{ch1, ch2, ch3}

	// Delete ch2
	list = channelDelete(list, 1)

	if len(list) != 2 {
		t.Fatalf("bad len")
	}
	if channelIndex(list, ch1) != 0 {
		t.Fatalf("bad index")
	}
	if channelIndex(list, ch3) != 1 {
		t.Fatalf("bad index")
	}
}
