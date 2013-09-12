package memberlist

import (
	"bytes"
	"testing"
)

func TestMemberlist_Queue(t *testing.T) {
	m := &Memberlist{}
	m.queueBroadcast("test", nil)
	m.queueBroadcast("foo", nil)
	m.queueBroadcast("bar", nil)

	if len(m.bcQueue) != 3 {
		t.Fatalf("bad len")
	}
	if m.bcQueue[0].node != "test" {
		t.Fatalf("missing test")
	}
	if m.bcQueue[1].node != "foo" {
		t.Fatalf("missing foo")
	}
	if m.bcQueue[2].node != "bar" {
		t.Fatalf("missing bar")
	}

	// Should invalidate previous message
	m.queueBroadcast("test", nil)

	if len(m.bcQueue) != 3 {
		t.Fatalf("bad len")
	}
	if m.bcQueue[0].node != "foo" {
		t.Fatalf("missing foo")
	}
	if m.bcQueue[1].node != "bar" {
		t.Fatalf("missing bar")
	}
	if m.bcQueue[2].node != "test" {
		t.Fatalf("missing test")
	}
}

func TestMemberlist_GetBroadcasts(t *testing.T) {
	c := DefaultConfig()
	m := &Memberlist{config: c}
	m.nodes = make([]*NodeState, 10) // fake some nodes...

	// 18 bytes per message
	m.queueBroadcast("test", bytes.NewBuffer([]byte("1. this is a test.")))
	m.queueBroadcast("foo", bytes.NewBuffer([]byte("2. this is a test.")))
	m.queueBroadcast("bar", bytes.NewBuffer([]byte("3. this is a test.")))
	m.queueBroadcast("baz", bytes.NewBuffer([]byte("4. this is a test.")))

	// 2 byte overhead per message, should get all 4 messages
	all := m.getBroadcasts(2, 80)
	if len(all) != 4 {
		t.Fatalf("missing messages: %v", all)
	}

	// 3 byte overhead, should only get 3 messages back
	partial := m.getBroadcasts(3, 80)
	if len(partial) != 3 {
		t.Fatalf("missing messages: %v", partial)
	}
}

func TestMemberlist_GetBroadcasts_Limit(t *testing.T) {
	c := DefaultConfig()
	c.RetransmitMult = 1
	m := &Memberlist{config: c}
	m.nodes = make([]*NodeState, 10) // fake some nodes...

	// 18 bytes per message
	m.queueBroadcast("test", bytes.NewBuffer([]byte("1. this is a test.")))
	m.queueBroadcast("foo", bytes.NewBuffer([]byte("2. this is a test.")))
	m.queueBroadcast("bar", bytes.NewBuffer([]byte("3. this is a test.")))
	m.queueBroadcast("baz", bytes.NewBuffer([]byte("4. this is a test.")))

	// 3 byte overhead, should only get 3 messages back
	partial1 := m.getBroadcasts(3, 80)
	if len(partial1) != 3 {
		t.Fatalf("missing messages: %v", partial1)
	}

	partial2 := m.getBroadcasts(3, 80)
	if len(partial2) != 3 {
		t.Fatalf("missing messages: %v", partial2)
	}

	// Only two not expired
	partial3 := m.getBroadcasts(3, 80)
	if len(partial3) != 2 {
		t.Fatalf("missing messages: %v", partial3)
	}

	// Should get nothing
	partial5 := m.getBroadcasts(3, 80)
	if len(partial5) != 0 {
		t.Fatalf("missing messages: %v", partial5)
	}
}

func TestBroadcastSort(t *testing.T) {
	bc := broadcasts([]*broadcast{
		&broadcast{
			transmits: 0,
		},
		&broadcast{
			transmits: 10,
		},
		&broadcast{
			transmits: 3,
		},
		&broadcast{
			transmits: 4,
		},
		&broadcast{
			transmits: 7,
		},
	})
	bc.Sort()

	if bc[0].transmits != 10 {
		t.Fatalf("bad val %v", bc[0])
	}
	if bc[1].transmits != 7 {
		t.Fatalf("bad val %v", bc[7])
	}
	if bc[2].transmits != 4 {
		t.Fatalf("bad val %v", bc[2])
	}
	if bc[3].transmits != 3 {
		t.Fatalf("bad val %v", bc[3])
	}
	if bc[4].transmits != 0 {
		t.Fatalf("bad val %v", bc[4])
	}
}
