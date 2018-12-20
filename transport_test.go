package memberlist

import (
	"log"
	"net"
	"sync/atomic"
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
	c1.Logger = testLogger(t)
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
	c2.Logger = testLogger(t)
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
	c1.Logger = testLogger(t)
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
	c2.Logger = testLogger(t)
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

	msgs1 := d1.getMessages()

	received := make([]string, len(msgs1))
	for i, bs := range msgs1 {
		received[i] = string(bs)
	}
	// Some of these are UDP so often get re-ordered making the test flaky if we
	// assert send ordering. Sort both slices to be tolerant of re-ordering.
	require.ElementsMatch(t, expected, received)
}

type testCountingWriter struct {
	t *testing.T
	numCalls *int32
}

func (tw testCountingWriter) Write(p []byte) (n int, err error) {
	atomic.AddInt32(tw.numCalls, 1)
	tw.t.Log("countingWriter", string(p))
	return len(p), nil
}

func TestTransport_TcpListenBackoff(t *testing.T) {

	listener, _ := net.ListenTCP("tcp", nil)
	listener.Close()

	var numCalls int32
	countingWriter := testCountingWriter{t, &numCalls}
	countingLogger := log.New(countingWriter, "test", log.LstdFlags)
	transport := NetTransport{
		streamCh: make(chan net.Conn),
		logger: countingLogger,
	}
	transport.wg.Add(1)

	go transport.tcpListen(listener)

	time.Sleep(5 * time.Second)
	atomic.StoreInt32(&transport.shutdown, 1)

	c := make(chan struct{})
	go func() {
		defer close(c)
		transport.wg.Wait()
	}()
	select {
	case <- c:
	case <-time.After(1 * time.Second):
		t.Error("timed out waiting for transport waitgroup to be done after setting shutdown == 1")
	}

	t.Log(countingWriter.numCalls)
	require.Equal(t, len(transport.streamCh), 0)
	require.True(t, numCalls > 10)
	require.True(t, numCalls < 15)
}

