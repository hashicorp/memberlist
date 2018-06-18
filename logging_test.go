package memberlist

import (
	"fmt"
	"log"
	"net"
	"strings"
	"testing"

	hclog "github.com/hashicorp/go-hclog"
)

type recorder struct {
	entry *string
	args  []interface{}
}

func (r *recorder) add(msgType, msg string, args ...interface{}) {
	argsStr := ""
	for i := 0; i < len(args); i = i + 2 {
		argsStr = argsStr + fmt.Sprintf("%v=%v", args[i], args[i+1])
	}
	for i := 0; i < len(r.args); i = i + 2 {
		argsStr = argsStr + fmt.Sprintf("%v=%v", r.args[i], r.args[i+1])
	}
	entry := fmt.Sprintf("%s: %s %s", msgType, msg, argsStr)
	*r.entry = entry
}

func (r *recorder) Trace(msg string, args ...interface{}) { r.add("trace", msg, args...) }
func (r *recorder) Debug(msg string, args ...interface{}) { r.add("debug", msg, args...) }
func (r *recorder) Info(msg string, args ...interface{})  { r.add("info", msg, args...) }
func (r *recorder) Warn(msg string, args ...interface{})  { r.add("warn", msg, args...) }
func (r *recorder) Error(msg string, args ...interface{}) { r.add("error", msg, args...) }

func (r *recorder) IsTrace() bool { panic("not implemented") }
func (r *recorder) IsDebug() bool { panic("not implemented") }
func (r *recorder) IsInfo() bool  { panic("not implemented") }
func (r *recorder) IsWarn() bool  { panic("not implemented") }
func (r *recorder) IsError() bool { panic("not implemented") }

func (r *recorder) With(args ...interface{}) hclog.Logger {
	return &recorder{
		entry: r.entry,
		args:  append(r.args, args...),
	}
}

func (r *recorder) Named(name string) hclog.Logger      { panic("not implemented") }
func (r *recorder) ResetNamed(name string) hclog.Logger { panic("not implemented") }
func (r *recorder) StandardLogger(opts *hclog.StandardLoggerOptions) *log.Logger {
	panic("not implemented")
}

func TestLogging_Address(t *testing.T) {
	r := &recorder{entry: new(string)}
	l := logger{Logger: r}

	l.fromAddress(nil).Info("Hello world")
	if !strings.Contains(*r.entry, "from=<unknown address>") {
		t.Fatalf("log entry does not contain from=<unknown address>: %s", *r.entry)
	}

	addr, err := net.ResolveIPAddr("ip4", "127.0.0.1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	l.fromAddress(addr).Info("Hello world")
	if !strings.Contains(*r.entry, "from=127.0.0.1") {
		t.Fatalf("log entry does not contain the origin IP: %s", *r.entry)
	}
}

func TestLogging_Conn(t *testing.T) {
	r := &recorder{entry: new(string)}
	l := logger{Logger: r}

	l.fromConn(nil).Info("Hello world")
	if !strings.Contains(*r.entry, "from=<unknown address>") {
		t.Fatalf("log entry does not contain from=<unknown address>: %s", *r.entry)
	}

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer conn.Close()

	l.fromConn(conn).Info("Hello world")
	if !strings.Contains(*r.entry, fmt.Sprintf("from=%s", conn.RemoteAddr().String())) {
		t.Fatalf("log entry does not contain the origin IP: %s", *r.entry)
	}
}
