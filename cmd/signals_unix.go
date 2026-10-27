//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"syscall"
)

func reloadSignal() (chan os.Signal, func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	return c, func() { signal.Stop(c) }
}
