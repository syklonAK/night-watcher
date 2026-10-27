//go:build !windows

package network

import "syscall"

const tcpMaxSeg = syscall.TCP_MAXSEG

func setSocketInt(fd uintptr, level, opt, value int) error {
	return syscall.SetsockoptInt(int(fd), level, opt, value)
}
