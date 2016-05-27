package memberlist

import (
	"testing"
	"time"
)

func TestSuspicion(t *testing.T) {
	const k = 3
	const min = 500 * time.Millisecond
	const max = 2 * time.Second

	cases := []struct {
		numConfirmations int32
		confirmations    []string
		expected         time.Duration
	}{
		{0, []string{}, max},
		{1, []string{"foo"}, 1250 * time.Millisecond},
		{1, []string{"foo", "foo", "foo"}, 1250 * time.Millisecond},
		{2, []string{"foo", "bar"}, 810 * time.Millisecond},
		{3, []string{"foo", "bar", "baz"}, min},
		{4, []string{"foo", "bar", "baz", "zoo"}, min},
	}
	for i, c := range cases {
		ch := make(chan time.Duration, 1)
		start := time.Now()
		f := func(numConfirmations int32) {
			if numConfirmations != c.numConfirmations {
				t.Errorf("case %d: bad %d != %d", i, numConfirmations, c.numConfirmations)
			}

			ch <- time.Now().Sub(start)
		}

		s := newSuspicion(k, min, max, f)
		for _, peer := range c.confirmations {
			s.Corroborate(peer)
		}

		// Wait until right before the timeout and make sure the
		// timer hasn't fired.
		fudge := 25 * time.Millisecond
		time.Sleep(c.expected - fudge)
		select {
		case d := <-ch:
			t.Fatalf("case %d: should not have fired (%9.6f)", i, d.Seconds())
		default:
		}

		// Wait through the timeout and a little after and make sure it
		// fires.
		time.Sleep(2 * fudge)
		select {
		case <-ch:
		default:
			t.Fatalf("case %d: should have fired", i)
		}

		// Corroborate after to make sure it handles a negative remaining
		// time correctly and doesn't fire again.
		s.Corroborate("late")
		time.Sleep(c.expected + 2*fudge)
		select {
		case d := <-ch:
			t.Fatalf("case %d: should not have fired (%9.6f)", i, d.Seconds())
		default:
		}
	}
}
