package webui

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"

	"github.com/syklonAK/night-watcher/internal/edge"
)

//go:embed assets/edge.html
var edgeHTML []byte

func (s *server) registerEdge(mux *http.ServeMux) {
	mux.HandleFunc("/edge", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(edgeHTML)
	}))
	mux.HandleFunc("/api/edge/metrics", s.requireReadAuth(s.handleEdgeMetrics))
	mux.HandleFunc("/api/edge/state", s.requireReadAuth(s.handleEdgeState))
	mux.HandleFunc("/api/edge/config", s.requireAuth(s.handleEdgeConfig))
	mux.HandleFunc("/api/edge/reload", s.requireAuth(s.handleEdgeReload))
	mux.HandleFunc("/api/edge/backup", s.requireAuth(s.handleEdgeBackup))
	mux.HandleFunc("/api/edge/restore", s.requireAuth(s.handleEdgeRestore))
	mux.HandleFunc("/api/edge/firewall/preview", s.requireAuth(s.handleEdgeFirewallPreview))
	mux.HandleFunc("/api/shell/token", s.requireAuth(s.handleShellToken))
	mux.HandleFunc("/api/shell/ws", s.requireAuth(s.handleShellWS))
}
func (s *server) edgeReady(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.edge == nil {
		http.Error(w, "edge service unavailable", 503)
		return false
	}
	return true
}
func edgeMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		http.Error(w, "method not allowed", 405)
		return false
	}
	return true
}
func edgeMutation(w http.ResponseWriter, r *http.Request) bool {
	if !edgeMethod(w, r, "POST") {
		return false
	}
	if !edge.SameOrigin(r) {
		http.Error(w, "same-origin request required", 403)
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "application/json required", 415)
		return false
	}
	return true
}
func (s *server) handleEdgeMetrics(w http.ResponseWriter, r *http.Request) {
	if !edgeMethod(w, r, "GET") || !s.edgeReady(w) {
		return
	}
	state := s.edge.Snapshot()
	if r.URL.Query().Get("format") == "prometheus" {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		edge.WritePrometheus(w, state.Metrics, state.Nodes)
		return
	}
	writeJSON(w, state.Metrics)
}
func (s *server) handleEdgeState(w http.ResponseWriter, r *http.Request) {
	if !edgeMethod(w, r, "GET") || !s.edgeReady(w) {
		return
	}
	writeJSON(w, s.edge.Snapshot())
}
func (s *server) handleEdgeConfig(w http.ResponseWriter, r *http.Request) {
	if !s.edgeReady(w) {
		return
	}
	if r.Method == "GET" {
		writeJSON(w, s.edge.Config())
		return
	}
	if !edgeMutation(w, r) {
		return
	}
	c, e := edge.DecodeConfig(http.MaxBytesReader(w, r.Body, edge.MaxConfigSize))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	s.saveEdge(w, r, c)
}
func (s *server) saveEdge(w http.ResponseWriter, r *http.Request, c edge.Config) {
	// Shell scope is provisioned locally. A panel configuration restore cannot
	// turn a harmless diagnostic profile into a command with elevated scope.
	if !reflect.DeepEqual(c.Shell, s.edge.Config().Shell) {
		http.Error(w, "shell scope must be provisioned in the local edge.json file", 403)
		return
	}
	if e := s.edge.Save(c); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	log.Printf("edge configuration updated by session %s", s.edgeOwner(r))
	writeJSON(w, map[string]any{"ok": true, "generation": s.edge.Snapshot().Generation})
}
func (s *server) handleEdgeReload(w http.ResponseWriter, r *http.Request) {
	if !edgeMutation(w, r) || !s.edgeReady(w) {
		return
	}
	if e := s.edge.Reload(); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
func (s *server) handleEdgeBackup(w http.ResponseWriter, r *http.Request) {
	if !edgeMethod(w, r, "GET") || !s.edgeReady(w) {
		return
	}
	b, e := s.edge.Backup()
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="night-watcher-edge.json"`)
	w.Write(b)
}
func (s *server) handleEdgeRestore(w http.ResponseWriter, r *http.Request) {
	if !edgeMutation(w, r) || !s.edgeReady(w) {
		return
	}
	c, e := edge.DecodeConfig(http.MaxBytesReader(w, r.Body, edge.MaxConfigSize))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	s.saveEdge(w, r, c)
}
func (s *server) handleEdgeFirewallPreview(w http.ResponseWriter, r *http.Request) {
	if !edgeMutation(w, r) || !s.edgeReady(w) {
		return
	}
	var c edge.FirewallConfig
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	d.DisallowUnknownFields()
	if e := d.Decode(&c); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	script, e := edge.NftRules(c, true)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	writeJSON(w, map[string]string{"nftables": script})
}
func (s *server) edgeOwner(r *http.Request) string {
	c, e := r.Cookie(sessionCookie)
	if e != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(c.Value)))
}
func (s *server) handleShellToken(w http.ResponseWriter, r *http.Request) {
	if !edgeMutation(w, r) || !s.edgeReady(w) {
		return
	}
	if !edge.SecureShellTransport(r) {
		http.Error(w, "terminal requires HTTPS or a loopback SSH forward", 403)
		return
	}
	var req struct {
		Profile string `json:"profile"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid profile request", 400)
		return
	}
	token, e := s.edge.Shell.Issue(s.edgeOwner(r), req.Profile, s.edge.Config().Shell)
	if e != nil {
		http.Error(w, e.Error(), 403)
		return
	}
	writeJSON(w, map[string]string{"token": token})
}
func (s *server) handleShellWS(w http.ResponseWriter, r *http.Request) {
	if !s.edgeReady(w) {
		return
	}
	cookie, _ := r.Cookie(sessionCookie)
	scope := s.edge.Config().Shell
	owner := s.edgeOwner(r)
	log.Printf("edge terminal opening for session %s", owner)
	defer log.Printf("edge terminal closed for session %s", owner)
	ctx := s.edge.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	s.edge.Shell.Serve(ctx, w, r, owner, scope, func() bool {
		return cookie != nil && s.sessions.valid(cookie.Value) && reflect.DeepEqual(scope, s.edge.Config().Shell)
	})
}
