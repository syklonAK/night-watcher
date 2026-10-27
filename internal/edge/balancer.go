package edge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

type NodeState struct {
	Upstream                     Upstream  `json:"upstream"`
	Healthy                      bool      `json:"healthy"`
	Checked                      bool      `json:"checked"`
	RTTMS                        float64   `json:"rtt_ms"`
	EffectiveWeight              int       `json:"effective_weight"`
	Error                        string    `json:"error,omitempty"`
	CheckedAt                    time.Time `json:"checked_at"`
	failures, successes, current int
}
type Balancer struct {
	mu    sync.Mutex
	nodes []*NodeState
}

func NewBalancer(nodes []Upstream) *Balancer {
	b := &Balancer{}
	for _, u := range nodes {
		b.nodes = append(b.nodes, &NodeState{Upstream: u, EffectiveWeight: u.Weight})
	}
	return b
}

// Smooth weighted round robin. Unprobed nodes fail closed.
func (b *Balancer) Pick(group string, exclude map[string]bool) (Upstream, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var best *NodeState
	total := 0
	for _, n := range b.nodes {
		if n.Upstream.Group != group || !n.Healthy || exclude[n.Upstream.Name] {
			continue
		}
		n.current += n.EffectiveWeight
		total += n.EffectiveWeight
		if best == nil || n.current > best.current {
			best = n
		}
	}
	if best == nil {
		return Upstream{}, false
	}
	best.current -= total
	return best.Upstream, true
}
func (b *Balancer) Record(name string, rtt time.Duration, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range b.nodes {
		if n.Upstream.Name != name {
			continue
		}
		n.Checked = true
		n.CheckedAt = time.Now()
		if err != nil {
			n.Error = err.Error()
			n.failures++
			n.successes = 0
			if n.failures >= n.Upstream.Probe.Failures {
				n.Healthy = false
				n.current = 0
			}
		} else {
			n.Error = ""
			n.failures = 0
			n.successes++
			if n.successes >= n.Upstream.Probe.Successes {
				n.Healthy = true
			}
			ms := float64(rtt) / float64(time.Millisecond)
			if n.RTTMS == 0 {
				n.RTTMS = ms
			} else {
				n.RTTMS = .8*n.RTTMS + .2*ms
			}
			n.EffectiveWeight = max(1, int(float64(n.Upstream.Weight)/(1+n.RTTMS/100)))
		}
		return
	}
}
func (b *Balancer) Snapshot() []NodeState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]NodeState, 0, len(b.nodes))
	for _, n := range b.nodes {
		out = append(out, *n)
	}
	return out
}
func (b *Balancer) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range b.Snapshot() {
		wg.Add(1)
		go func(u Upstream) {
			defer wg.Done()
			ticker := time.NewTicker(seconds(u.Probe.IntervalSeconds))
			defer ticker.Stop()
			for {
				d, e := Probe(ctx, u.Probe)
				b.Record(u.Name, d, e)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(s.Upstream)
	}
	wg.Wait()
}
func Probe(ctx context.Context, p ProbeConfig) (time.Duration, error) {
	if e := p.Validate(); e != nil {
		return 0, e
	}
	ctx, cancel := context.WithTimeout(ctx, seconds(p.TimeoutSeconds))
	defer cancel()
	start := time.Now()
	switch p.Kind {
	case "tcp":
		c, e := (&net.Dialer{}).DialContext(ctx, "tcp", p.Target)
		if e != nil {
			return 0, e
		}
		c.Close()
	case "http":
		req, e := http.NewRequestWithContext(ctx, http.MethodHead, p.Target, nil)
		if e != nil {
			return 0, e
		}
		tr := &http.Transport{Proxy: nil, DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		cl := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, e := cl.Do(req)
		if e != nil {
			return 0, e
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			return 0, fmt.Errorf("HTTP health status %d", resp.StatusCode)
		}
	case "icmp":
		return Ping(ctx, p.Target, 56, 64, false)
	}
	return time.Since(start), nil
}
