package memberlist

import (
	"net"

	hclog "github.com/hashicorp/go-hclog"
)

const unknownAddr = "<unknown address>"

type logger struct {
	hclog.Logger
}

func (l logger) fromAddress(addr net.Addr) logger {
	if addr == nil {
		return logger{Logger: l.With("from", unknownAddr)}
	}
	return logger{Logger: l.With("from", addr.String())}
}

func (l logger) fromConn(conn net.Conn) logger {
	if conn == nil {
		return l.fromAddress(nil)
	}
	return l.fromAddress(conn.RemoteAddr())
}
