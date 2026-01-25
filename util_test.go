// Copyright IBM Corp. 2013, 2025
// SPDX-License-Identifier: MPL-2.0

package memberlist

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUtil_PortFunctions(t *testing.T) {
	tests := []struct {
		addr       string
		hasPort    bool
		ensurePort string
	}{
		{"1.2.3.4", false, "1.2.3.4:8301"},
		{"1.2.3.4:1234", true, "1.2.3.4:1234"},
		{"2600:1f14:e22:1501:f9a:2e0c:a167:67e8", false, "[2600:1f14:e22:1501:f9a:2e0c:a167:67e8]:8301"},
		{"[2600:1f14:e22:1501:f9a:2e0c:a167:67e8]", false, "[2600:1f14:e22:1501:f9a:2e0c:a167:67e8]:8301"},
		{"[2600:1f14:e22:1501:f9a:2e0c:a167:67e8]:1234", true, "[2600:1f14:e22:1501:f9a:2e0c:a167:67e8]:1234"},
		{"localhost", false, "localhost:8301"},
		{"localhost:1234", true, "localhost:1234"},
		{"hashicorp.com", false, "hashicorp.com:8301"},
		{"hashicorp.com:1234", true, "hashicorp.com:1234"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got, want := hasPort(tt.addr), tt.hasPort; got != want {
				t.Fatalf("got %v want %v", got, want)
			}
			if got, want := ensurePort(tt.addr, 8301), tt.ensurePort; got != want {
				t.Fatalf("got %v want %v", got, want)
			}
		})
	}
}

func TestEncodeDecode(t *testing.T) {
	msg := &ping{SeqNo: 100}
	buf, err := encode(pingMsg, msg, false)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}
	var out ping
	if err := decode(buf.Bytes()[1:], &out); err != nil {
		t.Fatalf("unexpected err: %s", err)
	}
	if msg.SeqNo != out.SeqNo {
		t.Fatalf("bad sequence no")
	}
}

func TestRandomOffset(t *testing.T) {
	vals := make(map[int]struct{})
	for i := 0; i < 100; i++ {
		offset := randomOffset(2 << 30)
		if _, ok := vals[offset]; ok {
			t.Fatalf("got collision")
		}
		vals[offset] = struct{}{}
	}
}

func TestRandomOffset_Zero(t *testing.T) {
	offset := randomOffset(0)
	if offset != 0 {
		t.Fatalf("bad offset")
	}
}

func TestSuspicionTimeout(t *testing.T) {
	timeouts := map[int]time.Duration{
		5:    1000 * time.Millisecond,
		10:   1000 * time.Millisecond,
		50:   1698 * time.Millisecond,
		100:  2000 * time.Millisecond,
		500:  2698 * time.Millisecond,
		1000: 3000 * time.Millisecond,
	}
	for n, expected := range timeouts {
		timeout := suspicionTimeout(3, n, time.Second) / 3
		if timeout != expected {
			t.Fatalf("bad: %v, %v", expected, timeout)
		}
	}
}

func TestRetransmitLimit(t *testing.T) {
	lim := retransmitLimit(3, 0)
	if lim != 0 {
		t.Fatalf("bad val %v", lim)
	}
	lim = retransmitLimit(3, 1)
	if lim != 3 {
		t.Fatalf("bad val %v", lim)
	}
	lim = retransmitLimit(3, 99)
	if lim != 6 {
		t.Fatalf("bad val %v", lim)
	}
}

func TestShuffleNodes(t *testing.T) {
	orig := []*nodeState{
		&nodeState{
			State: StateDead,
		},
		&nodeState{
			State: StateAlive,
		},
		&nodeState{
			State: StateAlive,
		},
		&nodeState{
			State: StateDead,
		},
		&nodeState{
			State: StateAlive,
		},
		&nodeState{
			State: StateAlive,
		},
		&nodeState{
			State: StateDead,
		},
		&nodeState{
			State: StateAlive,
		},
	}
	nodes := make([]*nodeState, len(orig))
	copy(nodes[:], orig[:])

	if !reflect.DeepEqual(nodes, orig) {
		t.Fatalf("should match")
	}

	shuffleNodes(nodes)

	if reflect.DeepEqual(nodes, orig) {
		t.Fatalf("should not match")
	}
}

