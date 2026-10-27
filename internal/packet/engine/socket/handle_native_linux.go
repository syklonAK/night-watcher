//go:build linux && !pcap

package socket

import (
	"fmt"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/syklonAK/night-watcher/internal/packet/engine/conf"
	"golang.org/x/sys/unix"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativeHandle struct {
	mu        sync.RWMutex
	fd        int
	direction int
	port      uint16
	timeout   time.Duration
}

var captureBuffers = sync.Pool{New: func() any { return make([]byte, 65536) }}

func newHandle(cfg *conf.Network, timeout time.Duration) (captureHandle, error) {
	fd, e := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, int(htons(unix.ETH_P_ALL)))
	if e != nil {
		return nil, e
	}
	if e = unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ALL), Ifindex: cfg.Interface.Index}); e != nil {
		unix.Close(fd)
		return nil, e
	}
	if e = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, cfg.PCAP.Sockbuf); e != nil {
		unix.Close(fd)
		return nil, e
	}
	return &nativeHandle{fd: fd, timeout: timeout}, nil
}
func htons(n uint16) uint16                      { return n<<8 | n>>8 }
func (h *nativeHandle) SetDirection(n int) error { h.direction = n; return nil }
func (h *nativeHandle) SetBPFFilter(s string) error {
	const prefix = "tcp and dst port "
	if !strings.HasPrefix(s, prefix) {
		return fmt.Errorf("unsupported native capture filter %q", s)
	}
	n, e := strconv.ParseUint(strings.TrimPrefix(s, prefix), 10, 16)
	h.port = uint16(n)
	return e
}
func (h *nativeHandle) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	deadline := time.Now().Add(h.timeout)
	buf := captureBuffers.Get().([]byte)
	defer captureBuffers.Put(buf)
	for {
		if h.timeout > 0 && time.Now().After(deadline) {
			return nil, gopacket.CaptureInfo{}, errCaptureTimeout
		}
		h.mu.RLock()
		if h.fd < 0 {
			h.mu.RUnlock()
			return nil, gopacket.CaptureInfo{}, net.ErrClosed
		}
		n, from, e := unix.Recvfrom(h.fd, buf, 0)
		h.mu.RUnlock()
		if e == unix.EAGAIN || e == unix.EWOULDBLOCK {
			if h.timeout > 0 && time.Now().After(deadline) {
				return nil, gopacket.CaptureInfo{}, errCaptureTimeout
			}
			time.Sleep(time.Millisecond)
			continue
		}
		if e != nil {
			return nil, gopacket.CaptureInfo{}, e
		}
		if a, ok := from.(*unix.SockaddrLinklayer); ok && h.direction == directionIn && a.Pkttype == unix.PACKET_OUTGOING {
			continue
		}
		if h.port != 0 {
			p := gopacket.NewPacket(buf[:n], layers.LayerTypeEthernet, gopacket.NoCopy)
			l := p.Layer(layers.LayerTypeTCP)
			if l == nil || uint16(l.(*layers.TCP).DstPort) != h.port {
				continue
			}
		}
		return append([]byte(nil), buf[:n]...), gopacket.CaptureInfo{Timestamp: time.Now(), CaptureLength: n, Length: n}, nil
	}
}
func (h *nativeHandle) WritePacketData(b []byte) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.fd < 0 {
		return net.ErrClosed
	}
	return unix.Sendto(h.fd, b, 0, nil)
}
func (h *nativeHandle) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fd >= 0 {
		_ = unix.Close(h.fd)
		h.fd = -1
	}
}
