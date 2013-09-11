package memberlist

import (
	"testing"
	"time"
)

func TestMemberList_NextSeq(t *testing.T) {
	m := &Memberlist{}
	if m.nextSeqNo() != 1 {
		t.Fatalf("bad sequence no")
	}
	if m.nextSeqNo() != 2 {
		t.Fatalf("bad sequence no")
	}
}

func TestMemberList_SetAckHandler(t *testing.T) {
	m := &Memberlist{ackHandlers: make(map[uint32]*ackHandler)}

	f := func() {}
	m.setAckHandler(0, f, 10*time.Millisecond)

	if _, ok := m.ackHandlers[0]; !ok {
		t.Fatalf("missing handler")
	}
	time.Sleep(11 * time.Millisecond)

	if _, ok := m.ackHandlers[0]; ok {
		t.Fatalf("non-reaped handler")
	}
}

func TestMemberList_InvokeAckHandler(t *testing.T) {
	m := &Memberlist{ackHandlers: make(map[uint32]*ackHandler)}

	// Does nothing
	m.invokeAckHandler(0)

	var b bool
	f := func() { b = true }
	m.setAckHandler(0, f, 10*time.Millisecond)

	// Should set b
	m.invokeAckHandler(0)
	if !b {
		t.Fatalf("b not set")
	}

	if _, ok := m.ackHandlers[0]; ok {
		t.Fatalf("non-reaped handler")
	}
}

func TestMemberList_AliveNode_NewNode(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)
	m.NotifyJoin(ch)

	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	if len(m.nodes) != 1 {
		t.Fatalf("should add node")
	}

	state, ok := m.nodeMap["test"]
	if !ok {
		t.Fatalf("should map node")
	}

	if state.Incarnation != 1 {
		t.Fatalf("bad incarnation")
	}
	if state.State != StateAlive {
		t.Fatalf("bad state")
	}
	if time.Now().Sub(state.StateChange) > time.Second {
		t.Fatalf("bad change delta")
	}

	// Check for a join message
	select {
	case join := <-ch:
		if join.Name != "test" {
			t.Fatalf("bad node name")
		}
	default:
		t.Fatalf("no join message")
	}
}

func TestMemberList_AliveNode_SuspectNode(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)

	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	// Listen only after first join
	m.NotifyJoin(ch)

	// Make suspect
	state := m.nodeMap["test"]
	state.State = StateSuspect
	state.StateChange = state.StateChange.Add(-time.Hour)

	// Old incarnation number, should not change
	m.aliveNode(&a)
	if state.State != StateSuspect {
		t.Fatalf("update with old incarnation!")
	}

	// Should reset to alive now
	a.Incarnation = 2
	m.aliveNode(&a)
	if state.State != StateAlive {
		t.Fatalf("no update with new incarnation!")
	}

	if time.Now().Sub(state.StateChange) > time.Second {
		t.Fatalf("bad change delta")
	}

	// Check for a no join message
	select {
	case <-ch:
		t.Fatalf("got bad join message")
	default:
	}
}

func TestMemberList_AliveNode_Idempotent(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)

	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	// Listen only after first join
	m.NotifyJoin(ch)

	// Make suspect
	state := m.nodeMap["test"]
	stateTime := state.StateChange

	// Should reset to alive now
	a.Incarnation = 2
	m.aliveNode(&a)
	if state.State != StateAlive {
		t.Fatalf("non idempotent")
	}

	if stateTime != state.StateChange {
		t.Fatalf("should not change state")
	}

	// Check for a no join message
	select {
	case <-ch:
		t.Fatalf("got bad join message")
	default:
	}
}
