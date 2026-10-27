package tnet

import (
	"net"

	"github.com/syklonAK/night-watcher/internal/packet/engine/conf"
)

type Listener interface {
	Accept() (Conn, error)
	Close() error
	Addr() net.Addr
	SetClientTCPF(addr net.Addr, f []conf.TCPF)
	DeleteClientTCPF(addr net.Addr)
}
