# Night Watcher native edge

The seven edge modules run in Go. The binary does not launch Python, Nginx,
HAProxy, UFW, or wattun-manager. Linux firewall administration uses the `nft`
system utility; the terminal launches only locally configured command profiles.
The base repository already requires **Go 1.24.4 or later**. Lowering its go.mod
to 1.22 would break dependencies and standard library APIs.

## Build and test

```sh
CGO_ENABLED=0 go build -v -o night-watcher main.go
CGO_ENABLED=0 go test ./... -timeout 10m
go test -race ./internal/edge ./internal/webui ./internal/socks
```

The default Linux Packet transport uses native AF_PACKET sockets and requires
CAP_NET_RAW/root at runtime. It needs no C compiler or libpcap development
headers. To retain the original libpcap backend, use
`CGO_ENABLED=1 go build -tags pcap -o night-watcher main.go` with libpcap installed.
Linux amd64/arm64 releases are built with CGO disabled.

## Process ownership and startup

The `--webui` process owns edge listeners, the firewall table, and the terminal.
It opens `/edge` from the existing authenticated dashboard. The engine (`-c`)
and `--proxy` processes load the same `/etc/night-watcher/edge.json` and use its
outbound policy for the built-in SOCKS5 and HTTP exit proxies. Existing transport
uplinks and fixed tunnel port mappings retain their own transport configuration;
they are not redirected recursively through the edge proxy. An edge `forward`
upstream can target a tunnel's exposed local port.

Keep the web panel service running when using edge ingress listeners. Stopping
it closes ingress listeners and terminal sessions. **Firewall isolation stays
installed on shutdown**, including after a crash; stopping the panel must not
silently defeat a kill switch. Disable `firewall.enabled` and apply the config
to deliberately remove its table while the owning process is running. For
out-of-band recovery, an administrator can run
`nft delete table inet night_watcher_edge` on the Linux host.

Missing edge.json starts with outbound policy disabled. Invalid JSON/schema
fails initial edge startup. Invalid reloads preserve the working configuration
and appear as `last_error`. Config changes are detected every two seconds;
SIGHUP immediately requests a reload, both for edge.json and for the running
tunnel's TOML. New connections use new routes; existing relays retain their
connections. Adding/removing listener addresses is supported. Changing the mode
of a listener on an occupied address requires stopping that listener first.

## Example configuration

Copy `examples/edge.json` into `/etc/night-watcher/edge.json`, adapt its local
upstream addresses and enable the features you need. CIDR bypass lists are
operator supplied: the program does not ship a guessed or stale domestic IP list.

Outbound rule precedence is explicit rules in array order, then bypass CIDRs,
then `default`. Actions are `direct`, `block`, or an upstream group. A domain
matches exactly; `*.example.com` matches subdomains, not the apex. CIDR matching
uses literal destination IPs; domains are matched before DNS. SOCKS5-routed
domains are resolved by the upstream proxy to avoid local DNS leakage.

`kind: socks5` supports TCP destinations, including HTTP CONNECT. UDP uses
direct egress or a fixed `kind: forward` backend; unsupported SOCKS5 upstream
UDP fails explicitly instead of leaking through direct egress. A forward group
targets a fixed service, so only route compatible traffic to it.

Inbound listeners support:

* `tcp`: fixed TCP service forwarding through a healthy group.
* `tls-passthrough`: inspect bounded TLS ClientHello/SNI and replay the exact
  bytes to the backend, without TLS termination.
* `http`: Host/path routing and HTTP/WebSocket reverse proxy to HTTP backends.
* `https`: the same routing after local TLS termination, with local certificate
  and key files. Backends receive HTTP, not an automatically guessed TLS mode.

Inbound groups require `forward` nodes. Routes use first match; place narrower
paths before broad paths. This implementation does not translate arbitrary
Nginx directives or implement a native gRPC/h2 upstream proxy.

Health probes use TCP connect, HTTP HEAD (2xx/3xx), or raw IPv4/IPv6 ICMP. A node
starts unavailable until its success threshold is met. Consecutive failures
remove it. Smooth weighted round-robin adjusts configured weights by measured
RTT. Connection failures retry other healthy nodes within a shared 15-second
budget. No healthy upstream means failure, never automatic direct egress.

## Detection and metrics

Startup probes the kernel-selected WAN addresses and interface MTUs. Optional
HTTPS services resolve public IPv4/IPv6. The defaults for a missing config are
api.ipify.org and api6.ipify.org; set both URLs empty for offline operation.
Detection is refreshed every five minutes with an eight-second overall budget.
An optional literal `path_target` measures ICMP RTT and the largest acknowledged
unfragmented packet up to 1500 bytes. It requires raw socket permission. ICMP
filtering, loss and timeouts can limit the result: unavailable data and errors
are reported explicitly, never invented as successful measurements. This is a
reachability/PMTU probe, not a hop-by-hop traceroute.

`/api/edge/metrics` returns JSON. `/api/edge/metrics?format=prometheus` and the
existing `/metrics` return Prometheus text exposition. Prometheus consumes the
text endpoint, not raw JSON. Use the panel's read-only remote bearer token.

