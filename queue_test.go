// Copyright IBM Corp. 2013, 2026
// SPDX-License-Identifier: MPL-2.0

package memberlist

import (
	"testing"

	"github.com/google/btree"
	"github.com/stretchr/testify/require"
)

// for testing only
func (q *TransmitLimitedQueue) orderedView() []*limitedBroadcast {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]*limitedBroadcast, 0, q.lenLocked())
	q.walkReadOnlyLocked(true, func(cur *limitedBroadcast) bool {
		out = append(out, cur)
		return true
	})

	return out
}

func TestLimitedBroadcastLess(t *testing.T) {
	cases := []struct {
		Name string
		A    *limitedBroadcast // lesser
		B    *limitedBroadcast
	}{
		{
			"diff-transmits",
			&limitedBroadcast{transmits: 0, msgLen: 10, id: 100},
			&limitedBroadcast{transmits: 1, msgLen: 10, id: 100},
		},
		{
			"same-transmits--diff-len",
			&limitedBroadcast{transmits: 0, msgLen: 12, id: 100},
			&limitedBroadcast{transmits: 0, msgLen: 10, id: 100},
		},
		{
			"same-transmits--same-len--diff-id",
			&limitedBroadcast{transmits: 0, msgLen: 12, id: 100},
			&limitedBroadcast{transmits: 0, msgLen: 12, id: 90},
		},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			a, b := c.A, c.B

			require.True(t, a.Less(b))

			tree := btree.New(32)

			tree.ReplaceOrInsert(b)
			tree.ReplaceOrInsert(a)

			min := tree.Min().(*limitedBroadcast)
			require.Equal(t, a.transmits, min.transmits)
			require.Equal(t, a.msgLen, min.msgLen)
			require.Equal(t, a.id, min.id)

			max := tree.Max().(*limitedBroadcast)
			require.Equal(t, b.transmits, max.transmits)
			require.Equal(t, b.msgLen, max.msgLen)
			require.Equal(t, b.id, max.id)
		})
	}
}

func TestTransmitLimited_Queue(t *testing.T) {
	q := &TransmitLimitedQueue{RetransmitMult: 1, NumNodes: func() int { return 1 }}
	q.QueueBroadcast(&memberlistBroadcast{"test", nil, nil})
	q.QueueBroadcast(&memberlistBroadcast{"foo", nil, nil})
	q.QueueBroadcast(&memberlistBroadcast{"bar", nil, nil})

	if q.NumQueued() != 3 {
		t.Fatalf("bad len")
	}
	dump := q.orderedView()
	if dump[0].b.(*memberlistBroadcast).node != "test" {
		t.Fatalf("missing test")
	}
	if dump[1].b.(*memberlistBroadcast).node != "foo" {
		t.Fatalf("missing foo")
	}
	if dump[2].b.(*memberlistBroadcast).node != "bar" {
		t.Fatalf("missing bar")
	}

	// Should invalidate previous message
	q.QueueBroadcast(&memberlistBroadcast{"test", nil, nil})

	if q.NumQueued() != 3 {
		t.Fatalf("bad len")
	}
	dump = q.orderedView()
	if dump[0].b.(*memberlistBroadcast).node != "foo" {
		t.Fatalf("missing foo")
	}
	if dump[1].b.(*memberlistBroadcast).node != "bar" {
		t.Fatalf("missing bar")
	}
	if dump[2].b.(*memberlistBroadcast).node != "test" {
		t.Fatalf("missing test")
	}
}

func TestTransmitLimited_GetBroadcasts(t *testing.T) {
	q := &TransmitLimitedQueue{RetransmitMult: 3, NumNodes: func() int { return 10 }}

	// 18 bytes per message
	q.QueueBroadcast(&memberlistBroadcast{"test", []byte("1. this is a test."), nil})
	q.QueueBroadcast(&memberlistBroadcast{"foo", []byte("2. this is a test."), nil})
	q.QueueBroadcast(&memberlistBroadcast{"bar", []byte("3. this is a test."), nil})
	q.QueueBroadcast(&memberlistBroadcast{"baz", []byte("4. this is a test."), nil})

	// 2 byte overhead per message, should get all 4 messages
	all := q.GetBroadcasts(2, 80)
	require.Equal(t, 4, len(all), "missing messages: %v", prettyPrintMessages(all))

	// 3 byte overhead, should only get 3 messages back
	partial := q.GetBroadcasts(3, 80)
	require.Equal(t, 3, len(partial), "missing messages: %v", prettyPrintMessages(partial))
}

