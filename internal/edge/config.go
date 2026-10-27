// Package edge provides Night Watcher's native edge control and data plane.
package edge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/syklonAK/night-watcher/internal/app"
)

const MaxConfigSize = 1 << 20

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

type Config struct {
	Version   int              `json:"version"`
	Enabled   bool             `json:"enabled"`
	Default   string           `json:"default"`
	Rules     []Rule           `json:"rules"`
	Bypass    []string         `json:"bypass"`
	Upstreams []Upstream       `json:"upstreams"`
	Listeners []ListenerConfig `json:"listeners"`
	Detect    DetectConfig     `json:"detect"`
	Firewall  FirewallConfig   `json:"firewall"`
	Shell     ShellConfig      `json:"shell"`
}
type Rule struct {
	Domain  string `json:"domain,omitempty"`
	CIDR    string `json:"cidr,omitempty"`
	Network string `json:"network,omitempty"`
	Port    uint16 `json:"port,omitempty"`
	Action  string `json:"action"`
}
type Upstream struct {
	Name    string      `json:"name"`
	Group   string      `json:"group"`
	Address string      `json:"address"`
	Kind    string      `json:"kind"` // socks5 preserves destination; forward targets a fixed backend
	Weight  int         `json:"weight"`
	Probe   ProbeConfig `json:"probe"`
}
type ProbeConfig struct {
	Kind            string `json:"kind"`
	Target          string `json:"target"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	Failures        int    `json:"failures"`
	Successes       int    `json:"successes"`
}
type DetectConfig struct {
	PublicIPv4URL string `json:"public_ipv4_url,omitempty"`
	PublicIPv6URL string `json:"public_ipv6_url,omitempty"`
	PathTarget    string `json:"path_target,omitempty"`
}
type ListenerConfig struct {
	Name         string         `json:"name"`
	Address      string         `json:"address"`
	Mode         string         `json:"mode"` // tcp, tls-passthrough, http, https
	DefaultGroup string         `json:"default_group"`
	Routes       []InboundRoute `json:"routes"`
	CertFile     string         `json:"cert_file,omitempty"`
	KeyFile      string         `json:"key_file,omitempty"`
}
type InboundRoute struct {
	Host  string `json:"host"`
	Path  string `json:"path,omitempty"`
	Group string `json:"group"`
}

func DefaultConfig() Config {
	return Config{Version: 1, Default: "direct", Detect: DetectConfig{PublicIPv4URL: "https://api.ipify.org", PublicIPv6URL: "https://api6.ipify.org"}}
}
func DecodeConfig(r io.Reader) (Config, error) {
	var c Config
	b, e := io.ReadAll(io.LimitReader(r, MaxConfigSize+1))
	if e != nil {
		return c, e
	}
	if len(b) > MaxConfigSize {
		return c, errors.New("edge config exceeds 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("expected one JSON document")
	}
	return c, c.Validate()
}
func addressValid(s string) bool {
	h, p, e := net.SplitHostPort(s)
	n, pe := strconv.Atoi(p)
	return e == nil && pe == nil && h != "" && n > 0 && n <= 65535
}
func (c Config) Validate() error {
	if c.Version != 1 {
		return errors.New("unsupported edge config version")
	}
	if len(c.Rules) > 10000 || len(c.Bypass) > 10000 || len(c.Upstreams) > 128 || len(c.Listeners) > 64 {
		return errors.New("edge table limit exceeded")
	}
	groups := map[string]bool{"direct": true, "block": true}
	forwardGroups := map[string]bool{}
	names := map[string]bool{}
	for _, u := range c.Upstreams {
		if !identifier.MatchString(u.Name) || names[u.Name] || !identifier.MatchString(u.Group) || u.Group == "direct" || u.Group == "block" {
			return errors.New("invalid or duplicate upstream name/group")
		}
		names[u.Name] = true
		groups[u.Group] = true
		if !addressValid(u.Address) {
			return fmt.Errorf("invalid upstream address: %s", u.Name)
		}
		if u.Kind != "socks5" && u.Kind != "forward" {
			return errors.New("upstream kind must be socks5 or forward")
		}
		if u.Kind == "forward" {
			forwardGroups[u.Group] = true
		}
		if u.Weight < 1 || u.Weight > 1000 {
			return errors.New("weight must be 1..1000")
		}
		if e := u.Probe.Validate(); e != nil {
			return fmt.Errorf("upstream %s: %w", u.Name, e)
		}
	}
	if !groups[c.Default] {
		return errors.New("unknown default action")
	}
	for _, r := range c.Rules {
		if !groups[r.Action] {
			return errors.New("unknown rule action")
		}
		if r.Network != "" && r.Network != "tcp" && r.Network != "udp" {
			return errors.New("network must be tcp or udp")
		}
		if r.Domain != "" && r.CIDR != "" {
			return errors.New("rule selects domain OR CIDR")
		}
		if r.Domain != "" && !validDomain(r.Domain) {
			return errors.New("invalid rule domain")
		}
		if r.CIDR != "" {
			if _, e := netip.ParsePrefix(r.CIDR); e != nil {
				return e
			}
		}
	}
	for _, p := range c.Bypass {
		if _, e := netip.ParsePrefix(p); e != nil {
			return fmt.Errorf("bypass: %w", e)
		}
	}
	names = map[string]bool{}
	addresses := map[string]bool{}
	for _, l := range c.Listeners {
		if !identifier.MatchString(l.Name) || names[l.Name] || addresses[l.Address] || !addressValid(l.Address) {
			return errors.New("invalid or duplicate listener")
		}
		names[l.Name] = true
		addresses[l.Address] = true
		if l.Mode != "tcp" && l.Mode != "tls-passthrough" && l.Mode != "http" && l.Mode != "https" {
			return errors.New("unknown listener mode")
		}
		if !forwardGroups[l.DefaultGroup] {
			return errors.New("inbound default requires a forward group")
		}
		if l.Mode == "https" && (!filepath.IsAbs(l.CertFile) || !filepath.IsAbs(l.KeyFile)) {
			return errors.New("https listener requires absolute certificate paths")
		}
		for _, r := range l.Routes {
			if (r.Host != "*" && !validDomain(r.Host)) || !forwardGroups[r.Group] || (r.Path != "" && !strings.HasPrefix(r.Path, "/")) {
				return errors.New("invalid inbound route")
			}
		}
	}
	for _, s := range []string{c.Detect.PublicIPv4URL, c.Detect.PublicIPv6URL} {
		if s != "" {
			u, e := url.Parse(s)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return errors.New("public IP resolver requires HTTPS")
			}
		}
	}
	if c.Detect.PathTarget != "" {
		if _, e := netip.ParseAddr(c.Detect.PathTarget); e != nil {
			return errors.New("path target must be a literal IP")
		}
	}
	if e := c.Firewall.Validate(); e != nil {
		return e
	}
	for _, u := range c.Upstreams {
		uh, up, _ := net.SplitHostPort(u.Address)
		for _, l := range c.Listeners {
			lh, lp, _ := net.SplitHostPort(l.Address)
			if up != lp {
				continue
			}
			uip, lip := net.ParseIP(uh), net.ParseIP(lh)
			if uh == lh || (uip != nil && uip.IsLoopback() && (lip != nil && (lip.IsUnspecified() || lip.IsLoopback()))) || (uh == "localhost" && lip != nil && (lip.IsUnspecified() || lip.IsLoopback())) {
				return errors.New("upstream points back to an edge listener")
			}
		}
	}
	return c.Shell.Validate()
}
func (p ProbeConfig) Validate() error {
	if p.IntervalSeconds < 1 || p.IntervalSeconds > 3600 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 30 || p.Failures < 1 || p.Failures > 20 || p.Successes < 1 || p.Successes > 20 {
		return errors.New("invalid health probe timing or thresholds")
	}
	switch p.Kind {
	case "tcp":
		if !addressValid(p.Target) {
			return errors.New("invalid TCP probe address")
		}
	case "icmp":
		if _, e := netip.ParseAddr(p.Target); e != nil {
			return e
		}
	case "http":
		u, e := url.Parse(p.Target)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return errors.New("invalid HTTP probe URL")
		}
	default:
		return errors.New("probe kind must be tcp, http or icmp")
	}
	return nil
}
func LoadConfig(path string) (Config, error) {
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return DefaultConfig(), nil
	}
	if e != nil {
		return Config{}, e
	}
	defer f.Close()
	return DecodeConfig(f)
}
func SaveConfig(path string, c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	return app.WriteFileAtomic(path, b, 0600)
}
func configPath(dir string) string { return filepath.Join(dir, "edge.json") }
func seconds(n int) time.Duration  { return time.Duration(n) * time.Second }
