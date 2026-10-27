package network

import "syscall"

const tcpMaxSeg = -1

func setSocketInt(fd uintptr, level, opt, value int) error {
	if opt == tcpMaxSeg {
		return nil
	}
	return syscall.SetsockoptInt(syscall.Handle(fd), level, opt, value)
}
