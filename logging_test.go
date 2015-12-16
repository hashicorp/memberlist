package memberlist

import (
	"net"
	"testing"
	"time"
)

type mockAddr struct{ addr string }

func (m *mockAddr) Network() string { return "don't care" }
func (m *mockAddr) String() string  { return m.addr }

type mockConn struct{ addr *mockAddr }

func (m *mockConn) Read(b []byte) (n int, err error)   { return 0, nil }
func (m *mockConn) Write(b []byte) (n int, err error)  { return 0, nil }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return m.addr }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestLogging_Address(t *testing.T) {
	s := LogAddress(nil)
	if s != "from=<unknown address>" {
		t.Fatalf("bad: %s", s)
	}

	s = LogAddress(&mockAddr{"hello"})
	if s != "from=hello" {
		t.Fatalf("bad: %s", s)
	}
}

func TestLogging_Conn(t *testing.T) {
	s := LogConn(nil)
	if s != "from=<unknown address>" {
		t.Fatalf("bad: %s", s)
	}

	s = LogConn(&mockConn{&mockAddr{"hello"}})
	if s != "from=hello" {
		t.Fatalf("bad: %s", s)
	}
}
