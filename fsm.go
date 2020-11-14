package memberlist

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/armon/go-metrics"
	"github.com/hashicorp/go-hclog"
	multierror "github.com/hashicorp/go-multierror"
)

type NodeInfo struct {
	Incarnation uint32
	Node        string
}

type InlineAck struct {
	Node string
}

type FSMActions interface {
	ExchangeState(node string, states []FSMNodeState, join bool) ([]FSMNodeState, error)
	NodeAlive(node string, state NodeInfo) error
	NodeSuspect(node string, state NodeInfo) error
	NodeDead(node string, state NodeInfo) error
	NodePing(node string, seq uint32) (time.Duration, *InlineAck, error)
	NodeAck(node string, seq uint32) error
	NodeNack(node string, seq uint32) error
	NodeIndirectPing(node, target string, seq uint32) (time.Duration, *InlineAck, error)
}

type FSMNode struct {
	Name  string
	Meta  []byte        // Metadata from the delegate for this node.
	State NodeStateType // State of the node.
}

// String returns the node name
func (n *FSMNode) String() string {
	return n.Name
}

// NodeState is used to manage our state view of another node
type fsmNodeState struct {
	FSMNode
	Incarnation uint32        // Last known incarnation number
	State       NodeStateType // Current state
	StateChange time.Time     // Time last state change happened
}

func (n *fsmNodeState) DeadOrLeft() bool {
	return n.State == StateDead || n.State == StateLeft
}

// kRandomNodes is used to select up to k random nodes, excluding any nodes where
// the filter function returns true. It is possible that less than k nodes are
// returned.
func kRandomFSMNodes(k int, nodes []*fsmNodeState, filterFn func(*fsmNodeState) bool) []*fsmNodeState {
	n := len(nodes)
	kNodes := make([]*fsmNodeState, 0, k)
OUTER:
	// Probe up to 3*n times, with large n this is not necessary
	// since k << n, but with small n we want search to be
	// exhaustive
	for i := 0; i < 3*n && len(kNodes) < k; i++ {
		// Get random node
		idx := randomOffset(n)
		node := nodes[idx]

		// Give the filter a shot at it.
		if filterFn != nil && filterFn(node) {
			continue OUTER
		}

		// Check if we have this node already
		for j := 0; j < len(kNodes); j++ {
			if node == kNodes[j] {
				continue OUTER
			}
		}

		// Append the node
		kNodes = append(kNodes, node)
	}
	return kNodes
}

// moveDeadNodes moves nodes that are dead and beyond the gossip to the dead interval
// to the end of the slice and returns the index of the first moved node.
func moveDeadFSMNodes(nodes []*fsmNodeState, gossipToTheDeadTime time.Duration) int {
	numDead := 0
	n := len(nodes)
	for i := 0; i < n-numDead; i++ {
		if nodes[i].State != StateDead {
			continue
		}

		// Respect the gossip to the dead interval
		if time.Since(nodes[i].StateChange) <= gossipToTheDeadTime {
			continue
		}

		// Move this node to the end
		nodes[i], nodes[n-numDead-1] = nodes[n-numDead-1], nodes[i]
		numDead++
		i--
	}
	return n - numDead
}

// shuffleNodes randomly shuffles the input nodes using the Fisher-Yates shuffle
func shuffleFSMNodes(nodes []*fsmNodeState) {
	n := len(nodes)
	rand.Shuffle(n, func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})
}

type FSM struct {
	*Config

	actions FSMActions

	Logger hclog.Logger

	tickerLock sync.Mutex
	tickers    []*time.Ticker
	stopTick   chan struct{}
	probeIndex int

	nodeLock   sync.RWMutex
	numNodes   uint32                   // Number of known nodes (estimate)
	nodes      []*fsmNodeState          // Known nodes
	nodeMap    map[string]*fsmNodeState // Maps FSMNode.Name -> FSMNodeState
	nodeTimers map[string]*suspicion    // Maps FSMNode.Name -> suspicion timer
	awareness  *awareness

	ackLock     sync.Mutex
	ackHandlers map[uint32]*fsmAckHandler

	leave          int32 // Used as an atomic boolean value
	leaveBroadcast chan struct{}

	sequenceNum uint32 // Local sequence number
	incarnation uint32 // Local incarnation number

	broadcasts *TransmitLimitedQueue
}

func NewFSM(L hclog.Logger, actions FSMActions, config *Config) (*FSM, error) {
	m := &FSM{
		Config:  config,
		actions: actions,
		Logger:  L,

		leaveBroadcast: make(chan struct{}, 1),
		nodeMap:        make(map[string]*fsmNodeState),
		nodeTimers:     make(map[string]*suspicion),
		awareness:      newAwareness(config.AwarenessMaxMultiplier),
		ackHandlers:    make(map[uint32]*fsmAckHandler),

		broadcasts: &TransmitLimitedQueue{RetransmitMult: config.RetransmitMult},
	}

	m.broadcasts.NumNodes = func() int {
		return m.estNumNodes()
	}

	n := &FSMNodeState{
		Name:        config.Name,
		Incarnation: m.nextIncarnation(),
	}

	m.aliveNode(n, nil, true)

	return m, nil
}