func TestTransmitLimited_GetBroadcasts_Limit(t *testing.T) {
	q := &TransmitLimitedQueue{RetransmitMult: 1, NumNodes: func() int { return 10 }}

	require.Equal(t, int64(0), q.idGen, "the id generator seed starts at zero")
	require.Equal(t, 2, retransmitLimit(q.RetransmitMult, q.NumNodes()), "sanity check transmit limits")

	// 18 bytes per message
	q.QueueBroadcast(&memberlistBroadcast{"test", []byte("1. this is a test."), nil})
	q.QueueBroadcast(&memberlistBroadcast{"foo", []byte("2. this is a test."), nil})
	q.QueueBroadcast(&memberlistBroadcast{"bar", []byte("3. this is a test."), nil})
	q.QueueBroadcast(&memberlistBroadcast{"baz", []byte("4. this is a test."), nil})

	require.Equal(t, int64(4), q.idGen, "we handed out 4 IDs")

	// 3 byte overhead, should only get 3 messages back
	partial1 := q.GetBroadcasts(3, 80)
	require.Equal(t, 3, len(partial1), "missing messages: %v", prettyPrintMessages(partial1))

	require.Equal(t, int64(4), q.idGen, "id generator doesn't reset until empty")

	partial2 := q.GetBroadcasts(3, 80)
	require.Equal(t, 3, len(partial2), "missing messages: %v", prettyPrintMessages(partial2))

	require.Equal(t, int64(4), q.idGen, "id generator doesn't reset until empty")

	// Only two not expired
	partial3 := q.GetBroadcasts(3, 80)
	require.Equal(t, 2, len(partial3), "missing messages: %v", prettyPrintMessages(partial3))

	require.Equal(t, int64(4), q.idGen, "id generator doesn't reset on empty")

	// Should get nothing
	partial5 := q.GetBroadcasts(3, 80)
	require.Equal(t, 0, len(partial5), "missing messages: %v", prettyPrintMessages(partial5))

	require.Equal(t, int64(4), q.idGen, "id generator doesn't reset on empty")
}

func prettyPrintMessages(msgs [][]byte) []string {
	var out []string
	for _, msg := range msgs {
		out = append(out, "'"+string(msg)+"'")
	}
	return out
}

func TestTransmitLimited_Prune(t *testing.T) {
	q := &TransmitLimitedQueue{RetransmitMult: 1, NumNodes: func() int { return 10 }}

	ch1 := make(chan struct{}, 1)
	ch2 := make(chan struct{}, 1)

	// 18 bytes per message
	q.QueueBroadcast(&memberlistBroadcast{"test", []byte("1. this is a test."), ch1})
	q.QueueBroadcast(&memberlistBroadcast{"foo", []byte("2. this is a test."), ch2})
	q.QueueBroadcast(&memberlistBroadcast{"bar", []byte("3. this is a test."), nil})
	q.QueueBroadcast(&memberlistBroadcast{"baz", []byte("4. this is a test."), nil})

	// Keep only 2
	q.Prune(2)

	require.Equal(t, 2, q.NumQueued())

	// Should notify the first two
	select {
	case <-ch1:
	default:
		t.Fatalf("expected invalidation")
	}
	select {
	case <-ch2:
	default:
		t.Fatalf("expected invalidation")
	}

	dump := q.orderedView()

	if dump[0].b.(*memberlistBroadcast).node != "bar" {
		t.Fatalf("missing bar")
	}
	if dump[1].b.(*memberlistBroadcast).node != "baz" {
		t.Fatalf("missing baz")
	}
}

