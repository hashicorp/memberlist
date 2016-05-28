package memberlist

import (
	"testing"
	"time"
)

func TestSuspicion(t *testing.T) {
	const k = 3
	const min = 500 * time.Millisecond
	const max = 2 * time.Second

	type pair struct {
		from    string
		newInfo bool
	}
	cases := []struct {
		numConfirmations int
		from             string
		confirmations    []pair
		expected         time.Duration
	}{
		{
			0,
			"me",
			[]pair{},
			max,
		},
		{
			1,
			"me",
			[]pair{
				pair{"me", false},
				pair{"foo", true},
			},
			1250 * time.Millisecond,
		},
		{
			1,
			"me",
			[]pair{
				pair{"me", false},
				pair{"foo", true},
				pair{"foo", false},
				pair{"foo", false},
			},
			1250 * time.Millisecond,
		},
		{
			2,
			"me",
			[]pair{
				pair{"me", false},
				pair{"foo", true},
				pair{"bar", true},
			},
			810 * time.Millisecond,
		},
		{
			3,
			"me",
			[]pair{
				pair{"me", false},
				pair{"foo", true},
				pair{"bar", true},
				pair{"baz", true},
			},
			min,
		},
		{
			3,
			"me",
			[]pair{
				pair{"me", false},
				pair{"foo", true},
				pair{"bar", true},
				pair{"baz", true},
				pair{"zoo", false},
			},
			min,
		},
	}
	for i, c := range cases {
		ch := make(chan time.Duration, 1)
		start := time.Now()
		f := func(numConfirmations int) {
			if numConfirmations != c.numConfirmations {
				t.Errorf("case %d: bad %d != %d", i, numConfirmations, c.numConfirmations)
			}

			ch <- time.Now().Sub(start)
		}

		// Create the timer and add the requested confirmations. Wait
		// the fudge amount to help make sure we calculate the timeout
		// overall, and don't accumulate extra time.
		s := newSuspicion(c.from, k, min, max, f)
		fudge := 25 * time.Millisecond
		for _, p := range c.confirmations {
			time.Sleep(fudge)
			if s.Confirm(p.from) != p.newInfo {
				t.Fatalf("case %d: newInfo mismatch for %s", i, p.from)
			}
		}

		// Wait until right before the timeout and make sure the
		// timer hasn't fired.
		already := time.Duration(len(c.confirmations)) * fudge
		time.Sleep(c.expected - already - fudge)
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

		// Confirm after to make sure it handles a negative remaining
		// time correctly and doesn't fire again.
		s.Confirm("late")
		time.Sleep(c.expected + 2*fudge)
		select {
		case d := <-ch:
			t.Fatalf("case %d: should not have fired (%9.6f)", i, d.Seconds())
		default:
		}
	}
}
