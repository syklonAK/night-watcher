package socket

import (
	"errors"
	"github.com/gopacket/gopacket"
)

// captureHandle allows static Linux builds to use AF_PACKET directly. The
// optional pcap build tag retains the original libpcap implementation.
type captureHandle interface {
	ReadPacketData() ([]byte, gopacket.CaptureInfo, error)
	WritePacketData([]byte) error
	SetDirection(int) error
	SetBPFFilter(string) error
	Close()
}

const (
	directionIn  = 1
	directionOut = 2
)

var errCaptureTimeout = errors.New("capture timeout")
