package memberlist

import (
	"testing"
	"time"
)

func HostMemberlist(host string, t *testing.T, f func(*Config)) *Memberlist {
	c := DefaultConfig()
	c.Name = host
	c.BindAddr = host
	if f != nil {
		f(c)
	}

	m, err := newMemberlist(c)
	if err != nil {
		t.Fatalf("failed to get memberlist: %s", err)
	}
	return m
}

func TestMemberList_Probe(t *testing.T) {
	addr1, ip1 := GetBindAddr()
	addr2, ip2 := GetBindAddr()
	m1 := HostMemberlist(addr1, t, func(c *Config) {
		c.RTT = time.Millisecond
		c.ProbeInterval = 10 * time.Millisecond
	})
	m2 := HostMemberlist(addr2, t, nil)

	a1 := alive{Node: addr1, Addr: ip1, Incarnation: 1}
	m1.aliveNode(&a1)
	a2 := alive{Node: addr2, Addr: ip2, Incarnation: 1}
	m1.aliveNode(&a2)

	// should ping addr2
	m1.probe()

	// Should not be marked suspect
	n := m1.nodeMap[addr2]
	if n.State != stateAlive {
		t.Fatalf("Expect node to be alive")
	}

	// Should increment seqno
	if m1.sequenceNum != 1 {
		t.Fatalf("bad seqno %v", m2.sequenceNum)
	}
}

func TestMemberList_ProbeNode_Suspect(t *testing.T) {
	addr1, ip1 := GetBindAddr()
	addr2, ip2 := GetBindAddr()
	addr3, ip3 := GetBindAddr()
	addr4, ip4 := GetBindAddr()

	m1 := HostMemberlist(addr1, t, func(c *Config) {
		c.RTT = time.Millisecond
		c.ProbeInterval = 10 * time.Millisecond
	})
	m2 := HostMemberlist(addr2, t, nil)
	m3 := HostMemberlist(addr3, t, nil)

	a1 := alive{Node: addr1, Addr: ip1, Incarnation: 1}
	m1.aliveNode(&a1)
	a2 := alive{Node: addr2, Addr: ip2, Incarnation: 1}
	m1.aliveNode(&a2)
	a3 := alive{Node: addr3, Addr: ip3, Incarnation: 1}
	m1.aliveNode(&a3)
	a4 := alive{Node: addr4, Addr: ip4, Incarnation: 1}
	m1.aliveNode(&a4)

	n := m1.nodeMap[addr4]
	m1.probeNode(n)

	// Should be marked suspect
	if n.State != stateSuspect {
		t.Fatalf("Expect node to be suspect")
	}
	time.Sleep(5 * time.Millisecond)

	// Should increment seqno
	if m2.sequenceNum != 1 {
		t.Fatalf("bad seqno %v", m2.sequenceNum)
	}
	if m3.sequenceNum != 1 {
		t.Fatalf("bad seqno %v", m3.sequenceNum)
	}
}

func TestMemberList_ProbeNode(t *testing.T) {
	addr1, ip1 := GetBindAddr()
	addr2, ip2 := GetBindAddr()

	m1 := HostMemberlist(addr1, t, func(c *Config) {
		c.RTT = time.Millisecond
		c.ProbeInterval = 10 * time.Millisecond
	})
	m2 := HostMemberlist(addr2, t, nil)

	a1 := alive{Node: addr1, Addr: ip1, Incarnation: 1}
	m1.aliveNode(&a1)
	a2 := alive{Node: addr2, Addr: ip2, Incarnation: 1}
	m1.aliveNode(&a2)

	n := m1.nodeMap[addr2]
	m1.probeNode(n)

	// Should be marked suspect
	if n.State != stateAlive {
		t.Fatalf("Expect node to be alive")
	}

	// Should increment seqno
	if m1.sequenceNum != 1 {
		t.Fatalf("bad seqno %v", m2.sequenceNum)
	}
}

