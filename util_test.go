package memberlist

import (
	"fmt"
	"reflect"
	"testing"
	"time"
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
	if err := decode(buf.Bytes()[1:], &out); err != nil {
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

func TestRandomOffset_Zero(t *testing.T) {
	offset := randomOffset(0)
	if offset != 0 {
		t.Fatalf("bad offset")
	}
}

func TestNotifyAll(t *testing.T) {
	ch1 := make(chan *Node, 1)
	ch2 := make(chan *Node, 1)
	ch3 := make(chan *Node, 1)

	// Make sure ch1 is full
	ch1 <- &Node{Name: "test"}

	// Notify all
	n := &Node{Name: "Push"}
	notifyAll([]chan<- *Node{ch1, ch2, ch3}, n)

	v := <-ch1
	if v.Name != "test" {
		t.Fatalf("bad name")
	}

	// Test receive
	select {
	case v := <-ch1:
		t.Fatalf("bad node %v", v)
	default:
	}

	select {
	case v := <-ch2:
		if v != n {
			t.Fatalf("bad node %v", v)
		}
	default:
		t.Fatalf("nothing on channel")
	}

	select {
	case v := <-ch3:
		if v != n {
			t.Fatalf("bad node %v", v)
		}
	default:
		t.Fatalf("nothing on channel")
	}
}

func TestSuspicionTimeout(t *testing.T) {
	timeout := suspicionTimeout(3, 10, time.Second)
	if timeout != 6*time.Second {
		t.Fatalf("bad timeout")
	}
}

func TestRetransmitLimit(t *testing.T) {
	lim := retransmitLimit(3, 0)
	if lim != 0 {
		t.Fatalf("bad val %v", lim)
	}
	lim = retransmitLimit(3, 1)
	if lim != 3 {
		t.Fatalf("bad val %v", lim)
	}
	lim = retransmitLimit(3, 99)
	if lim != 6 {
		t.Fatalf("bad val %v", lim)
	}
}

func TestShuffleNodes(t *testing.T) {
	orig := []*NodeState{
		&NodeState{
			State: StateDead,
		},
		&NodeState{
			State: StateAlive,
		},
		&NodeState{
			State: StateAlive,
		},
		&NodeState{
			State: StateDead,
		},
		&NodeState{
			State: StateAlive,
		},
	}
	nodes := make([]*NodeState, 5)
	copy(nodes, orig)

	if !reflect.DeepEqual(nodes, orig) {
		t.Fatalf("should match")
	}

	shuffleNodes(nodes)

	if reflect.DeepEqual(nodes, orig) {
		t.Fatalf("should not match")
	}
}

func TestMoveDeadNodes(t *testing.T) {
	nodes := []*NodeState{
		&NodeState{
			State: StateDead,
		},
		&NodeState{
			State: StateAlive,
		},
		&NodeState{
			State: StateAlive,
		},
		&NodeState{
			State: StateDead,
		},
		&NodeState{
			State: StateAlive,
		},
	}

	idx := moveDeadNodes(nodes)
	if idx != 3 {
		t.Fatalf("bad index")
	}
	for i := 0; i < idx; i++ {
		if nodes[i].State != StateAlive {
			t.Fatalf("Bad state %d", i)
		}
	}
	for i := idx; i < len(nodes); i++ {
		if nodes[i].State != StateDead {
			t.Fatalf("Bad state %d", i)
		}
	}
}

func TestKRandomNodes(t *testing.T) {
	nodes := []*NodeState{}
	for i := 0; i < 30; i++ {
		// Half the nodes are in a bad state
		state := StateAlive
		switch i % 3 {
		case 0:
			state = StateAlive
		case 1:
			state = StateSuspect
		case 2:
			state = StateDead
		}
		nodes = append(nodes, &NodeState{
			Node: Node{
				Name: fmt.Sprintf("test%d", i),
			},
			State: state,
		})
	}

	s1 := kRandomNodes(3, []string{"test0"}, nodes)
	s2 := kRandomNodes(3, []string{"test0"}, nodes)
	s3 := kRandomNodes(3, []string{"test0"}, nodes)

	if reflect.DeepEqual(s1, s2) {
		t.Fatalf("unexpected equal")
	}
	if reflect.DeepEqual(s1, s3) {
		t.Fatalf("unexpected equal")
	}
	if reflect.DeepEqual(s2, s3) {
		t.Fatalf("unexpected equal")
	}

	for _, s := range [][]*NodeState{s1, s2, s3} {
		if len(s) != 3 {
			t.Fatalf("bad len")
		}
		for _, n := range s {
			if n.Name == "test0" {
				t.Fatalf("Bad name")
			}
			if n.State != StateAlive {
				t.Fatalf("Bad state")
			}
		}
	}
}
