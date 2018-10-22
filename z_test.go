package memberlist

import (
	"log"
	"testing"
)

func testLogger(t testing.TB) *log.Logger {
	return log.New(testWriter{t}, "test: ", log.LstdFlags)
}

type testWriter struct {
	t testing.TB
}

func (tw testWriter) Write(p []byte) (n int, err error) {
	tw.t.Helper()
	tw.t.Log(string(p))
	return len(p), nil
}
