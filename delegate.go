package memberlist

// Constant event types
type NodeEventType int

const (
	NodeJoin NodeEventType = iota
	NodeLeave
)

// ChannelEventDelegate is used to enable an application to receive
// events about joins and leaves over a channel instead of a direct
// function call
type ChannelEventDelegate struct {
	Ch chan<- NodeEvent
}

// NodeEvent is used to represent a node event
type NodeEvent struct {
	Event NodeEventType
	Node  *Node
}

func (c *ChannelEventDelegate) NotifyJoin(n *Node) {
	select {
	case c.Ch <- NodeEvent{NodeJoin, n}:
	default:
	}
}

func (c *ChannelEventDelegate) NotifyLeave(n *Node) {
	select {
	case c.Ch <- NodeEvent{NodeLeave, n}:
	default:
	}
}
