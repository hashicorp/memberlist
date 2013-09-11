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

func TestMemberList_SuspectNode_NoNode(t *testing.T) {
	m := GetMemberlist(t)
	s := suspect{Node: "test", Incarnation: 1}
	m.suspectNode(&s)
	if len(m.nodes) != 0 {
		t.Fatalf("don't expect nodes")
	}
}

func TestMemberList_SuspectNode(t *testing.T) {
	m := GetMemberlist(t)
	m.config.Interval = time.Millisecond
	m.config.SuspicionMult = 1
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	s := suspect{Node: "test", Incarnation: 1}
	m.suspectNode(&s)

	if state.State != StateSuspect {
		t.Fatalf("Bad state")
	}

	change := state.StateChange
	if time.Now().Sub(change) > time.Second {
		t.Fatalf("bad change delta")
	}

	// Wait for the timeout
	time.Sleep(10 * time.Millisecond)

	if state.State != StateDead {
		t.Fatalf("Bad state")
	}

	if time.Now().Sub(state.StateChange) > time.Second {
		t.Fatalf("bad change delta")
	}
	if !state.StateChange.After(change) {
		t.Fatalf("should increment time")
	}
}

func TestMemberList_SuspectNode_DoubleSuspect(t *testing.T) {
	m := GetMemberlist(t)
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	s := suspect{Node: "test", Incarnation: 1}
	m.suspectNode(&s)

	if state.State != StateSuspect {
		t.Fatalf("Bad state")
	}

	change := state.StateChange
	if time.Now().Sub(change) > time.Second {
		t.Fatalf("bad change delta")
	}

	// Suspect again
	m.suspectNode(&s)

	if state.StateChange != change {
		t.Fatalf("unexpected state change")
	}
}

func TestMemberList_SuspectNode_OldSuspect(t *testing.T) {
	m := GetMemberlist(t)
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 10}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	s := suspect{Node: "test", Incarnation: 1}
	m.suspectNode(&s)

	if state.State != StateAlive {
		t.Fatalf("Bad state")
	}
}

func TestMemberList_DeadNode_NoNode(t *testing.T) {
	m := GetMemberlist(t)
	d := dead{Node: "test", Incarnation: 1}
	m.deadNode(&d)
	if len(m.nodes) != 0 {
		t.Fatalf("don't expect nodes")
	}
}

func TestMemberList_DeadNode(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)
	m.NotifyLeave(ch)
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	d := dead{Node: "test", Incarnation: 1}
	m.deadNode(&d)

	if state.State != StateDead {
		t.Fatalf("Bad state")
	}

	change := state.StateChange
	if time.Now().Sub(change) > time.Second {
		t.Fatalf("bad change delta")
	}

	select {
	case leave := <-ch:
		if leave.Name != "test" {
			t.Fatalf("bad node name")
		}
	default:
		t.Fatalf("no leave message")
	}
}

func TestMemberList_DeadNode_Double(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	d := dead{Node: "test", Incarnation: 1}
	m.deadNode(&d)

	// Notify after the first dead
	m.NotifyLeave(ch)

	// Should do nothing
	d.Incarnation = 2
	m.deadNode(&d)

	select {
	case <-ch:
		t.Fatalf("should not get leave")
	default:
	}
}

func TestMemberList_DeadNode_OldDead(t *testing.T) {
	m := GetMemberlist(t)
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 10}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	d := dead{Node: "test", Incarnation: 1}
	m.deadNode(&d)

	if state.State != StateAlive {
		t.Fatalf("Bad state")
	}
}
