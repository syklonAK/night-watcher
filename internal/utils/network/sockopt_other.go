//go:build !linux

package network

// setCongestion is a no-op off Linux: TCP_CONGESTION is a Linux socket option.
// night-watcher runs on Linux servers; this keeps local builds on other systems
// compiling.
func setCongestion(fd uintptr, algo string) {}
