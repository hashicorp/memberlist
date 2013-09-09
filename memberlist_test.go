package memberlist

import (
	"net"
	"testing"
)

func TestMemberList_CreateShutdown(t *testing.T) {
	c := DefaultConfig()

	var m *Memberlist
	var err error
	for i := 0; i < 100; i++ {
		m, err = Create(c)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
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
