package backend

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/mrmmg/openvpn-desktop/internal/analyzer"
	"github.com/mrmmg/openvpn-desktop/internal/network"
	"github.com/mrmmg/openvpn-desktop/internal/ovpn"
	"github.com/mrmmg/openvpn-desktop/internal/vpn"
)

func TestScanLog(t *testing.T) {
	lines := []string{
		"2026-01-01 10:00:00 UDPv4 link remote: [AF_INET]203.0.113.5:1194",
		"2026-01-01 10:00:01 Outgoing Data Channel: Cipher 'AES-256-GCM' initialized with 256 bit key",
		"2026-01-01 10:00:01 Outgoing Data Channel: Using 256 bit message hash 'SHA256' for HMAC authentication",
	}
	proto, cipher, auth := scanLog(lines)
	if proto != "UDP" || cipher != "AES-256-GCM" || auth != "SHA256" {
		t.Fatalf("got %q %q %q", proto, cipher, auth)
	}
	if p, c, a := scanLog(nil); p != "" || c != "" || a != "" {
		t.Fatal("empty log must yield nothing")
	}
}

func TestPushedDNSOnlyAppliedAndValid(t *testing.T) {
	cfg, _ := ovpn.Parse(strings.NewReader("client\nremote h 1\n"))
	st := analyzer.AnalyzeStatic(cfg)
	lines := []string{"PUSH: Received control message: 'PUSH_REPLY,dhcp-option DNS 10.8.0.1,dhcp-option DNS 1.1.1.1,dhcp-option DOMAIN corp,route 10.0.0.0 255.0.0.0'"}
	got := pushedDNS(analyzer.ParsePushed(lines, st))
	if len(got) != 2 || got[0] != "10.8.0.1" || got[1] != "1.1.1.1" {
		t.Fatalf("got %v", got)
	}
	cfg2, _ := ovpn.Parse(strings.NewReader("client\nremote h 1\nroute-nopull\n"))
	if got := pushedDNS(analyzer.ParsePushed(lines, analyzer.AnalyzeStatic(cfg2))); len(got) != 0 {
		t.Fatalf("route-nopull drops pushed DNS, got %v", got)
	}
}

func TestScriptRisks(t *testing.T) {
	parse := func(s string) *ovpn.Config { c, _ := ovpn.Parse(strings.NewReader(s)); return c }
	if r := scriptRisks(parse("client\nup /tmp/x.sh\n")); len(r) != 0 {
		t.Fatalf("script-security 1 does not run user scripts: %v", r)
	}
	if r := scriptRisks(parse("client\nscript-security 2\nup /tmp/x.sh\n")); len(r) != 1 {
		t.Fatalf("want one risk, got %v", r)
	}
	if r := scriptRisks(parse("client\nremote h 1\n")); len(r) != 0 {
		t.Fatalf("plain profile: %v", r)
	}
}

func TestTunnelRoutes(t *testing.T) {
	p := netip.MustParsePrefix
	routes := []network.Route{
		{Dst: p("10.8.0.0/24"), Dev: "tun0"},
		{Dst: p("192.168.1.0/24"), Dev: "eth0"},
		{Dst: p("fe80::/64"), Dev: "tun0"},
		{Dst: p("0.0.0.0/1"), Dev: "tun0", Gateway: netip.MustParseAddr("10.8.0.1")},
	}
	got := tunnelRoutes(routes, "tun0")
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestStateName(t *testing.T) {
	for p, want := range map[vpn.Phase]string{
		vpn.Connected: "connected", vpn.Connecting: "connecting", vpn.Disconnecting: "disconnecting",
		vpn.Failed: "error", vpn.Disconnected: "disconnected", vpn.Unknown: "unknown",
	} {
		if got := stateName(p); got != want {
			t.Errorf("%s: got %s", p, got)
		}
	}
}

func TestLogBufferRedactsAndBounds(t *testing.T) {
	var batches [][]LogEntry
	b := NewLogBuffer(func(name string, data any) {
		if name == EventLogs {
			batches = append(batches, data.([]LogEntry))
		}
	})
	b.AddOpenVPN(`2026-01-01 10:00:00 MANAGEMENT: CMD 'password "Auth" "hunter2"'`)
	b.Flush()
	if len(batches) != 1 || strings.Contains(batches[0][0].Message, "hunter2") {
		t.Fatalf("secret leaked: %+v", batches)
	}
	for i := 0; i < logCapacity*2; i++ {
		b.Add("app", "info", "", "x")
	}
	if n := len(b.Snapshot()); n > logCapacity {
		t.Fatalf("buffer grew to %d", n)
	}
}
