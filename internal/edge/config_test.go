package edge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigStrictDecode(t *testing.T) {
	for _, s := range []string{`{"version":1,"default":"direct","typo":true}`, `{"version":2,"default":"direct"}`, `{"version":1,"default":"missing"}`, `{"version":1,"default":"direct"} {}`, strings.Repeat("x", MaxConfigSize+1)} {
		if _, e := DecodeConfig(strings.NewReader(s)); e == nil {
			t.Fatalf("accepted invalid config %.60s", s)
		}
	}
	c := DefaultConfig()
	b, _ := json.Marshal(c)
	if _, e := DecodeConfig(strings.NewReader(string(b))); e != nil {
		t.Fatal(e)
	}
}
func TestConfigRejectsUnsafeScope(t *testing.T) {
	c := DefaultConfig()
	c.Firewall.EgressInterfaces = []string{"eth0;reboot"}
	if c.Validate() == nil {
		t.Fatal("accepted injected interface")
	}
	c = DefaultConfig()
	c.Shell = ShellConfig{Enabled: true}
	if c.Validate() == nil {
		t.Fatal("accepted root shell")
	}
	c = DefaultConfig()
	c.Rules = []Rule{{CIDR: "invalid", Action: "direct"}}
	if c.Validate() == nil {
		t.Fatal("accepted invalid prefix")
	}
}

func TestConfigRejectsForwardLoop(t *testing.T) {
	c := DefaultConfig()
	c.Upstreams = []Upstream{{Name: "loop", Group: "g", Address: "127.0.0.1:8443", Kind: "forward", Weight: 1, Probe: probeConfig("127.0.0.1:8443")}}
	c.Listeners = []ListenerConfig{{Name: "in", Address: "0.0.0.0:8443", Mode: "tcp", DefaultGroup: "g"}}
	if c.Validate() == nil {
		t.Fatal("forward loop accepted")
	}
}
