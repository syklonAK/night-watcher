package edge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicResolverValidatesAddress(t *testing.T) {
	for _, tt := range []struct {
		body string
		ok   bool
	}{{"8.8.8.8", true}, {"127.0.0.1", false}, {"10.1.2.3", false}, {"garbage", false}, {"2001:4860:4860::8888", false}} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tt.body)) }))
		ip, e := resolvePublic(context.Background(), ts.URL, "tcp4", false)
		ts.Close()
		if (e == nil) != tt.ok {
			t.Fatalf("%q = %q, %v", tt.body, ip, e)
		}
	}
}
func TestInvalidPathProbe(t *testing.T) {
	if _, e := Ping(context.Background(), "invalid", 56, 64, false); e == nil {
		t.Fatal("invalid IP accepted")
	}
	if _, e := Ping(context.Background(), "127.0.0.1", 1, 64, false); e == nil {
		t.Fatal("invalid size accepted")
	}
}
