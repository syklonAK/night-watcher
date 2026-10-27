//go:build linux

package edge

import (
	"golang.org/x/sys/unix"
	"net"
)

func setDontFragment(c net.PacketConn, v6 bool) error {
	rc, e := c.(*net.IPConn).SyscallConn()
	if e != nil {
		return e
	}
	var se error
	e = rc.Control(func(fd uintptr) {
		if v6 {
			se = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_DONTFRAG, 1)
		} else {
			se = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO)
		}
	})
	if e != nil {
		return e
	}
	return se
}
