package edge

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/net/proxy"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Router struct {
	rules    []Rule
	bypass   []netip.Prefix
	fallback string
}

func NewRouter(c Config) *Router {
	r := &Router{rules: append([]Rule(nil), c.Rules...), fallback: c.Default}
	for _, s := range c.Bypass {
		p, _ := netip.ParsePrefix(s)
		r.bypass = append(r.bypass, p.Masked())
	}
	return r
}
func validDomain(s string) bool {
	s = strings.TrimPrefix(s, "*.")
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, p := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(p) == 0 || len(p) > 63 || p[0] == '-' || p[len(p)-1] == '-' {
			return false
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func domainMatch(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(host, pattern[1:])
	}
	return host == pattern
}

// Match: explicit rules in order, bypass prefixes, then default. Domains are
// evaluated before DNS; no local DNS query is made for SOCKS5-routed domains.
func (r *Router) Match(network, host string, port uint16) string {
	ip, _ := netip.ParseAddr(host)
	ip = ip.Unmap()
	for _, rule := range r.rules {
		if rule.Network != "" && !strings.HasPrefix(network, rule.Network) || rule.Port != 0 && rule.Port != port {
			continue
		}
		if rule.Domain != "" && !domainMatch(rule.Domain, host) {
			continue
		}
		if rule.CIDR != "" {
			p, _ := netip.ParsePrefix(rule.CIDR)
			if !ip.IsValid() || !p.Contains(ip) {
				continue
			}
		}
		return rule.Action
	}
	for _, p := range r.bypass {
		if p.Contains(ip) {
			return "direct"
		}
	}
	return r.fallback
}

var active atomic.Pointer[Manager]

func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	m := active.Load()
	if m == nil {
		return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, address)
	}
	return m.DialContext(ctx, network, address)
}
func (m *Manager) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portS, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	port, e := strconv.ParseUint(portS, 10, 16)
	if e != nil {
		return nil, e
	}
	m.mu.RLock()
	enabled := m.config.Enabled
	router := m.router
	m.mu.RUnlock()
	action := "direct"
	if enabled {
		action = router.Match(network, host, uint16(port))
	}
	if action == "block" {
		m.traffic.RejectedFlows.Add(1)
		return nil, errors.New("destination blocked by edge policy")
	}
	if action == "direct" {
		c, e := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return m.wrapConn(c, network), nil
	}
	return m.DialGroup(ctx, network, address, action, false)
}
func (m *Manager) DialGroup(ctx context.Context, network, address, group string, forwardOnly bool) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	m.mu.RLock()
	bal := m.balancer
	m.mu.RUnlock()
	excluded := map[string]bool{}
	var last error
	for {
		u, ok := bal.Pick(group, excluded)
		if !ok {
			if last == nil {
				last = errors.New("no healthy upstream")
			}
			return nil, fmt.Errorf("edge group %s: %w", group, last)
		}
		excluded[u.Name] = true
		if forwardOnly && u.Kind != "forward" {
			continue
		}
		start := time.Now()
		var c net.Conn
		var e error
		if u.Kind == "forward" {
			c, e = (&net.Dialer{}).DialContext(ctx, network, u.Address)
		} else {
			if !strings.HasPrefix(network, "tcp") {
				last = errors.New("SOCKS5 upstream does not support UDP; use a UDP forward group")
				continue
			}
			d, err := proxy.SOCKS5("tcp", u.Address, nil, &net.Dialer{})
			if err != nil {
				return nil, err
			}
			c, e = d.(proxy.ContextDialer).DialContext(ctx, network, address)
		}
		if e == nil {
			bal.Record(u.Name, time.Since(start), nil)
			return m.wrapConn(c, network), nil
		}
		last = e
		bal.Record(u.Name, 0, e)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
}

func (m *Manager) wrapConn(conn net.Conn, network string) net.Conn {
	c := m.traffic.Wrap(conn, network).(*trackedConn)
	c.stop = context.AfterFunc(m.ctx, func() { conn.Close() })
	return c
}

type trackedConn struct {
	stop func() bool
	net.Conn
	stats *Counters
	udp   bool
	once  sync.Once
}

func (c *trackedConn) Read(b []byte) (int, error) {
	n, e := c.Conn.Read(b)
	c.stats.In.Add(uint64(n))
	if c.udp && e == nil {
		c.stats.Packets.Add(1)
	}
	return n, e
}
func (c *trackedConn) Write(b []byte) (int, error) {
	n, e := c.Conn.Write(b)
	c.stats.Out.Add(uint64(n))
	if c.udp {
		if e == nil {
			c.stats.Packets.Add(1)
		} else {
			c.stats.Drops.Add(1)
		}
	}
	return n, e
}
func (c *trackedConn) Close() error {
	e := c.Conn.Close()
	c.once.Do(func() {
		if c.stop != nil {
			c.stop()
		}
		if c.udp {
			c.stats.UDP.Add(-1)
		} else {
			c.stats.TCP.Add(-1)
		}
	})
	return e
}
func (c *trackedConn) CloseWrite() error {
	if w, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return w.CloseWrite()
	}
	return c.Close()
}