func TestMemberList_ResetNodes(t *testing.T) {
	m := GetMemberlist(t)
	a1 := alive{Node: "test1", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a1)
	a2 := alive{Node: "test2", Addr: []byte{127, 0, 0, 2}, Incarnation: 1}
	m.aliveNode(&a2)
	a3 := alive{Node: "test3", Addr: []byte{127, 0, 0, 3}, Incarnation: 1}
	m.aliveNode(&a3)
	d := dead{Node: "test2", Incarnation: 1}
	m.deadNode(&d)

	m.resetNodes()
	if len(m.nodes) != 2 {
		t.Fatalf("Bad length")
	}
	if _, ok := m.nodeMap["test2"]; ok {
		t.Fatalf("test2 should be unmapped")
	}
}

func TestMemberList_NextSeq(t *testing.T) {
	m := &Memberlist{}
	if m.nextSeqNo() != 1 {
		t.Fatalf("bad sequence no")
	}
	if m.nextSeqNo() != 2 {
		t.Fatalf("bad sequence no")
	}
}

func TestMemberList_SetAckChannel(t *testing.T) {
	m := &Memberlist{ackHandlers: make(map[uint32]*ackHandler)}

	ch := make(chan bool, 1)
	m.setAckChannel(0, ch, 10*time.Millisecond)

	if _, ok := m.ackHandlers[0]; !ok {
		t.Fatalf("missing handler")
	}
	time.Sleep(11 * time.Millisecond)

	if _, ok := m.ackHandlers[0]; ok {
		t.Fatalf("non-reaped handler")
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

func TestMemberList_InvokeAckHandler_Channel(t *testing.T) {
	m := &Memberlist{ackHandlers: make(map[uint32]*ackHandler)}

	// Does nothing
	m.invokeAckHandler(0)

	ch := make(chan bool, 1)
	m.setAckChannel(0, ch, 10*time.Millisecond)

	// Should send message
	m.invokeAckHandler(0)

	select {
	case v := <-ch:
		if v != true {
			t.Fatalf("Bad value")
		}
	default:
		t.Fatalf("message not sent")
	}

	if _, ok := m.ackHandlers[0]; ok {
		t.Fatalf("non-reaped handler")
	}
}

func TestMemberList_AliveNode_NewNode(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)
	m.config.JoinCh = ch

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
	if state.State != stateAlive {
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

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected queued message")
	}
}

func TestMemberList_AliveNode_SuspectNode(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)

	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	// Listen only after first join
	m.config.JoinCh = ch

	// Make suspect
	state := m.nodeMap["test"]
	state.State = stateSuspect
	state.StateChange = state.StateChange.Add(-time.Hour)

	// Old incarnation number, should not change
	m.aliveNode(&a)
	if state.State != stateSuspect {
		t.Fatalf("update with old incarnation!")
	}

	// Should reset to alive now
	a.Incarnation = 2
	m.aliveNode(&a)
	if state.State != stateAlive {
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

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected queued message")
	}
}

func TestMemberList_AliveNode_Idempotent(t *testing.T) {
	ch := make(chan *Node, 1)
	m := GetMemberlist(t)

	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	// Listen only after first join
	m.config.JoinCh = ch

	// Make suspect
	state := m.nodeMap["test"]
	stateTime := state.StateChange

	// Should reset to alive now
	a.Incarnation = 2
	m.aliveNode(&a)
	if state.State != stateAlive {
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

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected only one queued message")
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
	m.config.ProbeInterval = time.Millisecond
	m.config.SuspicionMult = 1
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	s := suspect{Node: "test", Incarnation: 1}
	m.suspectNode(&s)

	if state.State != stateSuspect {
		t.Fatalf("Bad state")
	}

	change := state.StateChange
	if time.Now().Sub(change) > time.Second {
		t.Fatalf("bad change delta")
	}

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected only one queued message")
	}

	// Check its a suspect message
	if messageType(m.broadcasts.bcQueue[0].b.Message()[0]) != suspectMsg {
		t.Fatalf("expected queued suspect msg")
	}

	// Wait for the timeout
	time.Sleep(10 * time.Millisecond)

	if state.State != stateDead {
		t.Fatalf("Bad state")
	}

	if time.Now().Sub(state.StateChange) > time.Second {
		t.Fatalf("bad change delta")
	}
	if !state.StateChange.After(change) {
		t.Fatalf("should increment time")
	}

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected only one queued message")
	}

	// Check its a suspect message
	if messageType(m.broadcasts.bcQueue[0].b.Message()[0]) != deadMsg {
		t.Fatalf("expected queued dead msg")
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

	if state.State != stateSuspect {
		t.Fatalf("Bad state")
	}

	change := state.StateChange
	if time.Now().Sub(change) > time.Second {
		t.Fatalf("bad change delta")
	}

	// clear the broadcast queue
	m.broadcasts.Reset()

	// Suspect again
	m.suspectNode(&s)

	if state.StateChange != change {
		t.Fatalf("unexpected state change")
	}

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 0 {
		t.Fatalf("expected only one queued message")
	}

}

