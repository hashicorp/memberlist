package memberlist

import (
	"log"
	"net"
	"sync/atomic"
	"time"
)

const (
	StateAlive = iota
	StateSuspect
	StateDead
)

// Node is used to represent a known node
type Node struct {
	Name string // Remote node name
	Addr net.IP // Remote address
}

// NodeState is used to manage our state view of another node
type NodeState struct {
	Node
	Incarnation uint32    // Last known incarnation number
	State       int       // Current state
	StateChange time.Time // Time last state change happened
}

// ackHandler is used to register handlers for incoming acks
type ackHandler struct {
	handler func()
	timer   *time.Timer
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
	if m.ticker != nil {
		m.ticker.Stop()
		m.ticker = nil
	}
	m.tickerLock.Unlock()
	select {
	case m.stopTick <- struct{}{}:
	default:
	}
}

// Tick is used to perform a single round of failure detection and gossip
func (m *Memberlist) tick() {
	// Track the number of indexes we've considered probing
	numCheck := 0
START:
	// Make sure we don't wrap around infinitely
	if numCheck >= len(m.nodes) {
		return
	}

	// Handle the wrap around case
	if m.tickIndex > len(m.nodes) {
		m.resetNodes()
		m.tickIndex = 0
	}

	// Determine if we should probe this node
	skip := false
	var node *NodeState
	m.nodeLock.RLock()

	node = m.nodes[m.tickIndex]
	if node.Name == m.config.Name {
		skip = true
	} else if node.State == StateDead {
		skip = true
	}

	// Potentially skip
	m.nodeLock.RUnlock()
	if skip {
		numCheck++
		m.tickIndex++
		goto START
	}

	// Probe the specific node
	m.probeNode(node)
}

// probeNode handles a single round of failure checking on a node
func (m *Memberlist) probeNode(node *NodeState) {
	// Send a ping to the node
	ping := ping{SeqNo: m.nextSeqNo()}
	destAddr := &net.UDPAddr{IP: node.Addr, Port: m.config.UDPPort}

	// Setup an ack handler
	ackCh := make(chan bool, m.config.IndirectChecks+1)
	m.setAckChannel(ping.SeqNo, ackCh, m.config.Interval)

	// Send the ping message
	if err := m.encodeAndSendMsg(destAddr, pingMsg, &ping); err != nil {
		log.Printf("[ERR] Failed to send ping: %s", err)
		return
	}

	// Wait for response or round-trip-time
	select {
	case v := <-ackCh:
		if v == true {
			return
		}
	case <-time.After(m.config.RTT):
	}

	// Get some random live nodes
	m.nodeLock.RLock()
	kNodes := kRandomNodes(m.config.IndirectChecks, m.config.Name, m.nodes)
	m.nodeLock.RUnlock()

	// Attempt an indirect ping
	ind := indirectPingReq{SeqNo: ping.SeqNo, Target: node.Addr}
	for _, peer := range kNodes {
		destAddr := &net.UDPAddr{IP: peer.Addr, Port: m.config.UDPPort}
		if err := m.encodeAndSendMsg(destAddr, indirectPingMsg, &ind); err != nil {
			log.Printf("[ERR] Failed to send indirect ping: %s", err)
		}
	}

	// Wait for the acks or timeout
	select {
	case v := <-ackCh:
		if v == true {
			return
		}
	}

	// No acks received from target, suspect
	s := suspect{Incarnation: node.Incarnation, Node: node.Name}
	m.suspectNode(&s)
}

// resetNodes is used when the tick wraps around. It will reap the
// dead nodes and shuffle the node list.
func (m *Memberlist) resetNodes() {
	m.nodeLock.Lock()
	defer m.nodeLock.Unlock()

	// Move the dead nodes
	deadIdx := moveDeadNodes(m.nodes)

	// Deregister the dead nodes
	for i := deadIdx; i < len(m.nodes); i++ {
		delete(m.nodeMap, m.nodes[i].Name)
	}

	// Trim the nodes to exclude the dead nodes
	m.nodes = m.nodes[0:deadIdx]

	// Shuffle live nodes
	shuffleNodes(m.nodes)
}

// nextSeqNo returns a usable sequence number in a thread safe way
func (m *Memberlist) nextSeqNo() uint32 {
	return atomic.AddUint32(&m.sequenceNum, 1)
}

// setAckChannel is used to attach a channel to receive a message when
// an ack with a given sequence number is received. The channel gets sent
// false on timeout
func (m *Memberlist) setAckChannel(seqNo uint32, ch chan bool, timeout time.Duration) {
	// Create a handler function
	handler := func() {
		select {
		case ch <- true:
		default:
		}
	}

	// Add the handler
	ah := &ackHandler{handler, nil}
	m.ackLock.Lock()
	m.ackHandlers[seqNo] = ah
	m.ackLock.Unlock()

	// Setup a reaping routing
	ah.timer = time.AfterFunc(timeout, func() {
		m.ackLock.Lock()
		delete(m.ackHandlers, seqNo)
		m.ackLock.Unlock()
		select {
		case ch <- false:
		default:
		}
	})
}

