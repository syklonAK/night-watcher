package edge

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSNIPassthroughPreservesHello(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		client := tls.Client(a, &tls.Config{ServerName: "test.example.com", InsecureSkipVerify: true})
		_ = client.Handshake()
	}()
	host, pre, e := sniffSNI(b)
	b.Close()
	<-done
	if e != nil || host != "test.example.com" || len(pre) < 5 || pre[0] != 22 {
		t.Fatalf("host %s bytes %d err %v", host, len(pre), e)
	}
}
func TestInboundHTTPRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.Host + r.URL.Path)) }))
	defer backend.Close()
	addr := strings.TrimPrefix(backend.URL, "http://")
	c := DefaultConfig()
	c.Enabled = true
	c.Upstreams = []Upstream{{Name: "http", Group: "web", Kind: "forward", Address: addr, Weight: 10, Probe: probeConfig(addr)}}
	m := testManager(t, c, false)
	m.balancer.Record("http", time.Millisecond, nil)
	i, e := newInlet(ListenerConfig{Name: "test", Address: "127.0.0.1:0", Mode: "http", DefaultGroup: "web", Routes: []InboundRoute{{Host: "test.example.com", Path: "/test", Group: "web"}}})
	if e != nil {
		t.Fatal(e)
	}
	defer i.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go i.run(ctx, m)
	req, _ := http.NewRequest("GET", "http://"+i.ln.Addr().String()+"/test", nil)
	req.Host = "test.example.com"
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, e := cl.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "test.example.com/test" {
		t.Fatalf("%d %q", resp.StatusCode, b)
	}
}