Metrics have explicit scopes:

* `process`: atomic counters for this process's edge relays, active TCP/UDP,
  packets and policy/write drops. Flows rejected by routing policy
  (`rejected_flows`, blocked or no healthy upstream) are counted separately
  from packet drops.
* `interfaces`: OS byte/packet/drop counters and sampled bytes/second.
* `host_tcp` / `host_udp`: established host TCP sockets and host UDP sockets.
* `tunnels`: the existing per-engine metrics snapshots, sampled every two
  seconds, rates and stale flags. KCP loss/retransmission statistics remain
  available in each tunnel's `kcp` object, and `loss_percent` reports KCP
  loss as a percentage. Host counters are not attributed to
  individual tunnels, and TCP byte counters are not mislabeled as packet loss.
* `state.nodes`: health checks, RTT, effective weights and last errors.

## Firewall scope

Only the owned `inet night_watcher_edge` table is changed. `nft --check` validates
before an atomic batch replacement. There is no shell interpolation or global
flush. IPv4 and IPv6 are covered. The available policies are:

* kill switch with explicit interface/CIDR egress exceptions;
* outbound/forward DNS and DoT allowlisting on TCP/UDP 53 and 853;
* non-loopback TCP-port isolation;
* same-interface forwarding loop prevention on configured egress interfaces.

Replies to established inbound management connections are allowed by the kill
switch. Existing outbound connections are still filtered. DoH is HTTPS and
requires an appropriate HTTPS/egress policy; port filtering does not identify it.
Preview the complete rules from the UI before applying a restrictive config.
Other firewall tables remain effective and can still block traffic accepted by
this table. Privileged firewall/AF_PACKET tests must run on Linux; mocks verify
the command plan on other operating systems.

## Terminal provisioning

The terminal is **disabled by default**. Provision `shell` locally and reload:

```json
{
  "enabled": true,
  "uid": 1001,
  "gid": 1001,
  "directory": "/home/nw-operator",
  "commands": {"identity": ["/usr/bin/id"], "shell": ["/bin/bash", "--noprofile", "--norc"]},
  "timeout_seconds": 300,
  "idle_seconds": 60,
  "max_sessions": 2
}
```

The UID/GID and directory must exist and be accessible. A dedicated account
defines the OS permission boundary; a PTY is not a container sandbox. Choosing
an interactive shell gives that account its normal command capabilities. The
panel cannot change command profiles, credentials or working directory via
config/restore endpoints. Shell scope changes revoke open terminal sessions.

The browser obtains a 256-bit, session-bound, one-use 30-second ticket from
`POST /api/shell/token` and presents it as WebSocket subprotocol `ticket.<token>`
alongside `night-watcher-shell`. Tokens are never placed in URLs. The socket
requires the logged-in panel session and an exact same Origin. HTTPS or a
loopback SSH-forwarded connection is required for terminal access. Revocation,
logout, idle timeout, hard timeout, disconnect and daemon cancellation stop the
PTY process group. Binary messages carry terminal input/output; text messages
resize the terminal with `{"cols":100,"rows":30}`. Frames and concurrent sessions
are bounded. The embedded simple console displays text; full-screen ANSI apps
need an external compatible terminal frontend.

## API and backup

| Endpoint | Method | Access |
|---|---|---|
| `/api/edge/metrics`, `/api/edge/state` | GET | Session or read-only bearer token |
| `/api/edge/config` | GET / POST | Admin session |
| `/api/edge/reload` | POST | Admin session |
| `/api/edge/backup` | GET | Admin session |
| `/api/edge/restore` | POST | Admin session |
| `/api/edge/firewall/preview` | POST | Admin session |
| `/api/shell/token` | POST | Admin session, configured profile |
| `/api/shell/ws` | GET upgrade | Session, same origin, one-use ticket |

Mutation endpoints require same-origin JSON, strict schema and bounded request
bodies. Edge backups contain edge config only. Existing panel backup endpoints
retain their full application scope. Config files are atomically written with
0600 permissions. Certificates and private keys referenced by path are not
embedded in an edge-only backup.

## Donor mapping and compatibility

The supplied watrouter Python daemon shells out to Nginx/HAProxy/UFW and a
separate WatTunnel binary. Its shell job system starts external installers,
systemctl, curl and wattun-manager. Those external runtimes are not embedded or
invoked here. Requested functions are implemented as native edge detection,
metrics, router/listeners, balancer, nft controller, authenticated PTY, and
lifecycle APIs. Night Watcher's existing tunnel-management APIs handle the base
project's supported transports.

The donor archive does **not** contain WatTunnel's transport implementation.
`wat1:` tokens, WatTunnel YAML/key files, carrier auto-selection and Cloudflare
ECH tuning are therefore not silently treated as compatible Night Watcher
transports. Donor Nginx/HAProxy text configs and state.json require explicit
conversion to the documented JSON schema. An installation of the donor
project also needs deliberate migration to the new `/etc/night-watcher` paths
and service names; the rebrand does not overwrite an existing installation
automatically.
