package edge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type FirewallConfig struct {
	Enabled             bool     `json:"enabled"`
	KillSwitch          bool     `json:"kill_switch"`
	PreventDNSLeaks     bool     `json:"prevent_dns_leaks"`
	BlockForwardLoops   bool     `json:"block_forward_loops"`
	EgressInterfaces    []string `json:"egress_interfaces"`
	AllowedDestinations []string `json:"allowed_destinations"`
	DNSServers          []string `json:"dns_servers"`
	IsolatedTCPPorts    []uint16 `json:"isolated_tcp_ports"`
}

var ifaceName = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,15}$`)

func (c FirewallConfig) Validate() error {
	for _, s := range c.EgressInterfaces {
		if !ifaceName.MatchString(s) {
			return errors.New("invalid firewall interface")
		}
	}
	for _, s := range c.AllowedDestinations {
		if _, e := netip.ParsePrefix(s); e != nil {
			return fmt.Errorf("firewall destination: %w", e)
		}
	}
	for _, s := range c.DNSServers {
		if _, e := netip.ParseAddr(s); e != nil {
			return e
		}
	}
	for _, p := range c.IsolatedTCPPorts {
		if p == 0 {
			return errors.New("invalid isolated port")
		}
	}
	if c.Enabled && c.KillSwitch && len(c.EgressInterfaces) == 0 && len(c.AllowedDestinations) == 0 {
		return errors.New("kill switch requires explicit egress interfaces or destinations")
	}
	return nil
}

// NftRules modifies only our dedicated inet table. Both IP families are covered.
// DNS/DoT are filtered before general egress exceptions. DoH is HTTPS and needs
// an explicit routing/egress policy, not a false claim of DNS inspection.
func NftRules(c FirewallConfig, exists bool) (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
	var b strings.Builder
	if exists {
		b.WriteString("delete table inet night_watcher_edge\n")
	}
	if !c.Enabled {
		return b.String(), nil
	}
	b.WriteString("table inet night_watcher_edge {\n chain output { type filter hook output priority -10; policy accept;\n  oifname \"lo\" accept\n")
	if c.PreventDNSLeaks {
		for _, s := range c.DNSServers {
			ip, _ := netip.ParseAddr(s)
			family := "ip"
			if ip.Is6() {
				family = "ip6"
			}
			fmt.Fprintf(&b, "  %s daddr %s meta l4proto { tcp, udp } th dport { 53, 853 } accept\n", family, ip)
		}
		b.WriteString("  meta l4proto { tcp, udp } th dport { 53, 853 } drop\n")
	}
	if c.KillSwitch {
		// Replies to inbound management connections remain usable. Existing
		// outbound sessions are still subject to the kill switch.
		b.WriteString("  ct direction reply ct state established,related accept\n  meta l4proto ipv6-icmp icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit, nd-router-advert } accept\n")
		writeEgress(&b, c)
		b.WriteString("  counter drop\n")
	}
	b.WriteString(" }\n chain input { type filter hook input priority -10; policy accept;\n")
	for _, p := range c.IsolatedTCPPorts {
		fmt.Fprintf(&b, "  iifname != \"lo\" tcp dport %d counter drop\n", p)
	}
	b.WriteString(" }\n chain forward { type filter hook forward priority -10; policy accept;\n")
	if c.BlockForwardLoops {
		for _, iface := range c.EgressInterfaces {
			fmt.Fprintf(&b, "  iifname %q oifname %q counter drop\n", iface, iface)
		}
	}
	if c.PreventDNSLeaks {
		for _, s := range c.DNSServers {
			ip, _ := netip.ParseAddr(s)
			family := "ip"
			if ip.Is6() {
				family = "ip6"
			}
			fmt.Fprintf(&b, "  %s daddr %s meta l4proto { tcp, udp } th dport { 53, 853 } accept\n", family, ip)
		}
		b.WriteString("  meta l4proto { tcp, udp } th dport { 53, 853 } drop\n")
	}

	if c.KillSwitch {
		b.WriteString("  ct direction reply ct state established,related accept\n")
		writeEgress(&b, c)
		b.WriteString("  counter drop\n")
	}
	b.WriteString(" }\n}\n")
	return b.String(), nil
}
func writeEgress(b *strings.Builder, c FirewallConfig) {
	for _, i := range c.EgressInterfaces {
		fmt.Fprintf(b, "  oifname %q accept\n", i)
	}
	for _, s := range c.AllowedDestinations {
		p, _ := netip.ParsePrefix(s)
		family := "ip"
		if p.Addr().Is6() {
			family = "ip6"
		}
		fmt.Fprintf(b, "  %s daddr %s accept\n", family, p.Masked())
	}
}

type CommandRunner func(context.Context, string, []string, []byte) ([]byte, error)

func runCommand(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return out, fmt.Errorf("%s: %w: %.2048s", name, e, out)
	}
	return out, nil
}

type Firewall struct {
	mu      sync.Mutex
	Run     CommandRunner
	applied bool
}

func (f *Firewall) Apply(ctx context.Context, c FirewallConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := c.Validate(); e != nil {
		return e
	}
	if !c.Enabled && !f.applied && runtime.GOOS != "linux" && f.Run == nil {
		return nil
	}
	if runtime.GOOS != "linux" && f.Run == nil {
		return errors.New("firewall requires Linux and nftables")
	}
	run := f.Run
	if run == nil {
		run = runCommand
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, e := run(ctx, "nft", []string{"list", "table", "inet", "night_watcher_edge"}, nil)
	exists := e == nil
	if !c.Enabled && !exists {
		if f.applied {
			return fmt.Errorf("cannot inspect previously applied firewall: %w", e)
		}
		return nil
	}
	script, e := NftRules(c, exists)
	if e != nil {
		return e
	}
	if script == "" {
		return nil
	}
	if _, e = run(ctx, "nft", []string{"--check", "-f", "-"}, []byte(script)); e != nil {
		return e
	}
	if _, e = run(ctx, "nft", []string{"-f", "-"}, []byte(script)); e != nil {
		return e
	}
	f.applied = c.Enabled
	return nil
}
