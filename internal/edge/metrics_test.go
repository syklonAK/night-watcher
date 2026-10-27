package edge

import (
	"bytes"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

func TestCountersConcurrentClose(t *testing.T) {
	var c Counters
	a, b := net.Pipe()
	wrapped := c.Wrap(a, "udp")
	defer b.Close()
	go func() { b.Write([]byte("data")) }()
	buf := make([]byte, 4)
	if _, e := io.ReadFull(wrapped, buf); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); wrapped.Close() }()
	}
	wg.Wait()
	if c.UDP.Load() != 0 || c.In.Load() != 4 || c.Packets.Load() != 1 {
		t.Fatal("incorrect concurrent counters")
	}
}
func TestMetricsRateResetAndPrometheus(t *testing.T) {
	if rate(10, 20, 2) != 0 || rate(30, 10, 2) != 10 || rate(30, 10, 0) != 0 {
		t.Fatal("rate reset handling")
	}
	var b bytes.Buffer
	WritePrometheus(&b, Metrics{Interfaces: map[string]Traffic{"eth0": {BytesIn: 42}}}, nil)
	out := b.String()
	if !strings.Contains(out, `night_watcher_edge_bytes_in_total{scope="interface",name="eth0"} 42`) || strings.Contains(out, "night-watcher") {
		t.Fatal(out)
	}
}