// setAckHandler is used to attach a handler to be invoked when an
// ack with a given sequence number is received. If a timeout is reached,
// the handler is deleted
func (m *Memberlist) setAckHandler(seqNo uint32, handler func(), timeout time.Duration) {
	// Add the handler
	ah := &ackHandler{handler, nil}
	m.ackLock.Lock()
	m.ackHandlers[seqNo] = ah
	m.ackLock.Unlock()

	// Setup a reaping routing
	ah.timer = time.AfterFunc(timeout, func() {
		m.ackLock.Lock()
		delete(m.ackHandlers, seqNo)
		m.ackLock.Unlock()
	})
}

// Invokes an Ack handler if any is associated, and reaps the handler immediately
func (m *Memberlist) invokeAckHandler(seqNo uint32) {
	m.ackLock.Lock()
	ah, ok := m.ackHandlers[seqNo]
	delete(m.ackHandlers, seqNo)
	m.ackLock.Unlock()
	if !ok {
		return
	}
	ah.timer.Stop()
	ah.handler()
}

// aliveNode is invoked by the network layer when we get a message
// about a live node
func (m *Memberlist) aliveNode(a *alive) {
	// TODO: Ignore we are alive
	// TODO: Re-broadcast
	m.nodeLock.Lock()
	defer m.nodeLock.Unlock()
	state, ok := m.nodeMap[a.Node]

	// Check if we've never seen this node before
	if !ok {
		state = &NodeState{
			Node: Node{
				Name: a.Node,
				Addr: a.Addr,
			},
			State: StateDead,
		}

		// Add to map
		m.nodeMap[a.Node] = state

		// Get a random offset. This is important to ensure
		// the failure detection bound is low on average. If all
		// nodes did an append, failure detection bound would be
		// very high.
		n := len(m.nodes)
		offset := randomOffset(n)

		// Add at the end and swap with the node at the offset
		m.nodes = append(m.nodes, state)
		m.nodes[offset], m.nodes[n] = m.nodes[n], m.nodes[offset]
	}

	// Bail if the incarnation number is old
	if a.Incarnation <= state.Incarnation {
		return
	}

	// Update the state and incarnation number
	oldState := state.State
	state.Incarnation = a.Incarnation
	if state.State != StateAlive {
		state.State = StateAlive
		state.StateChange = time.Now()
	}

	// if Dead -> Alive, notify of join
	if oldState == StateDead {
		m.notifyLock.RLock()
		defer m.notifyLock.RUnlock()
		notifyAll(m.notifyJoin, &state.Node)
	}
}

// suspectNode is invoked by the network layer when we get a message
// about a suspect node
func (m *Memberlist) suspectNode(s *suspect) {
	// TODO: Refute if _we_ are suspected
	// TODO: Re-broadcast
	m.nodeLock.Lock()
	defer m.nodeLock.Unlock()
	state, ok := m.nodeMap[s.Node]

	// If we've never heard about this node before, ignore it
	if !ok {
		return
	}

	// Ignore old incarnation numbers
	if s.Incarnation < state.Incarnation {
		return
	}

	// Ignore non-alive nodes
	if state.State != StateAlive {
		return
	}

	// Update the state
	state.Incarnation = s.Incarnation
	state.State = StateSuspect
	changeTime := time.Now()
	state.StateChange = changeTime

	// Setup a timeout for this
	timeout := suspicionTimeout(m.config.SuspicionMult, len(m.nodes), m.config.Interval)
	time.AfterFunc(timeout, func() {
		if state.State == StateSuspect && state.StateChange == changeTime {
			m.suspectTimeout(state)
		}
	})
}

// suspectTimeout is invoked when a suspect timeout has occurred
func (m *Memberlist) suspectTimeout(n *NodeState) {
	// Construct a dead message
	d := dead{Incarnation: n.Incarnation, Node: n.Name}
	m.deadNode(&d)
}

// deadNode is invoked by the network layer when we get a message
// about a dead node
func (m *Memberlist) deadNode(d *dead) {
	// TODO: Re-broadcast
	// TODO: Refute if us?
	m.nodeLock.Lock()
	defer m.nodeLock.Unlock()
	state, ok := m.nodeMap[d.Node]

	// If we've never heard about this node before, ignore it
	if !ok {
		return
	}

	// Ignore old incarnation numbers
	if d.Incarnation < state.Incarnation {
		return
	}

	// Ignore if node is already dead
	if state.State == StateDead {
		return
	}

	// Update the state
	state.Incarnation = d.Incarnation
	state.State = StateDead
	state.StateChange = time.Now()

	// Notify of death
	m.notifyLock.RLock()
	defer m.notifyLock.RUnlock()
	notifyAll(m.notifyLeave, &state.Node)
}

// mergeState is invoked by the network layer when we get a Push/Pull
// state transfer
func (m *Memberlist) mergeState(remote []pushNodeState) {
	for _, r := range remote {
		// Look for a matching local node
		m.nodeLock.RLock()
		local, ok := m.nodeMap[r.Name]
		m.nodeLock.RUnlock()

		// Skip if we agree on states
		if ok && local.State == r.State {
			continue
		}

		switch r.State {
		case StateAlive:
			a := alive{Incarnation: r.Incarnation, Node: r.Name, Addr: r.Addr}
			m.aliveNode(&a)

		case StateSuspect:
			s := suspect{Incarnation: r.Incarnation, Node: r.Name}
			m.suspectNode(&s)

		case StateDead:
			d := dead{Incarnation: r.Incarnation, Node: r.Name}
			m.deadNode(&d)
		}
	}
}
