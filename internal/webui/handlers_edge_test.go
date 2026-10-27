package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/syklonAK/night-watcher/internal/edge"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func edgeTestServer(t *testing.T) (*server, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	c := edge.DefaultConfig()
	c.Detect = edge.DetectConfig{}
	if e := edge.SaveConfig(filepath.Join(dir, "edge.json"), c); e != nil {
		t.Fatal(e)
	}
	m, e := edge.Start(context.Background(), dir, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	s := &server{sessions: newSessionStore(), edge: m}
	mux := http.NewServeMux()
	s.registerEdge(mux)
	ts := httptest.NewServer(gzipMiddleware(mux))
	t.Cleanup(ts.Close)
	return s, ts, s.sessions.create("127.0.0.1")
}
func edgeReq(t *testing.T, ts *httptest.Server, method, path, token, origin string, body []byte) *http.Response {
	t.Helper()
	req, e := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
func TestEdgeAuthMethodsAndCSRF(t *testing.T) {
	s, ts, tok := edgeTestServer(t)
	for _, path := range []string{"/api/edge/config", "/api/edge/state", "/api/edge/metrics", "/api/edge/backup", "/api/shell/ws"} {
		r := edgeReq(t, ts, "GET", path, "", "", nil)
		if r.StatusCode != 401 {
			t.Fatalf("%s unauthenticated = %d", path, r.StatusCode)
		}
	}
	r := edgeReq(t, ts, "GET", "/api/edge/metrics", tok, "", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("sensitive metrics cached")
	}
	body, _ := json.Marshal(s.edge.Config())
	for _, origin := range []string{"", "http://evil.invalid"} {
		r = edgeReq(t, ts, "POST", "/api/edge/config", tok, origin, body)
		if r.StatusCode != 403 {
			t.Fatalf("origin %q: %d", origin, r.StatusCode)
		}
	}
	r = edgeReq(t, ts, "DELETE", "/api/edge/config", tok, ts.URL, nil)
	if r.StatusCode != 405 {
		t.Fatal(r.StatusCode)
	}
	r = edgeReq(t, ts, "POST", "/api/edge/config", tok, ts.URL, body)
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("%d: %s", r.StatusCode, b)
	}
}
func TestEdgeRejectsShellEscalationAndBadRestore(t *testing.T) {
	s, ts, tok := edgeTestServer(t)
	c := s.edge.Config()
	c.Shell.Commands = map[string][]string{"diagnostic": {"/usr/bin/id"}}
	body, _ := json.Marshal(c)
	r := edgeReq(t, ts, "POST", "/api/edge/restore", tok, ts.URL, body)
	if r.StatusCode != 403 {
		t.Fatal("shell scope mutable via API", r.StatusCode)
	}
	r = edgeReq(t, ts, "POST", "/api/edge/restore", tok, ts.URL, []byte(`{"version":999}`))
	if r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
	r = edgeReq(t, ts, "POST", "/api/shell/token", tok, ts.URL, []byte(`{"profile":"diagnostic"}`))
	if r.StatusCode != 403 {
		t.Fatal("disabled shell issued token")
	}
}
func TestEdgePrometheusAndEmbeddedUI(t *testing.T) {
	_, ts, tok := edgeTestServer(t)
	r := edgeReq(t, ts, "GET", "/api/edge/metrics?format=prometheus", tok, "", nil)
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "# TYPE night_watcher_edge_active_tcp gauge") {
		t.Fatal(string(b))
	}
	r = edgeReq(t, ts, "GET", "/edge", tok, "", nil)
	b, _ = io.ReadAll(r.Body)
	if r.StatusCode != 200 || !strings.Contains(string(b), "/api/shell/token") || !strings.Contains(string(b), "Night Watcher") {
		t.Fatal("missing integrated page")
	}
}
func TestUpgradeBypassesGzipBuffer(t *testing.T) {
	handler := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(*bufferingWriter); ok {
			t.Error("WebSocket upgrade wrapped in buffering writer")
		}
		w.WriteHeader(204)
	}))
	r := httptest.NewRequest("GET", "http://panel/api/shell/ws", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(httptest.NewRecorder(), r)
}
