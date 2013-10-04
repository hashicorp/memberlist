package memberlist

// ChannelEventDelegate is used to enable an application to receive
// events about joins and leaves over a channel instead of a direct
// function call
type ChannelEventDelegate struct {
	JoinCh  chan<- *Node
	LeaveCh chan<- *Node
}

func (c *ChannelEventDelegate) NotifyJoin(n *Node) {
	select {
	case c.JoinCh <- n:
	default:
	}
}

func (c *ChannelEventDelegate) NotifyLeave(n *Node) {
	select {
	case c.LeaveCh <- n:
	default:
	}
}
