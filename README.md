# memberlist

memberlist is a [Go](http://www.golang.org) library that manages cluster
membership and member failure detection using a gossip based protocol.

memberlist is based on ["SWIM: Scalable Weakly-consistent Infection-style Process Group Membership Protocol"](http://www.cs.cornell.edu/~asdas/research/dsn02-swim.pdf),
with a few minor adaptations where we felt the protocol was too harsh or
simply incorrect in practice. As a high level overview, memberlist achieves
membership management and failure detection with the following features:

* Communication is done via gossip over UDP to a configurable but fixed
  number of nodes at a time.
