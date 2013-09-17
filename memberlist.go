/*
The memberlist package is used to provide a lightweight gossip based
mechanism for node membership and failure detection. It is loosely
based on the SWIM paper (Scalable Weakly-consistent Infection-style
process group Membership protocol). There are a few notable differences,
including the uses of additional gossip (instead of purely piggybacking on
failure detection) and the addition of a state push/pull mechanism.

An independent gossip mechanism is used because it allows for changes to be propogated
more quickly, and also enables us to gossip at a different interval that we perform
failure checks. The gossip rate is tunable, and can be disabled.

A Push/Pull mechanism is also included because it allows new nodes to
get an almost complete member list upon joining. It also is used as
a periodic anti-entropy mechanism to ensure very high convergence rates.
The frequency of this can be adjusted to change the overhead, or disabled
entirely.

*/
package memberlist

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

type Config struct {
	Name             string        // Node name (FQDN)
	BindAddr         string        // Binding address
	UDPPort          int           // UDP port to listen on
	TCPPort          int           // TCP port to listen on
	TCPTimeout       time.Duration // TCP timeout
	IndirectChecks   int           // Number of indirect checks to use
	RetransmitMult   int           // Retransmits = RetransmitMult * log(N+1)
	SuspicionMult    int           // Suspicion time = SuspcicionMult * log(N+1) * Interval
	PushPullInterval time.Duration // How often we do a Push/Pull update
	RTT              time.Duration // 99% precentile of round-trip-time
	ProbeInterval    time.Duration // Failure probing interval length

	GossipNodes    int           // Number of nodes to gossip to per GossipInterval
	GossipInterval time.Duration // Gossip interval for non-piggyback messages (only if GossipNodes > 0)
}

type Memberlist struct {
	config   *Config
	shutdown bool
	leave    bool

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
	tickers    []*time.Ticker
	stopTick   chan struct{}
	probeIndex int

	ackLock     sync.Mutex
	ackHandlers map[uint32]*ackHandler

	broadcastLock sync.Mutex
	bcQueue       broadcasts
}

func DefaultConfig() *Config {
	hostname, _ := os.Hostname()
	return &Config{
		hostname,
		"0.0.0.0",
		7946,
		7946,
		10 * time.Second,      // Timeout after 10 seconds
		3,                     // Use 3 nodes for the indirect ping
		4,                     // Retransmit a message 4 * log(N+1) nodes
		5,                     // Suspect a node for 5 * log(N+1) * Interval
		30 * time.Second,      // Low frequency
		20 * time.Millisecond, // Reasonable RTT time for LAN
		1 * time.Second,       // Failure check every second

		3, // Gossip to 3 nodes
		200 * time.Millisecond, // Gossip more rapidly
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
		stopTick:    make(chan struct{}, 32),
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
	if err := m.setAlive(); err != nil {
		m.Shutdown()
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
	if err := m.setAlive(); err != nil {
		m.Shutdown()
		return nil, err
	}

	// Attempt to join any of them
	success := false
	for _, exist := range existing {
		addr, err := net.ResolveIPAddr("ip", exist)
		if err != nil {
			log.Printf("[ERR] Failed to resolve %s: %s", exist, err)
			continue
		}

		if err := m.pushPullNode(addr.IP); err != nil {
			log.Printf("[ERR] Failed to contact %s: %s", exist, err)
			continue
		}

		// Mark success, but keep exchanging with other hosts
		// to get more complete state data
		success = true
	}

	// Only continue on success
	if !success {
		m.Shutdown()
		return nil, fmt.Errorf("Failed to contact existing hosts")
	}

	// Schedule background work
	m.schedule()
	return m, nil
}

// setAlive is used to mark this node as being alive
func (m *Memberlist) setAlive() error {
	// Pick a private IP address
	var ipAddr []byte
	if m.config.BindAddr == "0.0.0.0" {
		// Get the interfaces
		addresses, err := net.InterfaceAddrs()
		if err != nil {
			return fmt.Errorf("Failed to get interface addresses! Err: %vn", err)
		}

		// Find private IPv4 address
		for _, addr := range addresses {
			ip, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip.IP.To4() == nil {
				continue
			}
			if !isPrivateIP(ip.IP.String()) {
				continue
			}
			ipAddr = ip.IP
			break
		}

		// Failed to find private IP, use loopback
		if ipAddr == nil {
			ipAddr = []byte{127, 0, 0, 1}
		}

	} else {
		addr := m.tcpListener.Addr().(*net.TCPAddr)
		ipAddr = addr.IP
	}
	a := alive{
		Incarnation: m.nextIncarnation(),
		Node:        m.config.Name,
		Addr:        ipAddr,
	}
	m.aliveNode(&a)
	return nil
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
// the memberlist background maintenence. This should be followed
// by a Shutdown(). Note that this just enqueues the message,
// some time should be allowed for it to propogate.
func (m *Memberlist) Leave() error {
	m.leave = true
	d := dead{Incarnation: m.incarnation, Node: m.config.Name}
	m.deadNode(&d)
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
