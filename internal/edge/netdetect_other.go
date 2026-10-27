//go:build !linux

package edge

import (
	"errors"
	"net"
)

func setDontFragment(net.PacketConn, bool) error { return errors.New("DF path probes require Linux") }
