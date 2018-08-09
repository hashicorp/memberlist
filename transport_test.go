package memberlist

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTransport_Join(t *testing.T) {
	net := &MockNetwork{}

	t1 := net.NewTransport()

	c1 := DefaultLANConfig()
	c1.Name = "node1"
	c1.Transport = t1
	m1, err := Create(c1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	c2 := DefaultLANConfig()
	c2.Name = "node2"
	c2.Transport = net.NewTransport()
	m2, err := Create(c2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	m2.setAlive()
	m2.schedule()
	defer m2.Shutdown()

	num, err := m2.Join([]string{t1.addr.String()})
	if num != 1 {
		t.Fatalf("bad: %d", num)
	}
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if len(m2.Members()) != 2 {
		t.Fatalf("bad: %v", m2.Members())
	}
	if m2.estNumNodes() != 2 {
		t.Fatalf("bad: %v", m2.Members())
	}

}

func TestTransport_Send(t *testing.T) {
	net := &MockNetwork{}

	t1 := net.NewTransport()
	d1 := &MockDelegate{}

	c1 := DefaultLANConfig()
	c1.Name = "node1"
	c1.Transport = t1
	c1.Delegate = d1
	m1, err := Create(c1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	m1.setAlive()
	m1.schedule()
	defer m1.Shutdown()

	c2 := DefaultLANConfig()
	c2.Name = "node2"
	c2.Transport = net.NewTransport()
	m2, err := Create(c2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	m2.setAlive()
	m2.schedule()
	defer m2.Shutdown()

	num, err := m2.Join([]string{t1.addr.String()})
	if num != 1 {
		t.Fatalf("bad: %d", num)
	}
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if err := m2.SendTo(t1.addr, []byte("SendTo")); err != nil {
		t.Fatalf("err: %v", err)
	}

	var n1 *Node
	for _, n := range m2.Members() {
		if n.Name == c1.Name {
			n1 = n
			break
		}
	}
	if n1 == nil {
		t.Fatalf("bad")
	}

	if err := m2.SendToUDP(n1, []byte("SendToUDP")); err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := m2.SendToTCP(n1, []byte("SendToTCP")); err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := m2.SendBestEffort(n1, []byte("SendBestEffort")); err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := m2.SendReliable(n1, []byte("SendReliable")); err != nil {
		t.Fatalf("err: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	expected := []string{"SendTo", "SendToUDP", "SendToTCP", "SendBestEffort", "SendReliable"}

	received := make([]string, len(d1.msgs))
	for i, bs := range d1.msgs {
		received[i] = string(bs)
	}
	// Some of these are UDP so often get re-ordered making the test flaky if we
	// assert send ordering. Sort both slices to be tolerant of re-ordering.
	require.ElementsMatch(t, expected, received)
}