// triggerFunc is used to trigger a function call each time a
// message is received until a stop tick arrives.
func (m *FSM) triggerFunc(stagger time.Duration, C <-chan time.Time, stop <-chan struct{}, f func()) {
	// Use a random stagger to avoid syncronizing
	randStagger := time.Duration(uint64(rand.Int63()) % uint64(stagger))
	select {
	case <-time.After(randStagger):
	case <-stop:
		return
	}
	for {
		select {
		case <-C:
			f()
		case <-stop:
			return
		}
	}
}

// estNumNodes is used to get the current estimate of the number of nodes
func (m *FSM) estNumNodes() int {
	return int(atomic.LoadUint32(&m.numNodes))
}

// pushPull is invoked periodically to randomly perform a complete state
// exchange. Used to ensure a high level of convergence, but is also
// reasonably expensive as the entire state of this node is exchanged
// with the other node.
func (m *FSM) pushPull() {
	// Get a random live node
	m.nodeLock.RLock()
	nodes := kRandomFSMNodes(1, m.nodes, func(n *fsmNodeState) bool {
		return n.Name == m.Name ||
			n.State != StateAlive
	})
	m.nodeLock.RUnlock()

	// If no nodes, bail
	if len(nodes) == 0 {
		return
	}
	node := nodes[0]

	// Attempt a push pull
	if err := m.pushPullNode(node.Name, false); err != nil {
		m.Logger.Error("Push/Pull failed", "error", err, "node", node.Name)
	}
}

// pushPullTrigger is used to periodically trigger a push/pull until
// a stop tick arrives. We don't use triggerFunc since the push/pull
// timer is dynamically scaled based on cluster size to avoid network
// saturation
func (m *FSM) pushPullTrigger(stop <-chan struct{}) {
	interval := m.PushPullInterval

	// Use a random stagger to avoid syncronizing
	randStagger := time.Duration(uint64(rand.Int63()) % uint64(interval))
	select {
	case <-time.After(randStagger):
	case <-stop:
		return
	}

	// Tick using a dynamic timer
	for {
		tickTime := pushPullScale(interval, m.estNumNodes())
		select {
		case <-time.After(tickTime):
			m.pushPull()
		case <-stop:
			return
		}
	}
}

// Schedule is used to ensure the Tick is performed periodically. This
// function is safe to call multiple times. If the memberlist is already
// scheduled, then it won't do anything.
func (m *FSM) schedule() {
	m.tickerLock.Lock()
	defer m.tickerLock.Unlock()

	// If we already have tickers, then don't do anything, since we're
	// scheduled
	if len(m.tickers) > 0 {
		return
	}

	// Create the stop tick channel, a blocking channel. We close this
	// when we should stop the tickers.
	stopCh := make(chan struct{})

	// Create a new probeTicker
	if m.ProbeInterval > 0 {
		t := time.NewTicker(m.ProbeInterval)
		go m.triggerFunc(m.ProbeInterval, t.C, stopCh, m.probe)
		m.tickers = append(m.tickers, t)
	}

	// Create a push pull ticker if needed
	if m.PushPullInterval > 0 {
		go m.pushPullTrigger(stopCh)
	}

	// Create a gossip ticker if needed
	if m.GossipInterval > 0 && m.GossipNodes > 0 {
		t := time.NewTicker(m.GossipInterval)
		go m.triggerFunc(m.GossipInterval, t.C, stopCh, m.gossip)
		m.tickers = append(m.tickers, t)
	}

	// If we made any tickers, then record the stopTick channel for
	// later.
	if len(m.tickers) > 0 {
		m.stopTick = stopCh
	}
}

func (m *FSM) getBroadcasts() []Broadcast {
	return m.broadcasts.GetAvailableBroadcasts()
}

func (m *FSM) sendMesasges(n *fsmNodeState, msg []Broadcast) {

}

// gossip is invoked every GossipInterval period to broadcast our gossip
// messages to a few random nodes.
func (m *FSM) gossip() {
	// Get some random live, suspect, or recently dead nodes
	m.nodeLock.RLock()
	kNodes := kRandomFSMNodes(m.GossipNodes, m.nodes, func(n *fsmNodeState) bool {
		if n.Name == m.Name {
			return true
		}

		switch n.State {
		case StateAlive, StateSuspect:
			return false

		case StateDead:
			return time.Since(n.StateChange) > m.GossipToTheDeadTime

		default:
			return true
		}
	})
	m.nodeLock.RUnlock()

	/*

		// Compute the bytes available
		bytesAvail := m.config.UDPBufferSize - compoundHeaderOverhead
		if m.config.EncryptionEnabled() {
			bytesAvail -= encryptOverhead(m.encryptionVersion())
		}

	*/

	for _, node := range kNodes {
		// Get any pending broadcasts
		msgs := m.getBroadcasts()
		if len(msgs) == 0 {
			return
		}

		for _, x := range msgs {
			val := x.(*fsmBroadcast)

			switch val.action {
			case "alive":
				err := m.actions.NodeAlive(node.Name, val.value.(NodeInfo))
				if err != nil {
					m.Logger.Error("error sending alive", "error", err, "node", node.Name)
				}
			case "suspect":
				err := m.actions.NodeSuspect(node.Name, val.value.(NodeInfo))
				if err != nil {
					m.Logger.Error("error sending alive", "error", err, "node", node.Name)
				}
			case "dead":
				err := m.actions.NodeDead(node.Name, val.value.(NodeInfo))
				if err != nil {
					m.Logger.Error("error sending alive", "error", err, "node", node.Name)
				}
			}
		}
	}
}

