package memberlist

import (
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