func TestTransmitLimited_ordering(t *testing.T) {
	q := &TransmitLimitedQueue{RetransmitMult: 1, NumNodes: func() int { return 10 }}

	insert := func(name string, transmits int) {
		q.queueBroadcast(&memberlistBroadcast{name, []byte(name), make(chan struct{})}, transmits)
	}

	insert("node0", 0)
	insert("node1", 10)
	insert("node2", 3)
	insert("node3", 4)
	insert("node4", 7)

	dump := q.orderedView()

	if dump[0].transmits != 10 {
		t.Fatalf("bad val %v, %d", dump[0].b.(*memberlistBroadcast).node, dump[0].transmits)
	}
	if dump[1].transmits != 7 {
		t.Fatalf("bad val %v, %d", dump[7].b.(*memberlistBroadcast).node, dump[7].transmits)
	}
	if dump[2].transmits != 4 {
		t.Fatalf("bad val %v, %d", dump[2].b.(*memberlistBroadcast).node, dump[2].transmits)
	}
	if dump[3].transmits != 3 {
		t.Fatalf("bad val %v, %d", dump[3].b.(*memberlistBroadcast).node, dump[3].transmits)
	}
	if dump[4].transmits != 0 {
		t.Fatalf("bad val %v, %d", dump[4].b.(*memberlistBroadcast).node, dump[4].transmits)
	}
}

type namedTestBroadcast struct {
	name string
	msg  []byte
}

func (b *namedTestBroadcast) Name() string                 { return b.name }
func (b *namedTestBroadcast) Message() []byte              { return b.msg }
func (b *namedTestBroadcast) Finished()                    {}
func (b *namedTestBroadcast) Invalidates(o Broadcast) bool { return false }

func namedMsg(name string, fill byte) *namedTestBroadcast {
	msg := make([]byte, 115)
	for i := range msg {
		msg[i] = fill
	}
	return &namedTestBroadcast{name: name, msg: msg}
}

// A named broadcast that replaces an earlier one empties the queue, which
// resets the id generator; the replacement must not reuse the id of an item
// queued after it.
func TestTransmitLimitedQueue_NamedReplaceThenRelaysKeepsAll(t *testing.T) {
	q := &TransmitLimitedQueue{
		NumNodes:       func() int { return 3 },
		RetransmitMult: 4,
	}

	q.QueueBroadcast(namedMsg("node-3", 'a'))
	q.QueueBroadcast(namedMsg("node-3", 'b'))
	q.QueueBroadcast(namedMsg("node-1", 'c'))
	q.QueueBroadcast(namedMsg("node-2", 'd'))

	require.Equal(t, 3, q.NumQueued())

	var found bool
	for _, m := range q.GetBroadcasts(2, 1398) {
		if len(m) > 0 && m[0] == 'b' {
			found = true
		}
	}
	require.True(t, found, "second node-3 message missing from GetBroadcasts")
}

// Sending the only queued item deletes it and re-adds it one tier down. If
// that delete reset the id generator, items queued afterwards would reuse its
// id and, once they reach its tier, overwrite it in the tree.
func TestTransmitLimitedQueue_ReinsertAfterIdleKeepsAll(t *testing.T) {
	q := &TransmitLimitedQueue{
		NumNodes:       func() int { return 3 },
		RetransmitMult: 4,
	}

	// The limit fits exactly one 115-byte message per call.
	const overhead, limit = 2, 2 + 115

	q.QueueBroadcast(namedMsg("node-a", 'a'))
	require.Equal(t, 1, q.NumQueued())

	// A is sent once and re-added; the delete empties the queue.
	got := q.GetBroadcasts(overhead, limit)
	require.Len(t, got, 1)
	require.Equal(t, byte('a'), got[0][0])
	require.Equal(t, 1, q.NumQueued())

	q.QueueBroadcast(namedMsg("node-b", 'b'))
	q.QueueBroadcast(namedMsg("node-c", 'c'))
	require.Equal(t, 3, q.NumQueued())

	// B and C each reach A's transmit count; then drain A's tier too.
	seen := map[byte]bool{}
	for i := 0; i < 5; i++ {
		for _, m := range q.GetBroadcasts(overhead, limit) {
			seen[m[0]] = true
		}
		require.Equal(t, 3, q.NumQueued(), "call %d", i)
	}
	require.True(t, seen['a'], "A's message was not returned again")
	require.True(t, seen['b'] && seen['c'])
}