// Tick is used to perform a single round of failure detection and gossip
func (m *FSM) probe() {
	// Track the number of indexes we've considered probing
	numCheck := 0
START:
	m.nodeLock.RLock()

	// Make sure we don't wrap around infinitely
	if numCheck >= len(m.nodes) {
		m.nodeLock.RUnlock()
		return
	}

	// Handle the wrap around case
	if m.probeIndex >= len(m.nodes) {
		m.nodeLock.RUnlock()
		m.resetNodes()
		m.probeIndex = 0
		numCheck++
		goto START
	}

	// Determine if we should probe this node
	skip := false
	var node fsmNodeState

	node = *m.nodes[m.probeIndex]
	if node.Name == m.Name {
		skip = true
	} else if node.DeadOrLeft() {
		skip = true
	}

	// Potentially skip
	m.nodeLock.RUnlock()
	m.probeIndex++
	if skip {
		numCheck++
		goto START
	}

	// Probe the specific node
	m.probeNode(&node)
}

// nextSeqNo returns a usable sequence number in a thread safe way
func (m *FSM) nextSeqNo() uint32 {
	return atomic.AddUint32(&m.sequenceNum, 1)
}

type fsmAckMessage struct {
	Complete  bool
	Payload   NodeInfo
	Timestamp time.Time
}

// setProbeChannels is used to attach the ackCh to receive a message when an ack
// with a given sequence number is received. The `complete` field of the message
// will be false on timeout. Any nack messages will cause an empty struct to be
// passed to the nackCh, which can be nil if not needed.
func (m *FSM) setProbeChannels(seqNo uint32, ackCh chan fsmAckMessage, nackCh chan struct{}, timeout time.Duration) {
	// Create handler functions for acks and nacks
	ackFn := func(info NodeInfo, timestamp time.Time) {
		select {
		case ackCh <- fsmAckMessage{true, info, timestamp}:
		default:
		}
	}
	nackFn := func() {
		select {
		case nackCh <- struct{}{}:
		default:
		}
	}

	// Add the handlers
	ah := &fsmAckHandler{ackFn, nackFn, nil}
	m.ackLock.Lock()
	m.ackHandlers[seqNo] = ah
	m.ackLock.Unlock()

	// Setup a reaping routing
	ah.timer = time.AfterFunc(timeout, func() {
		m.ackLock.Lock()
		delete(m.ackHandlers, seqNo)
		m.ackLock.Unlock()
		select {
		case ackCh <- fsmAckMessage{false, NodeInfo{}, time.Now()}:
		default:
		}
	})
}

type fsmAckHandler struct {
	ackFn  func(NodeInfo, time.Time)
	nackFn func()
	timer  *time.Timer
}

