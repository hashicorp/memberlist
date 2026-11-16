// Copyright IBM Corp. 2013, 2026
// SPDX-License-Identifier: MPL-2.0

package memberlist

// NodeSelectionDelegate filters and prioritizes nodes for gossip and push/pull
// operations. It does not affect probes.
type NodeSelectionDelegate interface {
	// SelectNodes returns the candidate nodes and an optional preferred node.
	// The preferred node is selected first if it is not excluded by the caller.
	SelectNodes([]*NodeState) (selected []*NodeState, preferred *NodeState)
}
