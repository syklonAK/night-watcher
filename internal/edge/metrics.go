package edge

import (
	"context"
	"encoding/json"
	"fmt"
	tunnelmetrics "github.com/syklonAK/night-watcher/internal/metrics"
	gnet "github.com/shirou/gopsutil/v4/net"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Counters struct {
	In, Out, Packets, Drops atomic.Uint64
	RejectedFlows           atomic.Uint64
	TCP, UDP                atomic.Int64
}

func (c *Counters) Wrap(n net.Conn, network string) net.Conn {
	udp := strings.HasPrefix(network, "udp")
	if udp {
		c.UDP.Add(1)
	} else {
		c.TCP.Add(1)
	}
	return &trackedConn{Conn: n, stats: c, udp: udp}
}

type Traffic struct {
	BytesIn       uint64  `json:"bytes_in"`
	BytesOut      uint64  `json:"bytes_out"`
	IngressBPS    float64 `json:"ingress_bps"`
	EgressBPS     float64 `json:"egress_bps"`
	Packets       uint64  `json:"packets"`
	Drops         uint64  `json:"drops"`
	DropPercent   float64 `json:"drop_percent"`
	ActiveTCP     int64   `json:"active_tcp"`
	ActiveUDP     int64   `json:"active_udp"`
	RejectedFlows uint64  `json:"rejected_flows"`
}
type TunnelMetric struct {
	tunnelmetrics.Snapshot
	IngressBPS  float64  `json:"ingress_bps"`
	EgressBPS   float64  `json:"egress_bps"`
	Stale       bool     `json:"stale"`
	LossPercent *float64 `json:"loss_percent,omitempty"`
}
type Metrics struct {
	Taken      time.Time          `json:"taken"`
	Process    Traffic            `json:"process"`
	Interfaces map[string]Traffic `json:"interfaces"`
	Tunnels    []TunnelMetric     `json:"tunnels"`
	HostTCP    int                `json:"host_tcp"`
	HostUDP    int                `json:"host_udp"`
	Errors     []string           `json:"errors,omitempty"`
}
type metricSampler struct {
	mu   sync.Mutex
	last Metrics
}

func rate(now, old uint64, dt float64) float64 {
	if dt <= 0 || now < old {
		return 0
	}
	return float64(now-old) / dt
}
func (s *metricSampler) Sample(ctx context.Context, c *Counters, dir string) Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	dt := now.Sub(s.last.Taken).Seconds()
	m := Metrics{Taken: now, Interfaces: map[string]Traffic{}, Tunnels: []TunnelMetric{}}
	m.Process = Traffic{BytesIn: c.In.Load(), BytesOut: c.Out.Load(), Packets: c.Packets.Load(), Drops: c.Drops.Load(), ActiveTCP: c.TCP.Load(), ActiveUDP: c.UDP.Load(), RejectedFlows: c.RejectedFlows.Load()}
	m.Process.IngressBPS = rate(m.Process.BytesIn, s.last.Process.BytesIn, dt)
	m.Process.EgressBPS = rate(m.Process.BytesOut, s.last.Process.BytesOut, dt)
	if n := m.Process.Packets + m.Process.Drops; n > 0 {
		m.Process.DropPercent = 100 * float64(m.Process.Drops) / float64(n)
	}
	rows, e := gnet.IOCountersWithContext(ctx, true)
	if e != nil {
		m.Errors = append(m.Errors, e.Error())
	}
	for _, r := range rows {
		t := Traffic{BytesIn: r.BytesRecv, BytesOut: r.BytesSent, Packets: r.PacketsRecv + r.PacketsSent, Drops: r.Dropin + r.Dropout}
		old, seen := s.last.Interfaces[r.Name]
		if seen && !s.last.Taken.IsZero() {
			t.IngressBPS = rate(t.BytesIn, old.BytesIn, dt)
			t.EgressBPS = rate(t.BytesOut, old.BytesOut, dt)
		}
		if t.Packets+t.Drops > 0 {
			t.DropPercent = 100 * float64(t.Drops) / float64(t.Packets+t.Drops)
		}
		m.Interfaces[r.Name] = t
	}
	conns, e := gnet.ConnectionsWithContext(ctx, "all")
	if e != nil {
		m.Errors = append(m.Errors, e.Error())
	} else {
		for _, c := range conns {
			if c.Type == 1 && c.Status == "ESTABLISHED" {
				m.HostTCP++
			}
			if c.Type == 2 {
				m.HostUDP++
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.metrics.json"))
	old := map[string]TunnelMetric{}
	for _, t := range s.last.Tunnels {
		old[t.Name] = t
	}
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil || len(b) > MaxConfigSize {
			continue
		}
		var t TunnelMetric
		if json.Unmarshal(b, &t.Snapshot) != nil {
			continue
		}
		t.Stale = now.Sub(t.Taken) > 10*time.Second
		if t.KCP != nil {
			loss := t.KCP.LossPercent()
			t.LossPercent = &loss
		}
		if prev, ok := old[t.Name]; ok {
			delta := t.Taken.Sub(prev.Taken).Seconds()
			if delta > 0 {
				t.IngressBPS = rate(t.BytesIn, prev.BytesIn, delta)
				t.EgressBPS = rate(t.BytesOut, prev.BytesOut, delta)
			} else if !t.Stale {
				t.IngressBPS = prev.IngressBPS
				t.EgressBPS = prev.EgressBPS
			}
		}
		m.Tunnels = append(m.Tunnels, t)
	}
	s.last = m
	return m
}
func WritePrometheus(w io.Writer, m Metrics, nodes []NodeState) {
	emit := func(name, kind, help string, rows map[string]float64) {
		fmt.Fprintf(w, "# HELP night_watcher_edge_%s %s\n# TYPE night_watcher_edge_%s %s\n", name, help, name, kind)
		for labels, v := range rows {
			fmt.Fprintf(w, "night_watcher_edge_%s%s %g\n", name, labels, v)
		}
	}
	for _, metric := range []struct {
		name, kind string
		get        func(Traffic) float64
	}{{"bytes_in_total", "counter", func(t Traffic) float64 { return float64(t.BytesIn) }}, {"bytes_out_total", "counter", func(t Traffic) float64 { return float64(t.BytesOut) }}, {"ingress_bytes_per_second", "gauge", func(t Traffic) float64 { return t.IngressBPS }}, {"egress_bytes_per_second", "gauge", func(t Traffic) float64 { return t.EgressBPS }}, {"drops_total", "counter", func(t Traffic) float64 { return float64(t.Drops) }}, {"drop_percent", "gauge", func(t Traffic) float64 { return t.DropPercent }}} {
		rows := map[string]float64{`{scope="process",name="edge"}`: metric.get(m.Process)}
		for n, t := range m.Interfaces {
			rows[fmt.Sprintf(`{scope="interface",name=%q}`, n)] = metric.get(t)
		}
		emit(metric.name, metric.kind, metric.name, rows)
	}
	emit("active_tcp", "gauge", "Active edge TCP connections", map[string]float64{"": float64(m.Process.ActiveTCP)})
	emit("active_udp", "gauge", "Active edge UDP flows", map[string]float64{"": float64(m.Process.ActiveUDP)})
	emit("rejected_flows_total", "counter", "Rejected edge flow attempts", map[string]float64{"": float64(m.Process.RejectedFlows)})
	losses := map[string]float64{}
	for _, t := range m.Tunnels {
		if t.LossPercent != nil {
			losses[fmt.Sprintf(`{name=%q}`, t.Name)] = *t.LossPercent
		}
	}
	emit("tunnel_loss_percent", "gauge", "KCP transport loss estimate", losses)
	up, rtt := map[string]float64{}, map[string]float64{}
	for _, n := range nodes {
		l := fmt.Sprintf(`{name=%q}`, n.Upstream.Name)
		up[l] = 0
		if n.Healthy {
			up[l] = 1
		}
		rtt[l] = n.RTTMS
	}
	emit("upstream_up", "gauge", "Healthy upstream", up)
	emit("rtt_ms", "gauge", "Upstream probe RTT milliseconds", rtt)
	for _, metric := range []struct {
		name, kind string
		get        func(TunnelMetric) float64
	}{{"tunnel_bytes_in_total", "counter", func(t TunnelMetric) float64 { return float64(t.BytesIn) }}, {"tunnel_bytes_out_total", "counter", func(t TunnelMetric) float64 { return float64(t.BytesOut) }}, {"tunnel_ingress_bytes_per_second", "gauge", func(t TunnelMetric) float64 { return t.IngressBPS }}, {"tunnel_egress_bytes_per_second", "gauge", func(t TunnelMetric) float64 { return t.EgressBPS }}} {
		rows := map[string]float64{}
		for _, t := range m.Tunnels {
			rows[fmt.Sprintf(`{name=%q}`, t.Name)] = metric.get(t)
		}
		emit(metric.name, metric.kind, metric.name, rows)
	}
}
