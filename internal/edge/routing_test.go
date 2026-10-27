package edge

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestRouterPrecedenceAndBoundaries(t *testing.T) {
	c := DefaultConfig()
	c.Default = "remote"
	c.Bypass = []string{"10.0.0.0/8", "fc00::/7"}
	c.Rules = []Rule{{Domain: "*.example.com", Action: "block"}, {CIDR: "10.1.0.0/16", Port: 443, Action: "special"}, {Domain: "EXAMPLE.NET", Network: "udp", Action: "direct"}}
	r := NewRouter(c)
	for _, tt := range []struct {
		n, h string
		p    uint16
		want string
	}{{"tcp", "a.example.com", 443, "block"}, {"tcp", "badexample.com", 443, "remote"}, {"tcp", "example.com", 443, "remote"}, {"tcp", "10.1.2.3", 443, "special"}, {"tcp", "10.1.2.3", 80, "direct"}, {"udp", "EXAMPLE.NET.", 53, "direct"}, {"tcp", "example.net", 53, "remote"}, {"tcp", "fd00::1", 80, "direct"}, {"tcp", "2001:db8::1", 80, "remote"}} {
		if got := r.Match(tt.n, tt.h, tt.p); got != tt.want {
			t.Errorf("%+v: got %s", tt, got)
		}
	}
}
func testManager(t *testing.T, c Config, owner bool) *Manager {
	t.Helper()
	dir := t.TempDir()
	c.Detect = DetectConfig{}
	if e := SaveConfig(configPath(dir), c); e != nil {
		t.Fatal(e)
	}
	m, e := Start(context.Background(), dir, owner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	return m
}
func probeConfig(address string) ProbeConfig {
	return ProbeConfig{Kind: "tcp", Target: address, IntervalSeconds: 60, TimeoutSeconds: 1, Failures: 1, Successes: 1}
}
func TestForwardFailoverAndFailClosed(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	dead, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	deadAddr := dead.Addr().String()
	dead.Close()
	c := DefaultConfig()
	c.Enabled = true
	c.Default = "egress"
	c.Upstreams = []Upstream{{Name: "failed", Group: "egress", Kind: "forward", Address: deadAddr, Weight: 100, Probe: probeConfig(deadAddr)}, {Name: "working", Group: "egress", Kind: "forward", Address: ln.Addr().String(), Weight: 1, Probe: probeConfig(ln.Addr().String())}}
	m := testManager(t, c, false)
	m.balancer.Record("failed", time.Millisecond, nil)
	m.balancer.Record("working", time.Millisecond, nil)
	conn, e := m.DialContext(context.Background(), "tcp", "example.invalid:443")
	if e != nil {
		t.Fatal(e)
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, e = conn.Write([]byte("hello")); e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 5)
	if _, e = io.ReadFull(conn, buf); e != nil || string(buf) != "hello" {
		t.Fatalf("echo %q: %v", buf, e)
	}
	conn.Close()
	if m.traffic.TCP.Load() != 0 || m.traffic.In.Load() != 5 || m.traffic.Out.Load() != 5 {
		t.Fatal("traffic accounting mismatch")
	}
	m.balancer.Record("working", 0, io.EOF)
	m.balancer.Record("failed", 0, io.EOF)
	if conn, e = m.DialContext(context.Background(), "tcp", ln.Addr().String()); e == nil {
		conn.Close()
		t.Fatal("all nodes down bypassed policy")
	}
}
func TestBlockNeverDials(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.Default = "block"
	m := testManager(t, c, false)
	if _, e := m.DialContext(context.Background(), "tcp", "127.0.0.1:1"); e == nil {
		t.Fatal("block ignored")
	}
	if m.traffic.RejectedFlows.Load() != 1 {
		t.Fatal("missing drop")
	}
}
