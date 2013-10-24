/*
memberlist is a library that manages cluster
membership and member failure detection using a gossip based protocol.

The use cases for such a library are far-reaching: all distributed systems
require membership, and memberlist is a re-usable solution to managing
cluster membership and node failure detection.

memberlist is eventually consistent but converges quickly on average.
The speed at which it converges can be heavily tuned via various knobs
on the protocol. Node failures are detected and network partitions are partially
tolerated by attempting to communicate to potentially dead nodes through
multiple routes.
*/
package memberlist

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

type Config struct {
	// The name of this node. This must be unique in the cluster.
	Name string

	// Configuration related to what address to bind to and ports to
	// listen on. The ports used must match every node in the cluster,
	// since they'll be used for connecting as well as listening.
	BindAddr string
	UDPPort  int
	TCPPort  int

	// ProtocolVersion is the configured protocol version that we
	// will _speak_. This must be between ProtocolVersionMin and
	// ProtocolVersionMax.
	ProtocolVersion uint8

	// TCPTimeout is the timeout for establishing a TCP connection with
	// a remote node for a full state sync.
	TCPTimeout time.Duration

	// IndirectChecks is the number of nodes that will be asked to perform
	// an indirect probe of a node in the case a direct probe fails. Memberlist
	// waits for an ack from any single indirect node, so increasing this
	// number will increase the likelihood that an indirect probe will succeed
	// at the expense of bandwidth.
	IndirectChecks int

	// RetransmitMult is the multiplier for the number of retransmissions
	// that are attempted for messages broadcasted over gossip. The actual
	// count of retransmissions is calculated using the formula:
	//
	//   Retransmits = RetransmitMult * log(N+1)
	//
	// This allows the retransmits to scale properly with cluster size. The
	// higher the multiplier, the more likely a failed broadcast is to converge
	// at the expense of increased bandwidth.
	RetransmitMult int

	// SuspicionMult is the multiplier for determining the time an
	// inaccessible node is considered suspect before declaring it dead.
	// The actual timeout is calculated using the formula:
	//
	//   SuspicionTimeout = SuspicionMult * log(N+1) * ProbeInterval
	//
	// This allows the timeout to scale properly with expected propagation
	// delay with a larger cluster size. The higher the multiplier, the longer
	// an inaccessible node is considered part of the cluster before declaring
	// it dead, giving that suspect node more time to refute if it is indeed
	// still alive.
	SuspicionMult int

	// PushPullInterval is the interval between complete state syncs.
	// Complete state syncs are done with a single node over TCP and are
	// quite expensive relative to standard gossiped messages. Setting this
	// to zero will disable state push/pull syncs completely.
	//
	// Setting this interval lower (more frequent) will increase convergence
	// speeds across larger clusters at the expense of increased bandwidth
	// usage.
	PushPullInterval time.Duration

	// ProbeInterval and ProbeTimeout are used to configure probing
	// behavior for memberlist.
	//
	// ProbeInterval is the interval between random node probes. Setting
	// this lower (more frequent) will cause the memberlist cluster to detect
	// failed nodes more quickly at the expense of increased bandwidth usage.
	//
	// ProbeTimeout is the timeout to wait for an ack from a probed node
	// before assuming it is unhealthy. This should be set to 99-percentile
	// of RTT (round-trip time) on your network.
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration

	// GossipInterval and GossipNodes are used to configure the gossip
	// behavior of memberlist.
	//
	// GossipInterval is the interval between sending messages that need
	// to be gossiped that haven't been able to piggyback on probing messages.
	// If this is set to zero, non-piggyback gossip is disabled. By lowering
	// this value (more frequent) gossip messages are propagated across
	// the cluster more quickly at the expense of increased bandwidth.
	//
	// GossipNodes is the number of random nodes to send gossip messages to
	// per GossipInterval. Increasing this number causes the gossip messages
	// to propagate across the cluster more quickly at the expense of
	// increased bandwidth.
	GossipInterval time.Duration
	GossipNodes    int

	// EnableCompression is used to control message compression. This can
	// be used to reduce bandwidth usage at the cost of slightly more CPU
	// utilization.
	EnableCompression bool

	// SecretKey is provided if message level encryption and verification
	// are to be used. The key is passed through a Key Derivation Function
	// (PBKDF2) to ensure suitability. This key is also used for an HMAC-SHA1
	// to provide message integrity.
	SecretKey string

	// Delegate and Events are delegates for receiving and providing
	// data to memberlist via callback mechanisms. For Delegate, see
	// the Delegate interface. For Events, see the EventDelegate interface.
	Delegate Delegate
	Events   EventDelegate

	// LogOutput is the writer where logs should be sent. If this is not
	// set, logging will go to stderr by default.
	LogOutput io.Writer
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

	startStopLock sync.Mutex

	logger *log.Logger
}

