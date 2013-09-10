package memberlist

import (
	"net"
	"time"
)

const (
	StateAlive = iota
	StateSuspect
	StateDead
)

// NodeState is used to manage our state view of another node
type NodeState struct {
	Name        string    // Remote node name
	Addr        net.Addr  // Remote address
	Incarnation int       // Last known incarnation number
	State       int       // Current state
	StateChange time.Time // Time last state change happened
}

// Schedule is used to ensure the Tick is performed periodically
func (m *Memberlist) schedule() {
	// Create a new ticker
	m.tickerLock.Lock()
	m.ticker = time.NewTicker(m.config.Interval)
	C := m.ticker.C
	m.tickerLock.Unlock()
	go func() {
		for {
			select {
			case <-C:
				m.tick()
			case <-m.stopTick:
				return
			}
		}
	}()
}

// Deschedule is used to stop the background maintenence
func (m *Memberlist) deschedule() {
	m.tickerLock.Lock()
	m.ticker.Stop()
	m.ticker = nil
	m.tickerLock.Unlock()
	m.stopTick <- struct{}{}
}

// Tick is used to perform a single round of failure detection and gossip
func (m *Memberlist) tick() {

}
