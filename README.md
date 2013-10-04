# memberlist

memberlist is a [Go](http://www.golang.org) library that manages cluster
membership and member failure detection using a gossip based protocol.

The use cases for such a library are far-reaching: all distributed systems
require membership, and memberlist is a re-usable solution to managing
cluster membership and node failure detection.

memberlist is eventually consistent but converges quickly on average.
The speed at which it converges can be heavily tuned via various knobs
on the protocol. Node failures are detected and network partitions are partially
tolerated by attempting to communicate to potentially dead nodes through
multiple routes.

## Protocol

memberlist is based on ["SWIM: Scalable Weakly-consistent Infection-style Process Group Membership Protocol"](http://www.cs.cornell.edu/~asdas/research/dsn02-swim.pdf),
with a few minor adaptations, mostly to increase propogation speed and
convergence rate.

A high level overview of the memberlist protocol (based on SWIM) is
described below, but for details please read the full
[SWIM paper](http://www.cs.cornell.edu/~asdas/research/dsn02-swim.pdf)
followed by the memberlist source. We welcome any questions related
to the protocol on our issue tracker.

memberlist begins by joining an existing cluster or starting a new
cluster. If starting a new cluster, additional nodes are expected to join
it. New nodes in an existing cluster must be given the address of at
least one existing member in order to join the cluster. The new member
does a full state sync with the existing member over TCP and begins gossiping its
existence to the cluster.

Gossip is done over UDP to a with a configurable but fixed fanout and interval.
This ensures that network usage is constant with regards to number of nodes, as opposed to
exponential growth that can occur with traditional heartbeat mechanisms.
Complete state exchanges with a random node are done periodically over
TCP, but much less often than gossip messages. This increases the likelihood
that the membership list converges properly since the full state is exchanged
and merged. The interval between full state exchanges is configurable or can
be disabled entirely.

Failure detection is done by periodic random probing using a configurable interval.
If the node fails to ack within a reasonable time (typically some multiple
of RTT), then an indirect probe is attempted. An indirect probe asks a
configurable number of random nodes to probe the same node, in case there
are network issues causing our own node to fail the probe. If both our
probe and the indirect probes fail within a reasonable time, then the
node is marked "suspicious" and this knowledge is gossiped to the cluster.
A suspicious node is still considered a member of cluster. If no member
of the cluster disputes the suspicion within a configurable period of
time, the node is finally considered dead, and this state is then gossiped
to the cluster.

As mentioned earlier, this is a brief and incomplete description of the
protocol. For a better idea, please read the
[SWIM paper](http://www.cs.cornell.edu/~asdas/research/dsn02-swim.pdf)
in its entirety, along with the memberlist source code.
