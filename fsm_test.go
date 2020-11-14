package memberlist

import (
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nodeOp struct {
	dest string
	info NodeInfo
}

type testActions struct {
	joined    bool
	requested string

	other *FSM

	alives []nodeOp

	pinged      string
	forIndirect string

	probeFail         bool
	probeNoInline     bool
	probeFailIndirect bool

	suspects []nodeOp
	deaths   []nodeOp
}

func (t *testActions) ExchangeState(n string, local []FSMNodeState, join bool) ([]FSMNodeState, error) {
	t.joined = true
	t.requested = n

	return t.other.ExchangeState(local, join)
}

func (t *testActions) NodeAlive(n string, info NodeInfo) error {
	t.alives = append(t.alives, nodeOp{n, info})
	return nil
}

func (t *testActions) NodeSuspect(n string, info NodeInfo) error {
	t.suspects = append(t.suspects, nodeOp{n, info})
	return nil
}

func (t *testActions) NodeDead(n string, info NodeInfo) error {
	t.deaths = append(t.deaths, nodeOp{n, info})
	return nil
}

func (t *testActions) NodePing(node string, seq uint32) (time.Duration, *InlineAck, error) {
	if t.probeFail {
		return 0, nil, fmt.Errorf("ping failed: %w", ErrFailedRemote)
	}

	t.pinged = node

	if t.probeNoInline {
		return 100 * time.Millisecond, nil, nil
	}

	return 100 * time.Millisecond, &InlineAck{node}, nil
}

func (t *testActions) NodeAck(node string, seq uint32) error {
	return nil
}

func (t *testActions) NodeNack(node string, seq uint32) error {
	return nil
}

func (t *testActions) NodeIndirectPing(node, target string, seq uint32) (time.Duration, *InlineAck, error) {
	if t.probeFailIndirect {
		return 0, nil, fmt.Errorf("ping failed: %w", ErrFailedRemote)
	}

	t.pinged = node
	t.forIndirect = target

	return 100 * time.Millisecond, &InlineAck{target}, nil
}

func TestFSM(t *testing.T) {
	l := hclog.L()

	t.Run("includes itself as a member", func(t *testing.T) {
		config := DefaultLocalConfig()
		config.Name = "a"

		m, err := NewFSM(l, nil, config)
		require.NoError(t, err)

		require.Equal(t, 1, m.NumMembers())

		n := m.Members()[0]

		assert.Equal(t, "a", n.Name)
	})

	t.Run("joining fsms together shows each as members", func(t *testing.T) {
		aconfig := DefaultLocalConfig()
		aconfig.Name = "a"

		a, err := NewFSM(l, nil, aconfig)
		require.NoError(t, err)

		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"

		var bactions testActions
		bactions.other = a

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		n, err := b.Join([]string{"a"})
		require.NoError(t, err)

		assert.Equal(t, 1, n)

		require.Equal(t, 2, b.NumMembers())

		node := b.Members()[0]
		assert.Equal(t, "a", node.Name)

		require.Equal(t, 2, a.NumMembers())

		node = a.Members()[0]
		assert.Equal(t, "b", node.Name)
	})

	t.Run("gossips to other nodes upon request", func(t *testing.T) {
		aconfig := DefaultLocalConfig()
		aconfig.Name = "a"

		a, err := NewFSM(l, nil, aconfig)
		require.NoError(t, err)

		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"

		var bactions testActions
		bactions.other = a

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		assert.Equal(t, 1, b.broadcasts.NumQueued())
		assert.Equal(t, 1, a.broadcasts.NumQueued())

		_, err = b.Join([]string{"a"})
		require.NoError(t, err)

		assert.Equal(t, 2, b.broadcasts.NumQueued())
		assert.Equal(t, 2, a.broadcasts.NumQueued())

		b.gossip()

		require.Equal(t, 2, len(bactions.alives))

		alive := bactions.alives[0]

		assert.Equal(t, "a", alive.dest)
		assert.Equal(t, "a", alive.info.Node)

		alive = bactions.alives[1]

		assert.Equal(t, "a", alive.dest)
		assert.Equal(t, "b", alive.info.Node)
	})

	t.Run("tracks a new node based on NodeAlive", func(t *testing.T) {
		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"

		var bactions testActions

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		err = b.NodeAlive(NodeInfo{
			Node:        "a",
			Incarnation: 1,
		})
		require.NoError(t, err)

		assert.Equal(t, 2, b.NumMembers())

		members := b.Members()
		require.Equal(t, 2, len(members))

		assert.Equal(t, "a", members[0].Name)
		assert.Equal(t, "b", members[1].Name)
	})

	t.Run("probes nodes by sending pings and tracking acks", func(t *testing.T) {
		aconfig := DefaultLocalConfig()
		aconfig.Name = "a"

		a, err := NewFSM(l, nil, aconfig)
		require.NoError(t, err)

		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"

		var bactions testActions
		bactions.other = a

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		_, err = b.Join([]string{"a"})
		require.NoError(t, err)

		ns := b.nodeMap["a"]

		b.probeNode(ns)

		assert.Equal(t, "a", bactions.pinged)

		assert.Equal(t, 0, len(b.ackHandlers))
	})

	t.Run("attempts indirect ping on failed probe", func(t *testing.T) {
		aconfig := DefaultLocalConfig()
		aconfig.Name = "a"

		a, err := NewFSM(l, nil, aconfig)
		require.NoError(t, err)

		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"

		var bactions testActions
		bactions.other = a

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		_, err = b.Join([]string{"a"})
		require.NoError(t, err)

		// bring C into the mix to allow for an indirect ping
		b.NodeAlive(NodeInfo{Node: "c", Incarnation: 1})

		ns := b.nodeMap["a"]

		bactions.probeFail = true

		b.probeNode(ns)

		assert.Equal(t, "c", bactions.pinged)
		assert.Equal(t, "a", bactions.forIndirect)

		assert.Equal(t, StateAlive, ns.State)
	})

	t.Run("attempts indirect ping on no ack", func(t *testing.T) {
		aconfig := DefaultLocalConfig()
		aconfig.Name = "a"

		a, err := NewFSM(l, nil, aconfig)
		require.NoError(t, err)

		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"

		var bactions testActions
		bactions.other = a

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		_, err = b.Join([]string{"a"})
		require.NoError(t, err)

		// bring C into the mix to allow for an indirect ping
		b.NodeAlive(NodeInfo{Node: "c", Incarnation: 1})

		ns := b.nodeMap["a"]

		bactions.probeNoInline = true

		b.probeNode(ns)

		assert.Equal(t, "c", bactions.pinged)
		assert.Equal(t, "a", bactions.forIndirect)

		assert.Equal(t, StateAlive, ns.State)
	})

	t.Run("sets to suspect when indirect fails", func(t *testing.T) {
		aconfig := DefaultLocalConfig()
		aconfig.Name = "a"

		a, err := NewFSM(l, nil, aconfig)
		require.NoError(t, err)

		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"
		bconfig.SuspicionMult = 1

		var bactions testActions
		bactions.other = a

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		_, err = b.Join([]string{"a"})
		require.NoError(t, err)

		// bring C into the mix to allow for an indirect ping
		b.NodeAlive(NodeInfo{Node: "c", Incarnation: 1})

		ns := b.nodeMap["a"]

		bactions.probeNoInline = true
		bactions.probeFailIndirect = true

		b.probeNode(ns)

		assert.Equal(t, StateSuspect, ns.State)

		b.gossip()

		assert.Equal(t, "a", bactions.suspects[0].info.Node)

		// To allow the suspicion timer to expire
		time.Sleep(2 * time.Second)

		b.nodeLock.Lock()

		assert.Equal(t, StateDead, ns.State)

		b.nodeLock.Unlock()
		b.gossip()

		assert.Equal(t, "a", bactions.deaths[0].info.Node)
	})

	t.Run("alive message resets suspect timers", func(t *testing.T) {
		aconfig := DefaultLocalConfig()
		aconfig.Name = "a"

		a, err := NewFSM(l, nil, aconfig)
		require.NoError(t, err)

		bconfig := DefaultLocalConfig()
		bconfig.Name = "b"
		bconfig.SuspicionMult = 1

		var bactions testActions
		bactions.other = a

		b, err := NewFSM(l, &bactions, bconfig)
		require.NoError(t, err)

		_, err = b.Join([]string{"a"})
		require.NoError(t, err)

		// bring C into the mix to allow for an indirect ping
		b.NodeAlive(NodeInfo{Node: "c", Incarnation: 1})

		ns := b.nodeMap["a"]

		bactions.probeNoInline = true
		bactions.probeFailIndirect = true

		b.probeNode(ns)

		assert.Equal(t, StateSuspect, ns.State)

		b.gossip()

		assert.Equal(t, "a", bactions.suspects[0].info.Node)

		err = b.NodeAlive(NodeInfo{Node: "a", Incarnation: ns.Incarnation + 1})
		require.NoError(t, err)

		// To allow the suspicion timer to expire
		time.Sleep(2 * time.Second)

		b.nodeLock.Lock()

		assert.Equal(t, StateAlive, ns.State)

		b.nodeLock.Unlock()
		b.gossip()

		assert.Equal(t, 0, len(bactions.deaths))
	})

}
