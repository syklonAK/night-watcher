//go:build (!linux && !pcap) || (pcap && !cgo)

package socket

import (
	"errors"
	"github.com/syklonAK/night-watcher/internal/packet/engine/conf"
	"time"
)

func newHandle(*conf.Network, time.Duration) (captureHandle, error) {
	return nil, errors.New("raw Packet transport requires Linux, or -tags pcap with CGO and libpcap")
}
