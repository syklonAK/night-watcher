//go:build linux

package edge

import (
	"context"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPTYTicketReplayAndProcessExit(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("credential transition requires root; run in isolated Linux CI")
	}
	dir := t.TempDir()
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	c := ShellConfig{Enabled: true, UID: 65534, GID: 65534, Directory: "/tmp", Commands: map[string][]string{"id": {"/usr/bin/id"}}, TimeoutSeconds: 5, IdleSeconds: 2, MaxSessions: 1}
	s := NewShell()
	tok, e := s.Issue("owner", "id", c)
	if e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Serve(context.Background(), w, r, "owner", c, func() bool { return true })
	}))
	defer ts.Close()
	d := websocket.Dialer{Subprotocols: []string{"night-watcher-shell", "ticket." + tok}, HandshakeTimeout: time.Second}
	headers := http.Header{"Origin": []string{ts.URL}}
	ws, _, e := d.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), headers)
	if e != nil {
		t.Fatal(e)
	}
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, e = ws.ReadMessage()
		if e != nil {
			break
		}
	}
	ws.Close()
	if ws, resp, e := d.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), headers); e == nil {
		ws.Close()
		t.Fatal("ticket replay accepted")
	} else if resp == nil || resp.StatusCode != 403 {
		t.Fatalf("unexpected replay response: %v %v", resp, e)
	}
}