// DefaultConfig returns a sane set of configurations for Memberlist.
// It uses the hostname as the node name, and otherwise sets very conservative
// values that are sane for most LAN environments. The default configuration
// errs on the side on the side of caution, choosing values that are optimized
// for higher convergence at the cost of higher bandwidth usage. Regardless,
// these values are a good starting point when getting started with memberlist.
func DefaultConfig() *Config {
	hostname, _ := os.Hostname()
	return &Config{
		Name:             hostname,
		BindAddr:         "0.0.0.0",
		UDPPort:          7946,
		TCPPort:          7946,
		ProtocolVersion:  ProtocolVersionMin,
		TCPTimeout:       10 * time.Second,       // Timeout after 10 seconds
		IndirectChecks:   3,                      // Use 3 nodes for the indirect ping
		RetransmitMult:   4,                      // Retransmit a message 4 * log(N+1) nodes
		SuspicionMult:    5,                      // Suspect a node for 5 * log(N+1) * Interval
		PushPullInterval: 30 * time.Second,       // Low frequency
		ProbeTimeout:     500 * time.Millisecond, // Reasonable RTT time for LAN
		ProbeInterval:    1 * time.Second,        // Failure check every second

		GossipNodes:    3,                      // Gossip to 3 nodes
		GossipInterval: 200 * time.Millisecond, // Gossip more rapidly

		EnableCompression: true, // Enable compression by default
		SecretKey:         "",
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

	if conf.LogOutput == nil {
		conf.LogOutput = os.Stderr
	}

	m := &Memberlist{
		config:         conf,
		leaveBroadcast: make(chan struct{}, 1),
		udpListener:    udpLn.(*net.UDPConn),
		tcpListener:    tcpLn.(*net.TCPListener),
		nodeMap:        make(map[string]*nodeState),
		ackHandlers:    make(map[uint32]*ackHandler),
		broadcasts:     &TransmitLimitedQueue{RetransmitMult: conf.RetransmitMult},
		logger:         log.New(conf.LogOutput, "", log.LstdFlags),
	}
	m.broadcasts.NumNodes = func() int { return len(m.nodes) }
	go m.tcpListen()
	go m.udpListen()
	return m, nil
}

// Create will create a new Memberlist using the given configuration.
// This will not connect to any other node (see Join) yet, but will start
// all the listeners to allow other nodes to join this memberlist.
// After creating a Memberlist, the configuration given should not be
// modified by the user anymore.
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

// Join is used to take an existing Memberlist and attempt to join a cluster
// by contacting all the given hosts and performing a state sync. Initially,
// the Memberlist only contains our own state, so doing this will cause
// remote nodes to become aware of the existence of this node, effectively
// joining the cluster.
//
// This returns the number of hosts successfully contacted and an error if
// none could be reached. If an error is returned, the node did not successfully
// join the cluster.
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
	if m.config.Delegate != nil {
		meta = m.config.Delegate.NodeMeta(metaMaxSize)
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

// Members returns a list of all known live nodes. The node structures
// returned must not be modified. If you wish to modify a Node, make a
// copy first.
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

// NumMembers returns the number of alive nodes currently known. Between
// the time of calling this and calling Members, the number of alive nodes
// may have changed, so this shouldn't be used to determine how many
// members will be returned by Members.
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

// Leave will broadcast a leave message but will not shutdown the background
// listeners, meaning the node will continue participating in gossip and state
// updates.
//
// This will block until the leave message is successfully broadcasted to
// a member of the cluster, if any exist or until a specified timeout
// is reached.
//
// This method is safe to call multiple times, but must not be called
// after the cluster is already shut down.
func (m *Memberlist) Leave(timeout time.Duration) error {
	m.startStopLock.Lock()
	defer m.startStopLock.Unlock()

	if m.shutdown {
		panic("leave after shutdown")
	}

	if !m.leave {
		m.leave = true

		state, ok := m.nodeMap[m.config.Name]
		if !ok {
			m.logger.Println("[WARN] Leave but we're not in the node map.")
			return nil
		}

		d := dead{
			Incarnation: state.Incarnation,
			Node:        state.Name,
		}
		m.deadNode(&d)

		// Check for any other alive node
		anyAlive := false
		for _, n := range m.nodes {
			if n.State != stateDead {
				anyAlive = true
				break
			}
		}

		// Block until the broadcast goes out
		if anyAlive {
			var timeoutCh <-chan time.Time
			if timeout > 0 {
				timeoutCh = time.After(timeout)
			}
			select {
			case <-m.leaveBroadcast:
			case <-timeoutCh:
				return fmt.Errorf("timeout waiting for leave broadcast")
			}
		}
	}

	return nil
}

// Shutdown will stop any background maintanence of network activity
// for this memberlist, causing it to appear "dead". A leave message
// will not be broadcasted prior, so the cluster being left will have
// to detect this node's shutdown using probing. If you wish to more
// gracefully exit the cluster, call Leave prior to shutting down.
//
// This method is safe to call multiple times.
func (m *Memberlist) Shutdown() error {
	m.startStopLock.Lock()
	defer m.startStopLock.Unlock()

	if !m.shutdown {
		m.shutdown = true
		m.deschedule()
		m.udpListener.Close()
		m.tcpListener.Close()
	}

	return nil
}
