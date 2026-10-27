package edge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBalancerThresholdsAndWeights(t *testing.T) {
	p := probeConfig("127.0.0.1:80")
	p.Failures = 2
	p.Successes = 2
	nodes := []Upstream{{Name: "a", Group: "g", Weight: 3, Probe: p}, {Name: "b", Group: "g", Weight: 1, Probe: p}}
	b := NewBalancer(nodes)
	if _, ok := b.Pick("g", nil); ok {
		t.Fatal("unprobed node selected")
	}
	for _, n := range nodes {
		b.Record(n.Name, 0, nil)
		b.Record(n.Name, 0, nil)
	}
	counts := map[string]int{}
	for i := 0; i < 40; i++ {
		n, ok := b.Pick("g", nil)
		if !ok {
			t.Fatal("no healthy node")
		}
		counts[n.Name]++
	}
	if counts["a"] != 30 || counts["b"] != 10 {
		t.Fatal(counts)
	}
	b.Record("a", 0, errors.New("down"))
	if !b.Snapshot()[0].Healthy {
		t.Fatal("threshold not honored")
	}
	b.Record("a", 0, errors.New("down"))
	if b.Snapshot()[0].Healthy {
		t.Fatal("down node remains healthy")
	}
	b.Record("a", 0, nil)
	if b.Snapshot()[0].Healthy {
		t.Fatal("recovered too early")
	}
	b.Record("a", 500*time.Millisecond, nil)
	if !b.Snapshot()[0].Healthy || b.Snapshot()[0].EffectiveWeight >= 3 {
		t.Fatal("recovery or RTT adjustment failed")
	}
}
func TestHTTPProbeAndCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Error("not HEAD")
		}
		w.WriteHeader(503)
	}))
	defer ts.Close()
	p := probeConfig("127.0.0.1:1")
	p.Kind = "http"
	p.Target = ts.URL
	if _, e := Probe(context.Background(), p); e == nil {
		t.Fatal("accepted unhealthy status")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Probe(ctx, p); e == nil {
		t.Fatal("cancellation ignored")
	}
}
