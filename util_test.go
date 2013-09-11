package memberlist

import (
	"testing"
)

func TestChannelIndex(t *testing.T) {
	ch1 := make(chan *Node)
	ch2 := make(chan *Node)
	ch3 := make(chan *Node)
	list := []chan<- *Node{ch1, ch2, ch3}

	if channelIndex(list, ch1) != 0 {
		t.Fatalf("bad index")
	}
	if channelIndex(list, ch2) != 1 {
		t.Fatalf("bad index")
	}
	if channelIndex(list, ch3) != 2 {
		t.Fatalf("bad index")
	}

	ch4 := make(chan *Node)
	if channelIndex(list, ch4) != -1 {
		t.Fatalf("bad index")
	}
}

func TestChannelIndex_Empty(t *testing.T) {
	ch := make(chan *Node)
	if channelIndex(nil, ch) != -1 {
		t.Fatalf("bad index")
	}
}

func TestChannelDelete(t *testing.T) {
	ch1 := make(chan *Node)
	ch2 := make(chan *Node)
	ch3 := make(chan *Node)
	list := []chan<- *Node{ch1, ch2, ch3}

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

func TestEncodeDecode(t *testing.T) {
	msg := &ping{SeqNo: 100}
	buf, err := encode(pingMsg, msg)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}
	var out ping
	if err := decode(buf.Bytes()[4:], &out); err != nil {
		t.Fatalf("unexpected err: %s", err)
	}
	if msg.SeqNo != out.SeqNo {
		t.Fatalf("bad sequence no")
	}
}

func TestRandomOffset(t *testing.T) {
	vals := make(map[int]struct{})
	for i := 0; i < 100; i++ {
		offset := randomOffset(2 << 30)
		if _, ok := vals[offset]; ok {
			t.Fatalf("got collision")
		}
		vals[offset] = struct{}{}
	}
}