func TestPushPullScale(t *testing.T) {
	sec := time.Second
	for i := 0; i <= 32; i++ {
		if s := pushPullScale(sec, i); s != sec {
			t.Fatalf("Bad time scale: %v", s)
		}
	}
	for i := 33; i <= 64; i++ {
		if s := pushPullScale(sec, i); s != 2*sec {
			t.Fatalf("Bad time scale: %v", s)
		}
	}
	for i := 65; i <= 128; i++ {
		if s := pushPullScale(sec, i); s != 3*sec {
			t.Fatalf("Bad time scale: %v", s)
		}
	}
}

func TestMoveDeadNodes(t *testing.T) {
	now := time.Now()
	nodeStates := []*nodeState{
		{
			State:       StateDead,
			StateChange: now.Add(-20 * time.Second),
		},
		{
			State:       StateAlive,
			StateChange: now.Add(-20 * time.Second),
		},
		{
			State:       StateDead,
			StateChange: now.Add(-10 * time.Second),
		},
		{
			State:       StateLeft,
			StateChange: now.Add(-10 * time.Second),
		},
		{
			State:       StateLeft,
			StateChange: now.Add(-20 * time.Second),
		},
		{
			State:       StateAlive,
			StateChange: now.Add(-20 * time.Second),
		},
		{
			State:       StateDead,
			StateChange: now.Add(-20 * time.Second),
		},
		{
			State:       StateAlive,
			StateChange: now.Add(-20 * time.Second),
		},
		{
			State:       StateLeft,
			StateChange: now.Add(-20 * time.Second),
		},
		{
			State:       StateLeft,
			StateChange: now.Add(-30 * time.Second),
		},
		{
			State:       StateDead,
			StateChange: now.Add(-30 * time.Second),
		},
	}

	tests := []struct {
		name                string
		deadNodeReclaimTime time.Duration
		gossipToTheDeadTime time.Duration
		expectedIndex       int
	}{
		{
			name:                "Do not reclaim dead nodes when deadNodeReclaimTime is 0",
			deadNodeReclaimTime: 0,
			gossipToTheDeadTime: 15 * time.Second,
			expectedIndex:       8,
		},
		{
			name:                "Reclaim dead nodes when deadNodeReclaimTime is greater than 0",
			deadNodeReclaimTime: 10 * time.Second,
			gossipToTheDeadTime: 15 * time.Second,
			expectedIndex:       5,
		},
		{
			name:                "deadNodeReclaimTime is greater than gossipToTheDeadTime",
			deadNodeReclaimTime: 25 * time.Second,
			gossipToTheDeadTime: 15 * time.Second,
			expectedIndex:       9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			states := make([]*nodeState, len(nodeStates))
			copy(states, nodeStates)
			index := moveDeadNodes(states, tt.deadNodeReclaimTime, tt.gossipToTheDeadTime)
			assert.Equal(t, tt.expectedIndex, index)

			reclaimTime := now.Add(tt.deadNodeReclaimTime)
			if tt.gossipToTheDeadTime > tt.deadNodeReclaimTime {
				reclaimTime = now.Add(tt.gossipToTheDeadTime)
			}

			if tt.deadNodeReclaimTime == 0 {
				for i := 0; i < index; i++ {
					if states[i].State == StateLeft {
						assert.True(t, states[i].StateChange.Before(reclaimTime), "node %d should have been moved", i)
					}
				}
				for i := index; i < len(states); i++ {
					assert.Equal(t, StateLeft, states[i].State)
				}
			} else {
				for i := 0; i < index; i++ {
					if states[i].DeadOrLeft() {
						assert.True(t, states[i].StateChange.Before(reclaimTime), "node %d should have been moved", i)
					}
				}
				for i := index; i < len(states); i++ {
					assert.True(t, states[i].DeadOrLeft())
				}
			}
		})
	}
}

