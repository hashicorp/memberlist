package memberlist

import (
	"net"
	"reflect"
	"testing"
)

func GetMemberlist(t *testing.T) *Memberlist {
	c := DefaultConfig()

	var m *Memberlist
	var err error
	for i := 0; i < 100; i++ {
		m, err = Create(c)
		if err == nil {
			return m
		}
		c.TCPPort++
		c.UDPPort++
	}
	t.Fatalf("failed to start: %v", err)
	return nil
}

func TestMemberList_CreateShutdown(t *testing.T) {
	m := GetMemberlist(t)
	if err := m.Shutdown(); err != nil {
		t.Fatalf("failed to shutdown %v", err)
	}
}

func TestMemberList_NotifyJoin(t *testing.T) {
	ch := make(chan net.Addr)
	m := &Memberlist{}
	m.NotifyJoin(ch)
	if len(m.notifyJoin) != 1 || m.notifyJoin[0] != ch {
		t.Fatalf("did not add")
	}

	// Should do nothing
	m.NotifyJoin(ch)
	if len(m.notifyJoin) != 1 || m.notifyJoin[0] != ch {
		t.Fatalf("did not add")
	}
}

func TestMemberList_NotifyLeave(t *testing.T) {
	ch := make(chan net.Addr)
	m := &Memberlist{}
	m.NotifyLeave(ch)
	if len(m.notifyLeave) != 1 || m.notifyLeave[0] != ch {
		t.Fatalf("did not add")
	}

	// Should do nothing
	m.NotifyLeave(ch)
	if len(m.notifyLeave) != 1 || m.notifyLeave[0] != ch {
		t.Fatalf("did not add")
	}
}

func TestMemberList_NotifyFail(t *testing.T) {
	ch := make(chan net.Addr)
	m := &Memberlist{}
	m.NotifyFail(ch)
	if len(m.notifyFail) != 1 || m.notifyFail[0] != ch {
		t.Fatalf("did not add")
	}

	// Should do nothing
	m.NotifyFail(ch)
	if len(m.notifyFail) != 1 || m.notifyFail[0] != ch {
		t.Fatalf("did not add")
	}
}

func TestMemberList_Stop(t *testing.T) {
	ch := make(chan net.Addr)
	m := &Memberlist{}
	m.NotifyJoin(ch)
	m.NotifyLeave(ch)
	m.NotifyFail(ch)
	m.Stop(ch)

	if len(m.notifyJoin) != 0 {
		t.Fatalf("did not remove")
	}
	if len(m.notifyLeave) != 0 {
		t.Fatalf("did not remove")
	}
	if len(m.notifyFail) != 0 {
		t.Fatalf("did not remove")
	}
}

func TestMemberList_Members(t *testing.T) {
	n1 := &Node{Name: "test"}
	n2 := &Node{Name: "test2"}
	n3 := &Node{Name: "test3"}

	m := &Memberlist{}
	nodes := []*NodeState{
		&NodeState{Node: *n1, State: StateAlive},
		&NodeState{Node: *n2, State: StateDead},
		&NodeState{Node: *n3, State: StateSuspect},
	}
	m.nodes = nodes

	members := m.Members()
	if !reflect.DeepEqual(members, []*Node{n1, n3}) {
		t.Fatalf("bad members")
	}
}
