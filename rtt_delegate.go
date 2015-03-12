package memberlist

import "time"

// RTTDelegate is used to notify an observer how long it took for a
// ping message to complete a round trip.
type RTTDelegate interface {
	// NotifyRTT is invoked when an ack for a ping is received
	NotifyRTT(other *Node, rtt time.Duration)
}
