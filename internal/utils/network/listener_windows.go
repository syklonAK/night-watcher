package network

import (
	"context"
	"net"
	"time"
)

// Windows uses Go's socket defaults; Linux-only MSS/congestion tuning is skipped.
func ListenWithBuffers(network, address string, rcvBufSize, sndBufSize, mss int, keepAlivePeriod time.Duration, disNodelay bool) (net.Listener, error) {
	c := net.ListenConfig{KeepAliveConfig: net.KeepAliveConfig{Enable: true, Interval: keepAlivePeriod, Count: 9}}
	ln, e := c.Listen(context.Background(), network, address)
	if e != nil {
		return nil, e
	}
	return &windowsListener{Listener: ln, rcv: rcvBufSize, snd: sndBufSize, disableNoDelay: disNodelay}, nil
}

type windowsListener struct {
	net.Listener
	rcv, snd       int
	disableNoDelay bool
}

func (l *windowsListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e != nil {
		return nil, e
	}
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(!l.disableNoDelay)
		if PinTCPBuffers() {
			if l.rcv > 0 {
				_ = tcp.SetReadBuffer(l.rcv)
			}
			if l.snd > 0 {
				_ = tcp.SetWriteBuffer(l.snd)
			}
		}
	}
	return c, nil
}