func TestMemberList_SuspectNode_OldSuspect(t *testing.T) {
	m := GetMemberlist(t)
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 10}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	// Clear queue
	m.broadcasts.Reset()

	s := suspect{Node: "test", Incarnation: 1}
	m.suspectNode(&s)

	if state.State != stateAlive {
		t.Fatalf("Bad state")
	}

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 0 {
		t.Fatalf("expected only one queued message")
	}
}

func TestMemberList_SuspectNode_Refute(t *testing.T) {
	m := GetMemberlist(t)
	a := alive{Node: m.config.Name, Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	// Clear queue
	m.broadcasts.Reset()

	s := suspect{Node: m.config.Name, Incarnation: 1}
	m.suspectNode(&s)

	state := m.nodeMap[m.config.Name]
	if state.State != stateAlive {
		t.Fatalf("should still be alive")
	}

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected only one queued message")
	}

	// Should be alive mesg
	if messageType(m.broadcasts.bcQueue[0].b.Message()[0]) != aliveMsg {
		t.Fatalf("expected queued alive msg")
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
	m.config.LeaveCh = ch
	a := alive{Node: "test", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	state := m.nodeMap["test"]
	state.StateChange = state.StateChange.Add(-time.Hour)

	d := dead{Node: "test", Incarnation: 1}
	m.deadNode(&d)

	if state.State != stateDead {
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

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected only one queued message")
	}

	// Check its a suspect message
	if messageType(m.broadcasts.bcQueue[0].b.Message()[0]) != deadMsg {
		t.Fatalf("expected queued dead msg")
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

	// Clear queue
	m.broadcasts.Reset()

	// Notify after the first dead
	m.config.LeaveCh = ch

	// Should do nothing
	d.Incarnation = 2
	m.deadNode(&d)

	select {
	case <-ch:
		t.Fatalf("should not get leave")
	default:
	}

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 0 {
		t.Fatalf("expected only one queued message")
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

	if state.State != stateAlive {
		t.Fatalf("Bad state")
	}
}

func TestMemberList_DeadNode_Refute(t *testing.T) {
	m := GetMemberlist(t)
	a := alive{Node: m.config.Name, Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a)

	// Clear queue
	m.broadcasts.Reset()

	d := dead{Node: m.config.Name, Incarnation: 1}
	m.deadNode(&d)

	state := m.nodeMap[m.config.Name]
	if state.State != stateAlive {
		t.Fatalf("should still be alive")
	}

	// Check a broad cast is queued
	if m.broadcasts.NumQueued() != 1 {
		t.Fatalf("expected only one queued message")
	}

	// Should be alive mesg
	if messageType(m.broadcasts.bcQueue[0].b.Message()[0]) != aliveMsg {
		t.Fatalf("expected queued alive msg")
	}
}

func TestMemberList_MergeState(t *testing.T) {
	m := GetMemberlist(t)
	a1 := alive{Node: "test1", Addr: []byte{127, 0, 0, 1}, Incarnation: 1}
	m.aliveNode(&a1)
	a2 := alive{Node: "test2", Addr: []byte{127, 0, 0, 2}, Incarnation: 1}
	m.aliveNode(&a2)
	a3 := alive{Node: "test3", Addr: []byte{127, 0, 0, 3}, Incarnation: 1}
	m.aliveNode(&a3)

	s := suspect{Node: "test1", Incarnation: 1}
	m.suspectNode(&s)

	remote := []pushNodeState{
		pushNodeState{
			Name:        "test1",
			Addr:        []byte{127, 0, 0, 1},
			Incarnation: 2,
			State:       stateAlive,
		},
		pushNodeState{
			Name:        "test2",
			Addr:        []byte{127, 0, 0, 2},
			Incarnation: 1,
			State:       stateSuspect,
		},
		pushNodeState{
			Name:        "test3",
			Addr:        []byte{127, 0, 0, 3},
			Incarnation: 1,
			State:       stateDead,
		},
		pushNodeState{
			Name:        "test4",
			Addr:        []byte{127, 0, 0, 4},
			Incarnation: 2,
			State:       stateAlive,
		},
	}

	// Listen for changes
	joinCh := make(chan *Node, 1)
	leaveCh := make(chan *Node, 1)
	m.config.JoinCh = joinCh
	m.config.LeaveCh = leaveCh

	// Merge remote state
	m.mergeState(remote)

	// Check the states
	state := m.nodeMap["test1"]
	if state.State != stateAlive || state.Incarnation != 2 {
		t.Fatalf("Bad state %v", state)
	}

	state = m.nodeMap["test2"]
	if state.State != stateSuspect || state.Incarnation != 1 {
		t.Fatalf("Bad state %v", state)
	}

	state = m.nodeMap["test3"]
	if state.State != stateSuspect {
		t.Fatalf("Bad state %v", state)
	}

	state = m.nodeMap["test4"]
	if state.State != stateAlive || state.Incarnation != 2 {
		t.Fatalf("Bad state %v", state)
	}

	// Check the channels
	select {
	case j := <-joinCh:
		if j.Name != "test4" {
			t.Fatalf("bad node %v", j)
		}
	default:
		t.Fatalf("Expect join")
	}

	select {
	case l := <-leaveCh:
		t.Fatalf("Unexpect leave: %v", l)
	default:
	}
}

func TestMemberlist_Gossip(t *testing.T) {
	ch := make(chan *Node, 3)

	addr1, ip1 := GetBindAddr()
	addr2, ip2 := GetBindAddr()

	m1 := HostMemberlist(addr1, t, func(c *Config) {
		c.GossipInterval = time.Millisecond
	})
	m2 := HostMemberlist(addr2, t, func(c *Config) {
		c.JoinCh = ch
		c.GossipInterval = time.Millisecond
	})

	defer m1.Shutdown()
	defer m2.Shutdown()

	a1 := alive{Node: addr1, Addr: ip1, Incarnation: 1}
	m1.aliveNode(&a1)
	a2 := alive{Node: addr2, Addr: ip2, Incarnation: 1}
	m1.aliveNode(&a2)
	a3 := alive{Node: "172.0.0.1", Addr: []byte{172, 0, 0, 1}, Incarnation: 1}
	m1.aliveNode(&a3)

	// Gossip should send all this to m2
	m1.gossip()

	for i := 0; i < 3; i++ {
		select {
		case <-ch:
		case <-time.After(10 * time.Millisecond):
			t.Fatalf("timeout")
		}
	}
}

func TestMemberlist_PushPull(t *testing.T) {
	addr1, ip1 := GetBindAddr()
	addr2, ip2 := GetBindAddr()

	ch := make(chan *Node, 3)

	m1 := HostMemberlist(addr1, t, func(c *Config) {
		c.GossipInterval = 10 * time.Second
		c.PushPullInterval = time.Millisecond
	})
	m2 := HostMemberlist(addr2, t, func(c *Config) {
		c.GossipInterval = 10 * time.Second
		c.JoinCh = ch
	})

	defer m1.Shutdown()
	defer m2.Shutdown()

	a1 := alive{Node: addr1, Addr: ip1, Incarnation: 1}
	m1.aliveNode(&a1)
	a2 := alive{Node: addr2, Addr: ip2, Incarnation: 1}
	m1.aliveNode(&a2)

	// Gossip should send all this to m2
	m1.pushPull()

	for i := 0; i < 2; i++ {
		select {
		case <-ch:
		case <-time.After(10 * time.Millisecond):
			t.Fatalf("timeout")
		}
	}
}
