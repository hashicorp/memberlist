package memberlist

import (
	"math"
	"sync/atomic"
	"time"
)

// suspicion manages the suspect timer for a node and provides an interface
// to accelerate the timeout as we get more independent confirmations that
// a node is suspect.
type suspicion struct {
	// k is the number of independent confirmations we'd like to see in
	// order to drive the timer to its minimum value.
	k int32

	// min is the minimum timer value in seconds.
	min float64

	// max is the maximum timer value in seconds.
	max float64

	// start captures the timestamp when we began the timer. This is used
	// so we can calculate durations to feed the timer during updates in
	// a way the achieves the overall time we'd like.
	start time.Time

	// timer is the underlying timer that implements the timeout.
	timer *time.Timer

	// n is the number of independent confirmations we've seen. This must
	// be updated using atomic instructions to prevent contention with the
	// timer callback.
	n int32

	// confirmations is a map of "from" nodes that have confirmed a given
	// node is suspect. This prevents double counting.
	confirmations map[string]struct{}
}

// newSuspicion returns a timer started with the max time, and that will drive
// to the min time after seeing k or more confirmations. The from node will be
// excluded from confirmations since we might get our own suspicion message
// gossiped back to us.
func newSuspicion(from string, k int, min time.Duration, max time.Duration, f func(int)) *suspicion {
	s := &suspicion{
		k:             int32(k),
		min:           min.Seconds(),
		max:           max.Seconds(),
		start:         time.Now(),
		confirmations: make(map[string]struct{}),
	}
	s.confirmations[from] = struct{}{}
	f_wrap := func() {
		f(int(atomic.LoadInt32(&s.n)))
	}
	s.timer = time.AfterFunc(max, f_wrap)
	return s
}

// Confirm registers that a possibly new peer has also determined the given
// node is suspect. This returns true if this was new information, and false
// if it was a duplicate confirmation, or if we've got enough confirmations to
// hit the minimum.
func (s *suspicion) Confirm(from string) bool {
	// If we've got enough confirmations then stop accepting them.
	if atomic.LoadInt32(&s.n) >= s.k {
		return false
	}

	// Only allow one confirmation from each possible peer.
	if _, ok := s.confirmations[from]; ok {
		return false
	}
	s.confirmations[from] = struct{}{}

	// Compute the new timeout given the current number of confirmations.
	n := float64(atomic.AddInt32(&s.n, 1))
	timeout := math.Max(s.min, s.max-(s.max-s.min)*math.Log(n+1.0)/math.Log(float64(s.k)+1.0))

	// Reset the timer. We have to take into account the amount of time that
	// has passed so far, so we get the right overall timeout.
	remaining := math.Max(0.0, s.start.Sub(time.Now()).Seconds()+timeout)
	duration := time.Duration(math.Floor(1000.0*remaining)) * time.Millisecond
	if s.timer.Stop() {
		s.timer.Reset(duration)
	}
	return true
}
