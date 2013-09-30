package memberlist

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

var bindLock sync.Mutex
var (
	bindNum = 10
)

func GetMemberlist(t *testing.T) *Memberlist {
	c := DefaultConfig()
	c.BindAddr = "127.0.0.1"

	var m *Memberlist
	var err error
	for i := 0; i < 100; i++ {
		m, err = newMemberlist(c)
		if err == nil {
			return m
		}
		c.TCPPort++
		c.UDPPort++
	}
	t.Fatalf("failed to start: %v", err)
	return nil
}

func GetBindAddr() (string, []byte) {
	bindLock.Lock()
	defer bindLock.Unlock()
	addr := bindNum
	bindNum++
	s := fmt.Sprintf("127.0.0.%d", addr)
	b := []byte{127, 0, 0, byte(addr)}
	return s, b
}

func TestMemberList_CreateShutdown(t *testing.T) {
	m := GetMemberlist(t)
	m.schedule()
	if err := m.Shutdown(); err != nil {
		t.Fatalf("failed to shutdown %v", err)
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

func TestMemberlist_Join(t *testing.T) {
	m1 := GetMemberlist(t)
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	// Create a second node
	c := DefaultConfig()
	addr1, _ := GetBindAddr()
	c.Name = addr1
	c.BindAddr = addr1
	c.UDPPort = m1.config.UDPPort
	c.TCPPort = m1.config.TCPPort
	m2, err := Join(c, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
}

func TestMemberlist_Leave(t *testing.T) {
	m1 := GetMemberlist(t)
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	// Create a second node
	c := DefaultConfig()
	addr1, _ := GetBindAddr()
	c.Name = addr1
	c.BindAddr = addr1
	c.UDPPort = m1.config.UDPPort
	c.TCPPort = m1.config.TCPPort
	c.GossipInterval = time.Millisecond
	m2, err := Join(c, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}
	if len(m1.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	ch := make(chan *Node)
	m1.config.LeaveCh = ch

	// Leave
	m2.Leave()

	// Wait for leave
	select {
	case <-ch:
	case <-time.After(10 * time.Millisecond):
		t.Fatalf("timeout on leave")
	}

	// m1 should think dead
	if len(m1.Members()) != 1 {
		t.Fatalf("should have 1 node")
	}
	if len(m2.Members()) != 1 {
		t.Fatalf("should have 1 node")
	}
}

func TestMemberlist_JoinShutdown(t *testing.T) {
	m1 := GetMemberlist(t)
	m1.setAlive()
	m1.schedule()

	// Create a second node
	c := DefaultConfig()
	addr1, _ := GetBindAddr()
	c.Name = addr1
	c.BindAddr = addr1
	c.UDPPort = m1.config.UDPPort
	c.TCPPort = m1.config.TCPPort
	c.ProbeInterval = time.Millisecond
	c.RTT = 100 * time.Microsecond
	m2, err := Join(c, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal("unexpected err: %s", err)
	}

	// Check the hosts
	if len(m2.Members()) != 2 {
		t.Fatalf("should have 2 nodes! %v", m2.Members())
	}

	ch := make(chan *Node)
	m2.config.LeaveCh = ch

	m1.Shutdown()

	// Wait for leave
	select {
	case <-ch:
	case <-time.After(10 * time.Millisecond):
		t.Fatalf("timeout on leave")
	}

	if len(m2.Members()) != 1 {
		t.Fatalf("should have 1 nodes! %v", m2.Members())
	}
}
