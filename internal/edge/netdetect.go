package edge

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type Interface struct {
	Name      string   `json:"name"`
	MTU       int      `json:"mtu"`
	Addresses []string `json:"addresses"`
	WAN       bool     `json:"wan"`
}
type NetworkState struct {
	Interfaces []Interface `json:"interfaces"`
	PublicIPv4 string      `json:"public_ipv4,omitempty"`
	PublicIPv6 string      `json:"public_ipv6,omitempty"`
	Path       *PathState  `json:"path,omitempty"`
	Errors     []string    `json:"errors,omitempty"`
	Taken      time.Time   `json:"taken"`
}
type PathState struct {
	Target string  `json:"target"`
	RTTMS  float64 `json:"rtt_ms"`
	MTU    int     `json:"mtu"`
	Error  string  `json:"error,omitempty"`
}

// Detect finds the route-selected local address without sending UDP traffic.
// Public resolvers and optional DF ICMP probes have a shared eight-second budget.
func Detect(ctx context.Context, c DetectConfig) NetworkState {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	s := NetworkState{Taken: time.Now()}
	var local []net.IP
	for _, a := range []string{"1.1.1.1:443", "[2606:4700:4700::1111]:443"} {
		conn, e := (&net.Dialer{}).DialContext(ctx, "udp", a)
		if e == nil {
			local = append(local, conn.LocalAddr().(*net.UDPAddr).IP)
			conn.Close()
		}
	}
	ifs, e := net.Interfaces()
	if e != nil {
		s.Errors = append(s.Errors, e.Error())
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		row := Interface{Name: i.Name, MTU: i.MTU}
		addrs, e := i.Addrs()
		if e != nil {
			continue
		}
		for _, a := range addrs {
			row.Addresses = append(row.Addresses, a.String())
			ip, _, _ := net.ParseCIDR(a.String())
			for _, l := range local {
				if l.Equal(ip) {
					row.WAN = true
				}
			}
		}
		s.Interfaces = append(s.Interfaces, row)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, v := range []struct {
		url, network string
		v6           bool
	}{{c.PublicIPv4URL, "tcp4", false}, {c.PublicIPv6URL, "tcp6", true}} {
		if v.url == "" {
			continue
		}
		wg.Add(1)
		go func(u, n string, v6 bool) {
			defer wg.Done()
			ip, e := resolvePublic(ctx, u, n, v6)
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				s.Errors = append(s.Errors, e.Error())
			} else if v6 {
				s.PublicIPv6 = ip
			} else {
				s.PublicIPv4 = ip
			}
		}(v.url, v.network, v.v6)
	}
	if c.PathTarget != "" {
		wg.Add(1)
		go func() { defer wg.Done(); p := DiscoverPath(ctx, c.PathTarget); mu.Lock(); s.Path = &p; mu.Unlock() }()
	}
	wg.Wait()
	return s
}
func resolvePublic(ctx context.Context, url, network string, v6 bool) (string, error) {
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	defer tr.CloseIdleConnections()
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return "", e
	}
	res, e := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("IP resolver status %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 128))
	if e != nil {
		return "", e
	}
	ip, e := netip.ParseAddr(strings.TrimSpace(string(b)))
	if e != nil || ip.Is6() != v6 || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return "", errors.New("resolver returned an invalid public IP")
	}
	return ip.String(), nil
}

// Ping matches source, ID, sequence and a random payload to avoid consuming
// another concurrent probe's reply. Raw ICMP requires CAP_NET_RAW on Linux.
func Ping(ctx context.Context, target string, size, ttl int, df bool) (time.Duration, error) {
	ip := net.ParseIP(target)
	if ip == nil || size < 8 || size > 65500 {
		return 0, errors.New("invalid ICMP probe")
	}
	network, bind, proto := "ip4:icmp", "0.0.0.0", 1
	var typ icmp.Type = ipv4.ICMPTypeEcho
	reply := icmp.Type(ipv4.ICMPTypeEchoReply)
	if ip.To4() == nil {
		network, bind, proto = "ip6:ipv6-icmp", "::", 58
		typ = ipv6.ICMPTypeEchoRequest
		reply = ipv6.ICMPTypeEchoReply
	}
	c, e := net.ListenPacket(network, bind)
	if e != nil {
		return 0, e
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	c.SetDeadline(deadline)
	if proto == 1 {
		if e = ipv4.NewPacketConn(c).SetTTL(ttl); e != nil {
			return 0, e
		}
		if df {
			if e = setDontFragment(c, false); e != nil {
				return 0, e
			}
		}
	} else {
		if df {
			if e = setDontFragment(c, true); e != nil {
				return 0, e
			}
		}
		if e = ipv6.NewPacketConn(c).SetHopLimit(ttl); e != nil {
			return 0, e
		}
	}
	data := make([]byte, size)
	if _, e = rand.Read(data); e != nil {
		return 0, e
	}
	id := int(binary.BigEndian.Uint16(data[:2]))
	seq := int(binary.BigEndian.Uint16(data[2:4]))
	msg := icmp.Message{Type: typ, Body: &icmp.Echo{ID: id, Seq: seq, Data: data}}
	packet, e := msg.Marshal(nil)
	if e != nil {
		return 0, e
	}
	start := time.Now()
	if _, e = c.WriteTo(packet, &net.IPAddr{IP: ip}); e != nil {
		return 0, e
	}
	buf := make([]byte, 65535)
	for {
		n, peer, e := c.ReadFrom(buf)
		if e != nil {
			return 0, e
		}
		m, e := icmp.ParseMessage(proto, buf[:n])
		if e != nil || m.Type != reply {
			continue
		}
		echo, ok := m.Body.(*icmp.Echo)
		p, okIP := peer.(*net.IPAddr)
		if !ok || !okIP || !p.IP.Equal(ip) || echo.ID != id || echo.Seq != seq || string(echo.Data) != string(data) {
			continue
		}
		return time.Since(start), nil
	}
}
func DiscoverPath(ctx context.Context, target string) PathState {
	p := PathState{Target: target}
	r, e := Ping(ctx, target, 56, 64, false)
	if e != nil {
		p.Error = e.Error()
		return p
	}
	p.RTTMS = float64(r) / float64(time.Millisecond)
	// Largest acknowledged unfragmented datagram, bounded to Ethernet MTU.
	// No reply is ambiguous (filtering/loss), so report only proven sizes.
	header := 28
	if net.ParseIP(target).To4() == nil {
		header = 48
	}
	low, high := 576, 1500
	for low <= high && ctx.Err() == nil {
		mid := (low + high) / 2
		_, e = Ping(ctx, target, mid-header, 64, true)
		if e == nil {
			p.MTU = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if p.MTU == 0 {
		p.Error = "path MTU unavailable (ICMP filtered, timeout or DF unsupported)"
	}
	return p
}
