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
	// order to drive the timer to its minimum value. This is a float so
	// we don't have to cast it later.
	k float64

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
// to the min time after seeing k or more confirmations.
func newSuspicion(k int, min time.Duration, max time.Duration, f func(int32)) *suspicion {
	s := &suspicion{
		k:             float64(k),
		min:           min.Seconds(),
		max:           max.Seconds(),
		start:         time.Now(),
		confirmations: make(map[string]struct{}),
	}
	f_wrap := func() {
		f(atomic.LoadInt32(&s.n))
	}
	s.timer = time.AfterFunc(max, f_wrap)
	return s
}

// Corroborate registers that a possibly new peer has also determined the given
// node is suspect.
func (s *suspicion) Corroborate(from string) {
	// Only allow one confirmation from each possible peer.
	if _, ok := s.confirmations[from]; ok {
		return
	}
	s.confirmations[from] = struct{}{}

	// Compute the new timeout given the current number of confirmations.
	n := float64(atomic.AddInt32(&s.n, 1))
	timeout := math.Max(s.min, s.max-(s.max-s.min)*math.Log(n+1.0)/math.Log(s.k+1.0))

	// Reset the timer. We have to take into account the amount of time that
	// has passed so far, so we get the right overall timeout.
	remaining := math.Max(0.0, s.start.Sub(time.Now()).Seconds()+timeout)
	duration := time.Duration(math.Floor(1000.0*remaining)) * time.Millisecond
	if s.timer.Stop() {
		s.timer.Reset(duration)
	}
}
