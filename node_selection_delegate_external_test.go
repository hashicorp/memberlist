// Copyright IBM Corp. 2013, 2026
// SPDX-License-Identifier: MPL-2.0

package memberlist_test

import (
	"testing"

	"github.com/hashicorp/memberlist"
)

type nodeSelectionDelegateFunc func([]*memberlist.NodeState) ([]*memberlist.NodeState, *memberlist.NodeState)

func (f nodeSelectionDelegateFunc) SelectNodes(nodes []*memberlist.NodeState) ([]*memberlist.NodeState, *memberlist.NodeState) {
	return f(nodes)
}

func TestNodeSelectionDelegateExternalAPI(t *testing.T) {
	nodes := []*memberlist.NodeState{{Node: memberlist.Node{Name: "node"}}}
	delegate := nodeSelectionDelegateFunc(func(nodes []*memberlist.NodeState) ([]*memberlist.NodeState, *memberlist.NodeState) {
		return nodes, nodes[0]
	})

	var _ memberlist.NodeSelectionDelegate = delegate
	selected, preferred := delegate.SelectNodes(nodes)
	if len(selected) != 1 || preferred.Name != "node" {
		t.Fatal("unexpected node selection result")
	}
}
