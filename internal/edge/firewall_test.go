package edge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFirewallIsolationPlan(t *testing.T) {
	c := FirewallConfig{Enabled: true, KillSwitch: true, PreventDNSLeaks: true, BlockForwardLoops: true, EgressInterfaces: []string{"wg0"}, AllowedDestinations: []string{"192.0.2.9/32", "2001:db8::/64"}, DNSServers: []string{"1.1.1.1", "2606:4700:4700::1111"}, IsolatedTCPPorts: []uint16{1080}}
	s, e := NftRules(c, true)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"delete table inet night_watcher_edge", "ip6 daddr 2001:db8::/64", "th dport { 53, 853 } drop", `iifname "wg0" oifname "wg0" counter drop`, `iifname != "lo" tcp dport 1080 counter drop`} {
		if !strings.Contains(s, v) {
			t.Fatalf("missing %s", v)
		}
	}
	if strings.Contains(s, "flush ruleset") {
		t.Fatal("global firewall mutation")
	}
	if strings.Count(s, "th dport { 53, 853 } drop") != 2 {
		t.Fatal("forward DNS leak")
	}
}
func TestFirewallCheckFailureNeverApplies(t *testing.T) {
	var calls []string
	f := Firewall{Run: func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "--check" {
			return nil, errors.New("invalid")
		}
		return nil, nil
	}}
	if e := f.Apply(context.Background(), FirewallConfig{Enabled: true}); e == nil {
		t.Fatal("ignored check failure")
	}
	if len(calls) != 2 || f.applied {
		t.Fatalf("applied failed plan: %v", calls)
	}
}

func TestDisabledFirewallRemovesPreviousProcessTable(t *testing.T) {
	var scripts []string
	f := Firewall{Run: func(_ context.Context, _ string, args []string, body []byte) ([]byte, error) {
		if args[0] == "-f" {
			scripts = append(scripts, string(body))
		}
		return nil, nil
	}}
	if e := f.Apply(context.Background(), FirewallConfig{}); e != nil {
		t.Fatal(e)
	}
	if len(scripts) != 1 || scripts[0] != "delete table inet night_watcher_edge\n" {
		t.Fatalf("stale owned table not removed: %v", scripts)
	}
}