func TestKRandomNodes(t *testing.T) {
	nodes := []*nodeState{}
	for i := 0; i < 90; i++ {
		// Half the nodes are in a bad state
		state := StateAlive
		switch i % 3 {
		case 0:
			state = StateAlive
		case 1:
			state = StateSuspect
		case 2:
			state = StateDead
		}
		nodes = append(nodes, &nodeState{
			Node: Node{
				Name: fmt.Sprintf("test%d", i),
			},
			State: state,
		})
	}

	filterFunc := func(n *nodeState) bool {
		if n.Name == "test0" || n.State != StateAlive {
			return true
		}
		return false
	}

	s1 := kRandomNodes(3, nodes, filterFunc)
	s2 := kRandomNodes(3, nodes, filterFunc)
	s3 := kRandomNodes(3, nodes, filterFunc)

	if reflect.DeepEqual(s1, s2) {
		t.Fatalf("unexpected equal")
	}
	if reflect.DeepEqual(s1, s3) {
		t.Fatalf("unexpected equal")
	}
	if reflect.DeepEqual(s2, s3) {
		t.Fatalf("unexpected equal")
	}

	for _, s := range [][]Node{s1, s2, s3} {
		if len(s) != 3 {
			t.Fatalf("bad len")
		}
		for _, n := range s {
			if n.Name == "test0" {
				t.Fatalf("Bad name")
			}
			if n.State != StateAlive {
				t.Fatalf("Bad state")
			}
		}
	}
}

func TestMakeCompoundMessage(t *testing.T) {
	msg := &ping{SeqNo: 100}
	buf, err := encode(pingMsg, msg, false)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	msgs := [][]byte{buf.Bytes(), buf.Bytes(), buf.Bytes()}
	compound := makeCompoundMessage(msgs)

	if compound.Len() != 3*buf.Len()+3*compoundOverhead+compoundHeaderOverhead {
		t.Fatalf("bad len")
	}
}

func TestDecodeCompoundMessage(t *testing.T) {
	msg := &ping{SeqNo: 100}
	buf, err := encode(pingMsg, msg, false)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	msgs := [][]byte{buf.Bytes(), buf.Bytes(), buf.Bytes()}
	compound := makeCompoundMessage(msgs)

	trunc, parts, err := decodeCompoundMessage(compound.Bytes()[1:])
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}
	if trunc != 0 {
		t.Fatalf("should not truncate")
	}
	if len(parts) != 3 {
		t.Fatalf("bad parts")
	}
	for _, p := range parts {
		if len(p) != buf.Len() {
			t.Fatalf("bad part len")
		}
	}
}

func TestDecodeCompoundMessage_NumberOfPartsOverflow(t *testing.T) {
	buf := []byte{0x80}
	_, _, err := decodeCompoundMessage(buf)
	require.Error(t, err)
	require.Equal(t, err.Error(), "truncated len slice")
}

func TestDecodeCompoundMessage_Trunc(t *testing.T) {
	msg := &ping{SeqNo: 100}
	buf, err := encode(pingMsg, msg, false)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	msgs := [][]byte{buf.Bytes(), buf.Bytes(), buf.Bytes()}
	compound := makeCompoundMessage(msgs)

	trunc, parts, err := decodeCompoundMessage(compound.Bytes()[1:38])
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}
	if trunc != 1 {
		t.Fatalf("truncate: %d", trunc)
	}
	if len(parts) != 2 {
		t.Fatalf("bad parts")
	}
	for _, p := range parts {
		if len(p) != buf.Len() {
			t.Fatalf("bad part len")
		}
	}
}

func TestCompressDecompressPayload(t *testing.T) {
	buf, err := compressPayload([]byte("testing"), false)
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	decomp, err := decompressPayload(buf.Bytes()[1:])
	if err != nil {
		t.Fatalf("unexpected err: %s", err)
	}

	if !reflect.DeepEqual(decomp, []byte("testing")) {
		t.Fatalf("bad payload: %v", decomp)
	}
}