func (m *FSM) setAckHandler(seqNo uint32, ackFn func(NodeInfo, time.Time), timeout time.Duration) {
	// Add the handler
	ah := &fsmAckHandler{ackFn, nil, nil}
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

// Invokes an ack handler if any is associated, and reaps the handler immediately
func (m *FSM) invokeAckHandler(seqno uint32, info NodeInfo, timestamp time.Time) {
	m.ackLock.Lock()
	ah, ok := m.ackHandlers[seqno]
	delete(m.ackHandlers, seqno)
	m.ackLock.Unlock()
	if !ok {
		return
	}

	ah.timer.Stop()
	ah.ackFn(info, timestamp)
}

// Invokes nack handler if any is associated.
func (m *FSM) invokeNackHandler(seqno uint32) {
	m.ackLock.Lock()
	ah, ok := m.ackHandlers[seqno]
	m.ackLock.Unlock()
	if !ok || ah.nackFn == nil {
		return
	}
	ah.nackFn()
}

func (m *FSM) sendPing(node *fsmNodeState, p *ping) error {
	_, ack, err := m.actions.NodePing(p.Node, p.SeqNo)
	if err != nil {
		return err
	}

	if ack != nil {
		m.invokeAckHandler(p.SeqNo, NodeInfo{
			Node: p.Node,
		}, time.Now())
	}

	return nil
}

func (m *FSM) sendIndirect(n *fsmNodeState, ind *indirectPingReq) error {
	_, ack, err := m.actions.NodeIndirectPing(n.Name, ind.Node, ind.SeqNo)
	if err != nil {
		return err
	}

	if ack != nil {
		m.invokeAckHandler(ind.SeqNo, NodeInfo{
			Node: ind.Node,
		}, time.Now())
	}

	return nil
}

func (m *FSM) sendSuspect(node *fsmNodeState, p *ping) error {
	return m.actions.NodeSuspect(node.Name, NodeInfo{Node: p.Node})
}

func (m *FSM) NodePing(seqno uint32) (*InlineAck, error) {
	return &InlineAck{Node: m.Config.Name}, nil
}

func (m *FSM) NodeAck(seqno uint32) error {
	m.invokeAckHandler(seqno, NodeInfo{Node: m.Config.Name}, time.Now())
	return nil
}

func (m *FSM) NodeNack(seqno uint32) error {
	m.invokeNackHandler(seqno)
	return nil
}

func (m *FSM) NodeIndirectPing(requester string, seqno uint32, nack bool, info NodeInfo) (*InlineAck, error) {
	// Send a ping to the correct host.
	localSeqNo := m.nextSeqNo()

	// Setup a response handler to relay the ack
	cancelCh := make(chan struct{})
	respHandler := func(ack NodeInfo, timestamp time.Time) {
		// Try to prevent the nack if we've caught it in time.
		close(cancelCh)

		if err := m.actions.NodeAck(requester, seqno); err != nil {
			m.Logger.Error("Failed to forward ack", "error", err, "requester", requester)
		}
	}
	m.setAckHandler(localSeqNo, respHandler, m.Config.ProbeTimeout)

	_, ack, err := m.actions.NodePing(info.Node, localSeqNo)
	if err != nil {
		m.Logger.Error("Failed to send indirect ping", "error", err, "requester", requester)
	}

	if ack != nil {
		return ack, nil
	}

	// Setup a timer to fire off a nack if no ack is seen in time.
	if nack {
		go func() {
			select {
			case <-cancelCh:
				return
			case <-time.After(m.Config.ProbeTimeout):
				err := m.actions.NodeNack(requester, seqno)
				if err != nil {
					m.Logger.Error("Failed to send nack", "error", err, "requester", requester)
				}
			}
		}()
	}

	return nil, nil
}

// probeNode handles a single round of failure checking on a node.
func (m *FSM) probeNode(node *fsmNodeState) {
	// We use our health awareness to scale the overall probe interval, so we
	// slow down if we detect problems. The ticker that calls us can handle
	// us running over the base interval, and will skip missed ticks.
	probeInterval := m.awareness.ScaleTimeout(m.ProbeInterval)
	// if probeInterval > m.ProbeInterval {
	// metrics.IncrCounter([]string{"memberlist", "degraded", "probe"}, 1)
	// }

	// Prepare a ping message and setup an ack handler.
	// selfAddr, selfPort := m.getAdvertise()
	ping := ping{
		SeqNo: m.nextSeqNo(),
		Node:  node.Name,
		// SourceAddr: selfAddr,
		// SourcePort: selfPort,
		SourceNode: m.Name,
	}
	ackCh := make(chan fsmAckMessage, m.IndirectChecks+1)
	nackCh := make(chan struct{}, m.IndirectChecks+1)
	m.setProbeChannels(ping.SeqNo, ackCh, nackCh, probeInterval)

	// Mark the sent time here, which should be after any pre-processing but
	// before system calls to do the actual send. This probably over-reports
	// a bit, but it's the best we can do. We had originally put this right
	// after the I/O, but that would sometimes give negative RTT measurements
	// which was not desirable.
	// sent := time.Now()

	// Send a ping to the node. If this node looks like it's suspect or dead,
	// also tack on a suspect message so that it has a chance to refute as
	// soon as possible.
	// deadline := sent.Add(probeInterval)
	// addr := node.Address()

	// Arrange for our self-awareness to get updated.
	var awarenessDelta int
	defer func() {
		m.awareness.ApplyDelta(awarenessDelta)
	}()
	// if err := m.encodeAndSendMsg(node.FullAddress(), pingMsg, &ping); err != nil {
	if err := m.sendPing(node, &ping); err != nil {
		m.Logger.Error("Failed to send ping", "error", err)
		if failedRemote(err) {
			goto HANDLE_REMOTE_FAILURE
		} else {
			return
		}
	}

	if node.State != StateAlive {
		if err := m.sendSuspect(node, &ping); err != nil {
			m.Logger.Error("Failed to send ping", "error", err)
			if failedRemote(err) {
				goto HANDLE_REMOTE_FAILURE
			} else {
				return
			}
		}
	}

	// Arrange for our self-awareness to get updated. At this point we've
	// sent the ping, so any return statement means the probe succeeded
	// which will improve our health until we get to the failure scenarios
	// at the end of this function, which will alter this delta variable
	// accordingly.
	awarenessDelta = -1

	// Wait for response or round-trip-time.
	select {
	case v := <-ackCh:
		if v.Complete == true {
			if m.Ping != nil {
				// rtt := v.Timestamp.Sub(sent)
				// m.Config.Ping.NotifyPingComplete(&node.Node, rtt, v.Payload)
			}
			return
		}

		// As an edge case, if we get a timeout, we need to re-enqueue it
		// here to break out of the select below.
		if v.Complete == false {
			ackCh <- v
		}
	case <-time.After(m.Config.ProbeTimeout):
		// Note that we don't scale this timeout based on awareness and
		// the health score. That's because we don't really expect waiting
		// longer to help get UDP through. Since health does extend the
		// probe interval it will give the TCP fallback more time, which
		// is more active in dealing with lost packets, and it gives more
		// time to wait for indirect acks/nacks.
		m.Logger.Debug("Failed ping: (timeout reached)", "node", node.Name)
	}

HANDLE_REMOTE_FAILURE:
	// Get some random live nodes.
	m.nodeLock.RLock()
	kNodes := kRandomFSMNodes(m.Config.IndirectChecks, m.nodes, func(n *fsmNodeState) bool {
		return n.Name == m.Config.Name ||
			n.Name == node.Name ||
			n.State != StateAlive
	})
	m.nodeLock.RUnlock()

	// Attempt an indirect ping.
	expectedNacks := 0
	// selfAddr, selfPort = m.getAdvertise()
	ind := indirectPingReq{
		SeqNo:      ping.SeqNo,
		Node:       node.Name,
		SourceNode: m.Config.Name,
	}
	for _, peer := range kNodes {
		expectedNacks++

		if err := m.sendIndirect(peer, &ind); err != nil {
			m.Logger.Error("Failed to send indirect ping", "error", err, "node", node.Name)
		}
	}

	// Also make an attempt to contact the node directly over TCP. This
	// helps prevent confused clients who get isolated from UDP traffic
	// but can still speak TCP (which also means they can possibly report
	// misinformation to other nodes via anti-entropy), avoiding flapping in
	// the cluster.
	//
	// This is a little unusual because we will attempt a TCP ping to any
	// member who understands version 3 of the protocol, regardless of
	// which protocol version we are speaking. That's why we've included a
	// config option to turn this off if desired.
	/*
		fallbackCh := make(chan bool, 1)

		disableTcpPings := m.Config.DisableTcpPings ||
			(m.Config.DisableTcpPingsForNode != nil && m.Config.DisableTcpPingsForNode(node.Name))
		if (!disableTcpPings) && (node.PMax >= 3) {
			go func() {
				defer close(fallbackCh)
				didContact, err := m.sendPingAndWaitForAck(node.FullAddress(), ping, deadline)
				if err != nil {
					m.logger.Printf("[ERR] memberlist: Failed fallback ping: %s", err)
				} else {
					fallbackCh <- didContact
				}
			}()
		} else {
			close(fallbackCh)
		}
	*/

	// Wait for the acks or timeout. Note that we don't check the fallback
	// channel here because we want to issue a warning below if that's the
	// *only* way we hear back from the peer, so we have to let this time
	// out first to allow the normal UDP-based acks to come in.
	select {
	case v := <-ackCh:
		if v.Complete == true {
			return
		}
	}

	// Finally, poll the fallback channel. The timeouts are set such that
	// the channel will have something or be closed without having to wait
	// any additional time here.
	/*
		for didContact := range fallbackCh {
			if didContact {
				m.logger.Printf("[WARN] memberlist: Was able to connect to %s but other probes failed, network may be misconfigured", node.Name)
				return
			}
		}
	*/

	// Update our self-awareness based on the results of this failed probe.
	// If we don't have peers who will send nacks then we penalize for any
	// failed probe as a simple health metric. If we do have peers to nack
	// verify, then we can use that as a more sophisticated measure of self-
	// health because we assume them to be working, and they can help us
	// decide if the probed node was really dead or if it was something wrong
	// with ourselves.
	awarenessDelta = 0
	if expectedNacks > 0 {
		if nackCount := len(nackCh); nackCount < expectedNacks {
			awarenessDelta += (expectedNacks - nackCount)
		}
	} else {
		awarenessDelta += 1
	}

	// No acks received from target, suspect it as failed.
	m.Logger.Info("Suspect node has failed, no acks received", "node", node.Name)
	s := suspect{Incarnation: node.Incarnation, Node: node.Name, From: m.Config.Name}
	m.suspectNode(&s)
}

type fsmBroadcast struct {
	action string
	name   string
	value  interface{}
	notify chan struct{}
}

func (b *fsmBroadcast) Invalidates(other Broadcast) bool {
	// Check if that broadcast is a memberlist type
	mb, ok := other.(*fsmBroadcast)
	if !ok {
		return false
	}

	// Invalidates any message about the same node
	return b.name == mb.name
}

// memberlist.NamedBroadcast optional interface
func (b *fsmBroadcast) Name() string {
	return b.name
}

func (b *fsmBroadcast) Message() []byte {
	return nil
}

func (b *fsmBroadcast) Finished() {
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

func (m *FSM) broadcastSuspect(n string, s *suspect) {
	b := &fsmBroadcast{
		action: "suspect",
		name:   n,
		value: NodeInfo{
			Node:        s.Node,
			Incarnation: s.Incarnation,
		},
	}

	m.broadcasts.QueueBroadcast(b)
}

func (m *FSM) broadcastAlive(n string, a *fsmNodeState, notify chan struct{}) {
	b := &fsmBroadcast{
		action: "alive",
		name:   n,
		notify: notify,
		value: NodeInfo{
			Node:        a.Name,
			Incarnation: a.Incarnation,
		},
	}

	m.broadcasts.QueueBroadcast(b)
}

func (m *FSM) broadcastDead(n string, d *dead, notify chan struct{}) {
	b := &fsmBroadcast{
		action: "dead",
		name:   n,
		notify: notify,
		value: NodeInfo{
			Node:        d.Node,
			Incarnation: d.Incarnation,
		},
	}

	m.broadcasts.QueueBroadcast(b)
}

// nextIncarnation returns the next incarnation number in a thread safe way
func (m *FSM) nextIncarnation() uint32 {
	return atomic.AddUint32(&m.incarnation, 1)
}

// skipIncarnation adds the positive offset to the incarnation number.
func (m *FSM) skipIncarnation(offset uint32) uint32 {
	return atomic.AddUint32(&m.incarnation, offset)
}

// refute gossips an alive message in response to incoming information that we
// are suspect or dead. It will make sure the incarnation number beats the given
// accusedInc value, or you can supply 0 to just get the next incarnation number.
// This alters the node state that's passed in so this MUST be called while the
// nodeLock is held.
func (m *FSM) refute(me *fsmNodeState, accusedInc uint32) {
	// Make sure the incarnation number beats the accusation.
	inc := m.nextIncarnation()
	if accusedInc >= inc {
		inc = m.skipIncarnation(accusedInc - inc + 1)
	}
	me.Incarnation = inc

	// Decrease our health because we are being asked to refute a problem.
	m.awareness.ApplyDelta(1)

	// Format and broadcast an alive message.
	m.broadcastAlive(me.Name, me, nil)
}

func (m *FSM) NodeSuspect(info NodeInfo) error {
	m.suspectNode(&suspect{
		Node:        info.Node,
		Incarnation: info.Incarnation,
	})
	return nil
}

// suspectNode is invoked by the network layer when we get a message
// about a suspect node
func (m *FSM) suspectNode(s *suspect) {
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

	// See if there's a suspicion timer we can confirm. If the info is new
	// to us we will go ahead and re-gossip it. This allows for multiple
	// independent confirmations to flow even when a node probes a node
	// that's already suspect.
	if timer, ok := m.nodeTimers[s.Node]; ok {
		if timer.Confirm(s.From) {
			m.broadcastSuspect(s.Node, s)
		}
		return
	}

	// Ignore non-alive nodes
	if state.State != StateAlive {
		return
	}

	// If this is us we need to refute, otherwise re-broadcast
	if state.Name == m.Config.Name {
		m.refute(state, s.Incarnation)
		m.Logger.Warn("Refuting a suspect message", "from", s.From)
		return // Do not mark ourself suspect
	} else {
		m.broadcastSuspect(s.Node, s)
	}

	// Update metrics
	// metrics.IncrCounter([]string{"memberlist", "msg", "suspect"}, 1)

	// Update the state
	state.Incarnation = s.Incarnation
	state.State = StateSuspect
	changeTime := time.Now()
	state.StateChange = changeTime

	// Setup a suspicion timer. Given that we don't have any known phase
	// relationship with our peers, we set up k such that we hit the nominal
	// timeout two probe intervals short of what we expect given the suspicion
	// multiplier.
	k := m.Config.SuspicionMult - 2

	// If there aren't enough nodes to give the expected confirmations, just
	// set k to 0 to say that we don't expect any. Note we subtract 2 from n
	// here to take out ourselves and the node being probed.
	n := m.estNumNodes()
	if n-2 < k {
		k = 0
	}

	// Compute the timeouts based on the size of the cluster.
	min := suspicionTimeout(m.Config.SuspicionMult, n, m.Config.ProbeInterval)
	max := time.Duration(m.Config.SuspicionMaxTimeoutMult) * min
	fn := func(numConfirmations int) {
		m.nodeLock.Lock()
		state, ok := m.nodeMap[s.Node]
		timeout := ok && state.State == StateSuspect && state.StateChange == changeTime
		m.nodeLock.Unlock()

		if timeout {
			if k > 0 && numConfirmations < k {
				// metrics.IncrCounter([]string{"memberlist", "degraded", "timeout"}, 1)
			}

			m.Logger.Info("Marking node as failed, suspect timeout reached", "node", state.Name, "confirmations", numConfirmations)
			d := dead{Incarnation: state.Incarnation, Node: state.Name, From: m.Config.Name}
			m.deadNode(&d)
		}
	}
	m.nodeTimers[s.Node] = newSuspicion(s.From, k, min, max, fn)
}

func (m *FSM) hasLeft() bool {
	return atomic.LoadInt32(&m.leave) == 1
}

func (m *FSM) NodeDead(info NodeInfo) error {
	m.deadNode(&dead{
		Node:        info.Node,
		Incarnation: info.Incarnation,
	})
	return nil
}

// deadNode is invoked by the network layer when we get a message
// about a dead node
func (m *FSM) deadNode(d *dead) {
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

	// Clear out any suspicion timer that may be in effect.
	delete(m.nodeTimers, d.Node)

	// Ignore if node is already dead
	if state.DeadOrLeft() {
		return
	}

	// Check if this is us
	if state.Name == m.Config.Name {
		// If we are not leaving we need to refute
		if !m.hasLeft() {
			m.refute(state, d.Incarnation)
			m.Logger.Warn("Refuting a dead message", "from", d.From)
			return // Do not mark ourself dead
		}

		// If we are leaving, we broadcast and wait
		m.broadcastDead(d.Node, d, m.leaveBroadcast)
	} else {
		m.broadcastDead(d.Node, d, nil)
	}

	// Update metrics
	metrics.IncrCounter([]string{"memberlist", "msg", "dead"}, 1)

	// Update the state
	state.Incarnation = d.Incarnation

	// If the dead message was send by the node itself, mark it is left
	// instead of dead.
	if d.Node == d.From {
		state.State = StateLeft
	} else {
		state.State = StateDead
	}
	state.StateChange = time.Now()
}

// resetNodes is used when the tick wraps around. It will reap the
// dead nodes and shuffle the node list.
func (m *FSM) resetNodes() {
	m.nodeLock.Lock()
	defer m.nodeLock.Unlock()

	// Move dead nodes, but respect gossip to the dead interval
	deadIdx := moveDeadFSMNodes(m.nodes, m.GossipToTheDeadTime)

	// Deregister the dead nodes
	for i := deadIdx; i < len(m.nodes); i++ {
		delete(m.nodeMap, m.nodes[i].Name)
		m.nodes[i] = nil
	}

	// Trim the nodes to exclude the dead nodes
	m.nodes = m.nodes[0:deadIdx]

	// Update numNodes after we've trimmed the dead nodes
	atomic.StoreUint32(&m.numNodes, uint32(deadIdx))

	// Shuffle live nodes
	shuffleFSMNodes(m.nodes)
}

// Members returns a list of all known live nodes. The node structures
// returned must not be modified. If you wish to modify a Node, make a
// copy first.
func (m *FSM) Members() []*FSMNode {
	m.nodeLock.RLock()
	defer m.nodeLock.RUnlock()

	nodes := make([]*FSMNode, 0, len(m.nodes))
	for _, n := range m.nodes {
		if !n.DeadOrLeft() {
			nodes = append(nodes, &n.FSMNode)
		}
	}

	return nodes
}

// NumMembers returns the number of alive nodes currently known. Between
// the time of calling this and calling Members, the number of alive nodes
// may have changed, so this shouldn't be used to determine how many
// members will be returned by Members.
func (m *FSM) NumMembers() (alive int) {
	m.nodeLock.RLock()
	defer m.nodeLock.RUnlock()

	for _, n := range m.nodes {
		if !n.DeadOrLeft() {
			alive++
		}
	}

	return
}

func (m *FSM) Join(existing []string) (int, error) {
	numSuccess := 0
	var errs error
	for _, exist := range existing {
		if err := m.pushPullNode(exist, true); err != nil {
			err = fmt.Errorf("Failed to join %s: %v", existing, err)
			errs = multierror.Append(errs, err)
			m.Logger.Debug("error pulling existing data", "error", err)
			continue
		}
		numSuccess++
	}
	if numSuccess > 0 {
		errs = nil
	}
	return numSuccess, errs
}

// pushNodeState is used for pushPullReq when we are
// transferring out node states
type FSMNodeState struct {
	Name        string
	Meta        []byte
	Incarnation uint32
	State       NodeStateType
}

// sendAndReceiveState is used to initiate a push/pull over a stream with a
// remote host.
func (m *FSM) sendAndReceiveState(n string, join bool) ([]FSMNodeState, []byte, error) {
	// Prepare the local node state
	m.nodeLock.RLock()
	localNodes := make([]FSMNodeState, len(m.nodes))
	for idx, n := range m.nodes {
		localNodes[idx].Name = n.Name
		localNodes[idx].Incarnation = n.Incarnation
		localNodes[idx].State = n.State
		localNodes[idx].Meta = n.Meta
	}
	m.nodeLock.RUnlock()

	state, err := m.actions.ExchangeState(n, localNodes, join)
	return state, nil, err
}

// mergeRemoteState is used to merge the remote state with our local state
func (m *FSM) mergeRemoteState(join bool, remoteNodes []FSMNodeState, userBuf []byte) error {
	// Invoke the merge delegate if any
	if join && m.Config.Merge != nil {
		nodes := make([]*FSMNode, len(remoteNodes))
		for idx, n := range remoteNodes {
			nodes[idx] = &FSMNode{
				Name:  n.Name,
				Meta:  n.Meta,
				State: n.State,
			}
		}
	}

	// Merge the membership state
	m.mergeState(remoteNodes)

	// Invoke the delegate for user state
	if userBuf != nil && m.Config.Delegate != nil {
		m.Config.Delegate.MergeRemoteState(userBuf, join)
	}
	return nil
}

func (m *FSM) ExchangeState(remote []FSMNodeState, join bool) ([]FSMNodeState, error) {
	// Prepare the local node state
	m.nodeLock.RLock()
	localNodes := make([]FSMNodeState, len(m.nodes))
	for idx, n := range m.nodes {
		localNodes[idx].Name = n.Name
		localNodes[idx].Incarnation = n.Incarnation
		localNodes[idx].State = n.State
		localNodes[idx].Meta = n.Meta
	}
	m.nodeLock.RUnlock()

	err := m.mergeRemoteState(join, remote, nil)
	if err != nil {
		return nil, err
	}

	return localNodes, nil
}

// mergeState is invoked by the network layer when we get a Push/Pull
// state transfer
func (m *FSM) mergeState(remote []FSMNodeState) {
	for _, r := range remote {
		switch r.State {
		case StateAlive:
			m.aliveNode(&r, nil, false)

		case StateLeft:
			d := dead{Incarnation: r.Incarnation, Node: r.Name, From: r.Name}
			m.deadNode(&d)
		case StateDead:
			// If the remote node believes a node is dead, we prefer to
			// suspect that node instead of declaring it dead instantly
			fallthrough
		case StateSuspect:
			s := suspect{Incarnation: r.Incarnation, Node: r.Name, From: m.Config.Name}
			m.suspectNode(&s)
		}
	}
}

// pushPullNode does a complete state exchange with a specific node.
func (m *FSM) pushPullNode(n string, join bool) error {
	defer metrics.MeasureSince([]string{"memberlist", "pushPullNode"}, time.Now())

	// Attempt to send and receive with the node
	remote, userState, err := m.sendAndReceiveState(n, join)
	if err != nil {
		return err
	}

	if err := m.mergeRemoteState(join, remote, userState); err != nil {
		return err
	}
	return nil
}

func (m *FSM) NodeAlive(info NodeInfo) error {
	m.aliveNode(&FSMNodeState{
		Name:        info.Node,
		Incarnation: info.Incarnation,
	}, nil, false)
	return nil
}

// aliveNode is invoked by the network layer when we get a message about a
// live node.
func (m *FSM) aliveNode(a *FSMNodeState, notify chan struct{}, bootstrap bool) {
	m.nodeLock.Lock()
	defer m.nodeLock.Unlock()
	state, ok := m.nodeMap[a.Name]

	// It is possible that during a Leave(), there is already an aliveMsg
	// in-queue to be processed but blocked by the locks above. If we let
	// that aliveMsg process, it'll cause us to re-join the cluster. This
	// ensures that we don't.
	if m.hasLeft() && a.Name == m.Config.Name {
		return
	}

	// Check if we've never seen this node before, and if not, then
	// store this node in our node map.
	var updatesNode bool
	if !ok {
		state = &fsmNodeState{
			FSMNode: FSMNode{
				Name: a.Name,
				Meta: a.Meta,
			},
			State: StateDead,
		}

		// Add to map
		m.nodeMap[a.Name] = state

		// Get a random offset. This is important to ensure
		// the failure detection bound is low on average. If all
		// nodes did an append, failure detection bound would be
		// very high.
		n := len(m.nodes)
		offset := randomOffset(n)

		// Add at the end and swap with the node at the offset
		m.nodes = append(m.nodes, state)
		m.nodes[offset], m.nodes[n] = m.nodes[n], m.nodes[offset]

		// Update numNodes after we've added a new node
		atomic.AddUint32(&m.numNodes, 1)
	}

	// Bail if the incarnation number is older, and this is not about us
	isLocalNode := state.Name == m.Config.Name
	if a.Incarnation <= state.Incarnation && !isLocalNode && !updatesNode {
		return
	}

	// Bail if strictly less and this is about us
	if a.Incarnation < state.Incarnation && isLocalNode {
		return
	}

	// Clear out any suspicion timer that may be in effect.
	delete(m.nodeTimers, a.Name)

	// Store the old state and meta data
	oldState := state.State
	oldMeta := state.Meta

	// If this is us we need to refute, otherwise re-broadcast
	if !bootstrap && isLocalNode {
		// If the Incarnation is the same, we need special handling, since it
		// possible for the following situation to happen:
		// 1) Start with configuration C, join cluster
		// 2) Hard fail / Kill / Shutdown
		// 3) Restart with configuration C', join cluster
		//
		// In this case, other nodes and the local node see the same incarnation,
		// but the values may not be the same. For this reason, we always
		// need to do an equality check for this Incarnation. In most cases,
		// we just ignore, but we may need to refute.
		//
		if a.Incarnation == state.Incarnation &&
			bytes.Equal(a.Meta, state.Meta) {
			return
		}
		m.refute(state, a.Incarnation)
		// m.Logger.Warn("Refuting an alive message for '%s' (%v:%d) meta:(%v VS %v), vsn:(%v VS %v)", a.Node, net.IP(a.Addr), a.Port, a.Meta, state.Meta, a.Vsn, versions)
	} else {
		m.broadcastAlive(a.Name, state, notify)

		// Update the state and incarnation number
		state.Incarnation = a.Incarnation
		state.Meta = a.Meta
		if state.State != StateAlive {
			state.State = StateAlive
			state.StateChange = time.Now()
		}
	}

	// Update metrics
	metrics.IncrCounter([]string{"memberlist", "msg", "alive"}, 1)

	// Notify the delegate of any relevant updates
	if m.Config.Events != nil {
		if oldState == StateDead || oldState == StateLeft {
			// if Dead/Left -> Alive, notify of join
			// m.Config.Events.NotifyJoin(&state.Node)

		} else if !bytes.Equal(oldMeta, state.Meta) {
			// if Meta changed, trigger an update notification
			// m.Config.Events.NotifyUpdate(&state.Node)
		}
	}
}
