package edge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ShellConfig struct {
	Enabled        bool                `json:"enabled"`
	UID            uint32              `json:"uid"`
	GID            uint32              `json:"gid"`
	Directory      string              `json:"directory"`
	Commands       map[string][]string `json:"commands"`
	TimeoutSeconds int                 `json:"timeout_seconds"`
	IdleSeconds    int                 `json:"idle_seconds"`
	MaxSessions    int                 `json:"max_sessions"`
}

func (c ShellConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.UID == 0 || c.GID == 0 {
		return errors.New("web shell must use a non-root UID and GID")
	}
	if !filepath.IsAbs(c.Directory) || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 || c.IdleSeconds < 1 || c.IdleSeconds > c.TimeoutSeconds || c.MaxSessions < 1 || c.MaxSessions > 8 {
		return errors.New("invalid shell scope or limits")
	}
	if len(c.Commands) == 0 || len(c.Commands) > 20 {
		return errors.New("shell requires 1..20 fixed command profiles")
	}
	for n, argv := range c.Commands {
		if !identifier.MatchString(n) || len(argv) == 0 || !filepath.IsAbs(argv[0]) {
			return errors.New("invalid shell command profile")
		}
	}
	return nil
}

type ticket struct {
	owner, profile string
	expires        time.Time
}
type Shell struct {
	mu       sync.Mutex
	tickets  map[string]ticket
	sessions int
}

func NewShell() *Shell { return &Shell{tickets: map[string]ticket{}} }
func (s *Shell) Issue(owner, profile string, c ShellConfig) (string, error) {
	if !c.Enabled {
		return "", errors.New("shell disabled")
	}
	if _, ok := c.Commands[profile]; !ok {
		return "", errors.New("unknown command profile")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.tickets {
		if time.Now().After(t.expires) {
			delete(s.tickets, k)
		}
	}
	if len(s.tickets) >= 64 {
		return "", errors.New("too many pending shell tickets")
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	tok := hex.EncodeToString(b)
	s.tickets[tok] = ticket{owner, profile, time.Now().Add(30 * time.Second)}
	return tok, nil
}
func SecureShellTransport(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	return e == nil && net.ParseIP(h).IsLoopback()
}
func SameOrigin(r *http.Request) bool {
	v := r.Header.Get("Origin")
	if v == "" {
		return false
	}
	u, e := url.Parse(v)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return e == nil && u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) && u.User == nil
}
func (s *Shell) Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, owner string, c ShellConfig, valid func() bool) {
	if !c.Enabled || !shellSupported() {
		http.Error(w, "shell unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != "GET" || !SameOrigin(r) || !SecureShellTransport(r) {
		http.Error(w, "invalid shell request origin", http.StatusForbidden)
		return
	}
	// Token is a WebSocket subprotocol, never a URL/query/log parameter.
	var tok string
	for _, p := range websocket.Subprotocols(r) {
		if strings.HasPrefix(p, "ticket.") {
			tok = strings.TrimPrefix(p, "ticket.")
		}
	}
	s.mu.Lock()
	t, ok := s.tickets[tok]
	if ok {
		delete(s.tickets, tok)
	}
	if !ok || t.owner != owner || time.Now().After(t.expires) || s.sessions >= c.MaxSessions {
		s.mu.Unlock()
		http.Error(w, "invalid, expired or exhausted shell ticket", http.StatusForbidden)
		return
	}
	s.sessions++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.sessions--; s.mu.Unlock() }()
	if _, ok = c.Commands[t.profile]; !ok {
		http.Error(w, "profile removed", http.StatusForbidden)
		return
	}
	up := websocket.Upgrader{CheckOrigin: SameOrigin, Subprotocols: []string{"night-watcher-shell"}, HandshakeTimeout: 5 * time.Second}
	ws, e := up.Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer ws.Close()
	ctx, cancel := context.WithTimeout(ctx, seconds(c.TimeoutSeconds))
	defer cancel()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				ws.Close()
				return
			case <-ticker.C:
				if !valid() {
					cancel()
					return
				}
			}
		}
	}()
	if e = servePTY(ctx, ws, c, c.Commands[t.profile]); e != nil {
		ws.SetWriteDeadline(time.Now().Add(time.Second))
		_ = ws.WriteMessage(websocket.TextMessage, []byte("\r\nTerminal closed: "+e.Error()))
	}
}
