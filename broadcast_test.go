package memberlist

import (
	"testing"
)

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
