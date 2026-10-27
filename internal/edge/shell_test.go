package edge

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func shellTestConfig(t *testing.T) ShellConfig {
	return ShellConfig{Enabled: true, UID: 1000, GID: 1000, Directory: t.TempDir(), Commands: map[string][]string{"status": {filepath.Join(t.TempDir(), "diagnostic")}}, TimeoutSeconds: 60, IdleSeconds: 10, MaxSessions: 1}
}
func TestShellTicketScopesAndExpiry(t *testing.T) {
	s := NewShell()
	c := shellTestConfig(t)
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Issue("owner", "missing", c); e == nil {
		t.Fatal("arbitrary profile accepted")
	}
	tok, e := s.Issue("owner", "status", c)
	if e != nil || len(tok) != 64 {
		t.Fatalf("ticket %s %v", tok, e)
	}
	if s.tickets[tok].owner != "owner" {
		t.Fatal("owner not bound")
	}
	s.tickets[tok] = ticket{expires: time.Now().Add(-time.Second)}
	if _, e = s.Issue("owner", "status", c); e != nil {
		t.Fatal(e)
	}
	if _, ok := s.tickets[tok]; ok {
		t.Fatal("expired ticket retained")
	}
	c.UID = 0
	if c.Validate() == nil {
		t.Fatal("root allowed")
	}
}
func TestShellOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "http://panel.test/api/shell/ws", nil)
	for _, origin := range []string{"", "https://panel.test", "http://evil.test", "http://panel.test.evil.test"} {
		r.Header.Set("Origin", origin)
		if SameOrigin(r) {
			t.Fatalf("accepted %s", origin)
		}
	}
	r.Header.Set("Origin", "http://panel.test")
	if !SameOrigin(r) {
		t.Fatal("same origin rejected")
	}
}

func TestShellRejectsPlaintextRemote(t *testing.T) {
	r := httptest.NewRequest("GET", "http://panel/api/shell/ws", nil)
	r.RemoteAddr = "192.0.2.1:3333"
	if SecureShellTransport(r) {
		t.Fatal("remote plaintext terminal allowed")
	}
	r.RemoteAddr = "127.0.0.1:3333"
	if !SecureShellTransport(r) {
		t.Fatal("SSH forward rejected")
	}
}
