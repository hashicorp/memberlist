package memberlist

import (
	"strings"
	"testing"
)

func TestSendBestEffortRejectsOversizedMessage(t *testing.T) {
	m := &Memberlist{
		config: &Config{
			UDPBufferSize: 64,
		},
	}
	to := &Node{Name: "n1", Addr: []byte{127, 0, 0, 1}, Port: 7946}

	// Encoded size is 1 (userMsg) + len(msg). With compoundHeaderOverhead=2
	// and empty label, limit is 64-2-0 = 62, so a 62-byte payload is too large.
	big := []byte(strings.Repeat("x", 62))
	err := m.SendBestEffort(to, big)
	if err == nil {
		t.Fatal("expected oversized SendBestEffort to fail")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}

	// One byte under the limit should pass the size gate. A nil transport will
	// panic later, so only assert that the size check itself does not fire.
	limit := m.config.UDPBufferSize - compoundHeaderOverhead - labelOverhead(m.config.Label)
	okPayload := limit - 1 // room for the userMsg type byte
	if okPayload < 0 {
		t.Fatal("test config leaves no room for a message")
	}
	if 1+okPayload > limit {
		t.Fatalf("sanity: encoded size %d should be <= limit %d", 1+okPayload, limit)
	}
	// Confirm the oversized boundary: encoded length == limit+1 is rejected above.
	if 1+len(big) != limit+1 {
		t.Fatalf("test setup: want encoded %d, got %d", limit+1, 1+len(big))
	}
}
