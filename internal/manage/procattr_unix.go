//go:build !windows

package manage

import "syscall"

func logProcessAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
