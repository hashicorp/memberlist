package memberlist

import (
	"math"
	"time"
)

type suspicion struct {
	k             float64
	min           float64
	max           float64
	start         time.Time
	timer         *time.Timer
	confirmations map[string]struct{}
}

func newSuspicion(k int, min time.Duration, max time.Duration, f func()) *suspicion {
	return &suspicion{
		k:             float64(k),
		min:           min.Seconds(),
		max:           max.Seconds(),
		start:         time.Now(),
		timer:         time.AfterFunc(max, f),
		confirmations: make(map[string]struct{}),
	}
}

func (s *suspicion) Corroborate(from string) {
	// Only allow one confirmation from each possible peer.
	if _, ok := s.confirmations[from]; ok {
		return
	}
	s.confirmations[from] = struct{}{}

	// Compute the new timeout given the current number of confirmations.
	n := float64(len(s.confirmations))
	timeout := math.Max(s.min, s.max-(s.max-s.min)*math.Log(n+1.0)/math.Log(s.k+1.0))

	// Reset the timer. Note we don't care if this returns false (the timer
	// already fired). We have to take into account the amount of time that
	// has passed so far, so we get the right overall timeout.
	remaining := math.Max(0.0, s.start.Sub(time.Now()).Seconds()+timeout)
	duration := time.Duration(math.Floor(1000.0*remaining)) * time.Millisecond
	s.timer.Reset(duration)
}
