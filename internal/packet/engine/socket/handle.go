//go:build pcap && cgo

package socket

import (
	"fmt"
	"runtime"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/pcap"

	"github.com/syklonAK/night-watcher/internal/packet/engine/conf"
)

func newHandle(cfg *conf.Network, timeout time.Duration) (captureHandle, error) {
	// On Windows, use the GUID field to construct the NPF device name
	// On other platforms, use the interface name directly
	ifaceName := cfg.Interface.Name
	if runtime.GOOS == "windows" {
		ifaceName = cfg.GUID
	}

	inactive, err := pcap.NewInactiveHandle(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to create inactive pcap handle for %s: %v", cfg.Interface.Name, err)
	}
	defer inactive.CleanUp()

	if err = inactive.SetBufferSize(cfg.PCAP.Sockbuf); err != nil {
		return nil, fmt.Errorf("failed to set pcap buffer size to %d: %v", cfg.PCAP.Sockbuf, err)
	}

	if err = inactive.SetSnapLen(65536); err != nil {
		return nil, fmt.Errorf("failed to set pcap snap length: %v", err)
	}
	if err = inactive.SetPromisc(true); err != nil {
		return nil, fmt.Errorf("failed to enable promiscuous mode: %v", err)
	}
	if err = inactive.SetTimeout(timeout); err != nil {
		return nil, fmt.Errorf("failed to set pcap timeout: %v", err)
	}
	if err = inactive.SetImmediateMode(true); err != nil {
		return nil, fmt.Errorf("failed to enable immediate mode: %v", err)
	}

	handle, err := inactive.Activate()
	if err != nil {
		return nil, fmt.Errorf("failed to activate pcap handle on %s: %v", cfg.Interface.Name, err)
	}

	return &pcapCapture{handle}, nil
}

type pcapCapture struct{ *pcap.Handle }

func (p *pcapCapture) SetDirection(d int) error { return p.Handle.SetDirection(pcap.Direction(d)) }
func (p *pcapCapture) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	b, i, e := p.Handle.ReadPacketData()
	if e == pcap.NextErrorTimeoutExpired {
		e = errCaptureTimeout
	}
	return b, i, e
}
