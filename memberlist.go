/*
The memberlist package is used to provide a lightweight gossip based
mechanism for node membership and failure detection. It is loosely
based on the SWIM paper (Scalable Weakly-consistent Infection-style
process group Membership protocol). There are a few notable differences,
including the uses of additional fanout (instead of purely piggybacking on
failure detection) and the addition of a state push/pull mechanism.

A fanout mechanism is used because it allows for changes to be propogated
more quickly, and also enables us to dynamically increase the amount of
gossip in response to a surge of events. The fanout rate is provided as a tunable
parameter.

A Push/Pull mechanism is also included because it allows new nodes to
get an almost complete member list upon joining. It also is used as
a periodic anti-entropy mechanism to ensure very high convergence rates.
The frequency of this can be adjusted to change the overhead, or disabled
entirely.

*/
package memberlist

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

type Config struct {
	Name           string        // Node name (FQDN)
	BindAddr       string        // Binding address
	UDPPort        int           // UDP port to listen on
	TCPPort        int           // TCP port to listen on
	Fanout         int           // Number of nodes to publish to per round
	IndirectChecks int           // Number of indirect checks to use
	Retransmits    int           // Retransmit multiplier for messages
	SuspicionMult  int           // Suspicion time = SuspcicionMult * log(N+1) * Interval
	PushPullFreq   float32       // How often we do a Push/Pull update
	RTT            time.Duration // 99% precentile of round-trip-time
	Interval       time.Duration // Interval length
}

type Memberlist struct {
	config   *Config
	shutdown bool

	udpListener *net.UDPConn
	tcpListener *net.TCPListener

	notifyLock  sync.RWMutex
	notifyJoin  []chan<- *Node // Channels to notify on join
	notifyLeave []chan<- *Node // Channels to notify on leave

	sequenceNum uint32 // Local sequence number
	incarnation uint32 // Local incarnation number

	nodeLock sync.RWMutex
	nodes    []*NodeState          // Known nodes
	nodeMap  map[string]*NodeState // Maps Addr.String() -> NodeState

	tickerLock sync.Mutex
	ticker     *time.Ticker
	stopTick   chan struct{}
	tickCount  uint32

	ackLock     sync.Mutex
	ackHandlers map[uint32]*ackHandler
}

func DefaultConfig() *Config {
	hostname, _ := os.Hostname()
	return &Config{
		hostname,
		"0.0.0.0",
		7946,
		7946,
		3,    // Fanout to 3 nodes
		3,    // Use 3 nodes for the indirect ping
		4,    // Retransmit a message 4 * log(N+1) nodes
		5,    // Suspect a node for 5 * log(N+1) * Interval
		0.05, // 5% frequency for push/pull
		20 * time.Millisecond, // Reasonable RTT time for LAN
		1 * time.Second,       // Failure check every second
	}
}

// newMemberlist creates the network listeners.
// Does not schedule exeuction of background maintenence.
func newMemberlist(conf *Config) (*Memberlist, error) {
	tcpAddr := fmt.Sprintf("%s:%d", conf.BindAddr, conf.TCPPort)
	tcpLn, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return nil, fmt.Errorf("Failed to start TCP listener. Err: %s", err)
	}

	udpAddr := fmt.Sprintf("%s:%d", conf.BindAddr, conf.UDPPort)
	udpLn, err := net.ListenPacket("udp", udpAddr)
	if err != nil {
		tcpLn.Close()
		return nil, fmt.Errorf("Failed to start UDP listener. Err: %s", err)
	}

	m := &Memberlist{config: conf,
		udpListener: udpLn.(*net.UDPConn),
		tcpListener: tcpLn.(*net.TCPListener),
		nodeMap:     make(map[string]*NodeState),
		stopTick:    make(chan struct{}, 1),
		ackHandlers: make(map[uint32]*ackHandler),
	}
	go m.tcpListen()
	go m.udpListen()
	return m, nil
}

// Create will start memberlist and create a new gossip pool, but
// will not connect to an existing node. This should only be used
// for the first node in the cluster.
func Create(conf *Config) (*Memberlist, error) {
	m, err := newMemberlist(conf)
	if err != nil {
		return nil, err
	}
	m.schedule()
	return m, nil
}

// Join will start memberlist and perform an initial push/pull with
// all the given hosts. If none of the existing hosts could be contacted,
// the join will fail.
func Join(conf *Config, existing []string) (*Memberlist, error) {
	m, err := newMemberlist(conf)
	if err != nil {
		return nil, err
	}

	// TODO: Attempt to join...

	// Schedule background work
	m.schedule()
	return m, nil
}

// NotifyJoin is used to subscribe a channel to join events
func (m *Memberlist) NotifyJoin(ch chan<- *Node) {
	m.notifyLock.Lock()
	defer m.notifyLock.Unlock()

	// Bail if the channel is already subscribed
	if channelIndex(m.notifyJoin, ch) >= 0 {
		return
	}
	m.notifyJoin = append(m.notifyJoin, ch)
}

// NotifyLeave is used to subscribe a channel to leave events
func (m *Memberlist) NotifyLeave(ch chan<- *Node) {
	m.notifyLock.Lock()
	defer m.notifyLock.Unlock()

	// Bail if the channel is already subscribed
	if channelIndex(m.notifyLeave, ch) >= 0 {
		return
	}
	m.notifyLeave = append(m.notifyLeave, ch)
}

// Stop is used to unsubscribe a channel from any notifications
func (m *Memberlist) Stop(ch chan<- *Node) {
	m.notifyLock.Lock()
	defer m.notifyLock.Unlock()

	idx := channelIndex(m.notifyJoin, ch)
	if idx >= 0 {
		m.notifyJoin = channelDelete(m.notifyJoin, idx)
	}

	idx = channelIndex(m.notifyLeave, ch)
	if idx >= 0 {
		m.notifyLeave = channelDelete(m.notifyLeave, idx)
	}
}

// Members is used to return a list of all known live nodes
func (m *Memberlist) Members() []*Node {
	nodes := make([]*Node, 0, len(m.nodes))
	m.nodeLock.RLock()
	defer m.nodeLock.RUnlock()
	for _, n := range m.nodes {
		if n.State != StateDead {
			nodes = append(nodes, &n.Node)
		}
	}
	return nodes
}

// Leave will broadcast a leave message but will not shutdown
// the memberlist background maintenence. This should be followed/
// by a Shutdown().
func (m *Memberlist) Leave() error {
	return nil
}

// Shutdown will stop the memberlist background maintenence
// but will not broadcast a leave message prior. If no prior
// leave was issued, other nodes will detect this as a failure.
func (m *Memberlist) Shutdown() error {
	m.shutdown = true
	m.deschedule()
	m.udpListener.Close()
	m.tcpListener.Close()
	return nil
}
