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
	"net"
	"os"
	"sync"
	"time"
)

type Delegate interface {
	// NodeMeta is used to retrieve meta-data about the current node
	// when broadcasting an alive message. It's length is limited to
	// the given byte size.
	NodeMeta(limit int) []byte

	// NotifyMsg is called when a user-data message is received.
	// This should not block
	NotifyMsg([]byte)

	// GetBroadcasts is called when user data messages can be broadcast.
	// It can return a list of buffers to send. Each buffer should assume an
	// overhead as provided with a limit on the total byte size allowed.
	GetBroadcasts(overhead, limit int) [][]byte

	// LocalState is used for a TCP Push/Pull. This is sent to
	// the remote side as well as membership information
	LocalState() []byte

	// MergeRemoteState is invoked after a TCP Push/Pull. This is the
	// state received from the remote side.
	MergeRemoteState([]byte)
}

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

	JoinCh       chan<- *Node
	LeaveCh      chan<- *Node
	UserDelegate Delegate // Delegate for user data
}

type Memberlist struct {
	config         *Config
	shutdown       bool
	leave          bool
	leaveBroadcast chan struct{}

	udpListener *net.UDPConn
	tcpListener *net.TCPListener

	sequenceNum uint32 // Local sequence number
	incarnation uint32 // Local incarnation number

	nodeLock sync.RWMutex
	nodes    []*nodeState          // Known nodes
	nodeMap  map[string]*nodeState // Maps Addr.String() -> NodeState

	tickerLock sync.Mutex
	tickers    []*time.Ticker
	stopTick   chan struct{}
	probeIndex int

	ackLock     sync.Mutex
	ackHandlers map[uint32]*ackHandler

	broadcasts *TransmitLimitedQueue
}

// DefaultConfig is used to return a default sane set of configurations for
// Memberlist. It uses the hostname as the node name, and otherwise sets conservative
// values that are sane for most LAN environments.
func DefaultConfig() *Config {
	hostname, _ := os.Hostname()
	return &Config{
		Name:             hostname,
		BindAddr:         "0.0.0.0",
		UDPPort:          7946,
		TCPPort:          7946,
		TCPTimeout:       10 * time.Second,      // Timeout after 10 seconds
		IndirectChecks:   3,                     // Use 3 nodes for the indirect ping
		RetransmitMult:   4,                     // Retransmit a message 4 * log(N+1) nodes
		SuspicionMult:    5,                     // Suspect a node for 5 * log(N+1) * Interval
		PushPullInterval: 30 * time.Second,      // Low frequency
		RTT:              20 * time.Millisecond, // Reasonable RTT time for LAN
		ProbeInterval:    1 * time.Second,       // Failure check every second

		GossipNodes:    3,                      // Gossip to 3 nodes
		GossipInterval: 200 * time.Millisecond, // Gossip more rapidly

		JoinCh:       nil,
		LeaveCh:      nil,
		UserDelegate: nil,
	}
}

// newMemberlist creates the network listeners.
// Does not schedule execution of background maintenence.
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

	// Set the UDP receive window size
	setUDPRecvBuf(udpLn.(*net.UDPConn))

	m := &Memberlist{
		config:         conf,
		leaveBroadcast: make(chan struct{}, 1),
		udpListener:    udpLn.(*net.UDPConn),
		tcpListener:    tcpLn.(*net.TCPListener),
		nodeMap:        make(map[string]*nodeState),
		ackHandlers:    make(map[uint32]*ackHandler),
		broadcasts:     &TransmitLimitedQueue{RetransmitMult: conf.RetransmitMult},
	}
	m.broadcasts.NumNodes = func() int { return len(m.nodes) }
	go m.tcpListen()
	go m.udpListen()
	return m, nil
}

// Create will start memberlist but does not connect to any other node
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

// Join is used to take an existing Memberlist and attempt to join
// a cluster by contacting all the given hosts. Returns the number successfully,
// contacted and an error if none could be reached.
func (m *Memberlist) Join(existing []string) (int, error) {
	// Attempt to join any of them
	numSuccess := 0
	var retErr error
	for _, exist := range existing {
		addr, err := net.ResolveIPAddr("ip", exist)
		if err != nil {
			retErr = err
			continue
		}

		if err := m.pushPullNode(addr.IP); err != nil {
			retErr = err
			continue
		}

		numSuccess++
	}

	if numSuccess > 0 {
		retErr = nil
	}
	return numSuccess, retErr
}

// setAlive is used to mark this node as being alive. This is the same
// as if we received an alive notification our own network channel for
// ourself.
func (m *Memberlist) setAlive() error {
	// Pick a private IP address
	var ipAddr []byte
	if m.config.BindAddr == "0.0.0.0" {
		// We're not bound to a specific IP, so let's list the interfaces
		// on this machine and use the first private IP we find.
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
		// Use the IP that we're bound to.
		addr := m.tcpListener.Addr().(*net.TCPAddr)
		ipAddr = addr.IP
	}

	// Get the node meta data
	var meta []byte
	if m.config.UserDelegate != nil {
		meta = m.config.UserDelegate.NodeMeta(metaMaxSize)
		if len(meta) > metaMaxSize {
			panic("Node meta data provided is longer than the limit")
		}
	}

	a := alive{
		Incarnation: m.nextIncarnation(),
		Node:        m.config.Name,
		Addr:        ipAddr,
		Meta:        meta,
	}
	m.aliveNode(&a)
	return nil
}

// Members is used to return a list of all known live nodes
func (m *Memberlist) Members() []*Node {
	m.nodeLock.RLock()
	defer m.nodeLock.RUnlock()
	nodes := make([]*Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		if n.State != stateDead {
			nodes = append(nodes, &n.Node)
		}
	}
	return nodes
}

// NumMembers provides an efficient way to determine
// the number of alive members
func (m *Memberlist) NumMembers() (alive int) {
	m.nodeLock.RLock()
	defer m.nodeLock.RUnlock()
	for _, n := range m.nodes {
		if n.State != stateDead {
			alive++
		}
	}
	return
}

// Leave will broadcast a leave message but will not shutdown
// the memberlist background maintenence. This should be followed
// by a Shutdown(). This will block until the death message has
// finished gossiping out.
func (m *Memberlist) Leave() error {
	m.leave = true
	d := dead{Incarnation: m.incarnation, Node: m.config.Name}
	m.deadNode(&d)

	// Block until the broadcast goes out
	if len(m.nodes) > 1 {
		<-m.leaveBroadcast
	}
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
