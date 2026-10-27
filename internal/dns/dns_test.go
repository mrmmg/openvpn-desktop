package dns

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrmmg/openvpn-wails/internal/network"
)

type routes map[string]string // addr -> dev

func (r routes) RouteGet(_ context.Context, dst netip.Addr) network.RouteDecision {
	return network.RouteDecision{Dst: dst, Dev: r[dst.String()]}
}

func TestParseResolvConf(t *testing.T) {
	ns, search := ParseResolvConf("# comment\nnameserver 1.1.1.1\nnameserver fe80::1%eth0\nsearch lan corp\n; x\nnameserver bogus\n")
	if len(ns) != 2 || ns[1].String() != "fe80::1" || strings.Join(search, ",") != "lan,corp" {
		t.Fatalf("%v %v", ns, search)
	}
}

func TestParseResolvectl(t *testing.T) {
	out := "Global: 9.9.9.9#dns.quad9.net\nLink 2 (wlan0): 192.168.1.1 fe80::1%wlan0\nLink 9 (tun0): 10.8.0.1 [2001:db8::53]:53\n"
	servers := ParseResolvectlDNS(out)
	got := map[string]string{}
	for _, s := range servers {
		got[s.Addr.String()] = s.Link
	}
	if got["9.9.9.9"] != "" || got["192.168.1.1"] != "wlan0" || got["10.8.0.1"] != "tun0" || got["2001:db8::53"] != "tun0" || len(servers) != 5 {
		t.Fatalf("%v", got)
	}
	doms := ParseResolvectlLinks("Global:\nLink 9 (tun0): ~.\nLink 2 (wlan0): lan\n")
	if doms["tun0"][0] != "~." || doms["wlan0"][0] != "lan" {
		t.Fatalf("%v", doms)
	}
}

func TestReaderWithResolved(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, "resolv.conf")
	_ = os.WriteFile(rc, []byte("nameserver 127.0.0.53\n"), 0o644)
	r := &Reader{ResolvConfPath: rc, Resolvectl: func(_ context.Context, args ...string) (string, error) {
		if args[0] == "dns" {
			return "Link 2 (wlan0): 192.168.1.1\nLink 9 (tun0): 10.8.0.1\n", nil
		}
		return "Link 9 (tun0): ~.\n", nil
	}}
	c, err := r.Read(context.Background())
	if err != nil || !c.StubLocal || !c.Resolved || !c.DefaultLink["tun0"] || len(c.Servers) != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	// The LAN server is only used for its own domain while tun0 owns "~.".
	leak := CheckLeak(context.Background(), c, "tun0", true, routes{"192.168.1.1": "wlan0"})
	if leak.Verdict != VerdictOK {
		t.Fatalf("verdict %s %v", leak.Verdict, leak.Reasons)
	}
}

func TestCheckLeak(t *testing.T) {
	ctx := context.Background()
	plain := Config{Servers: []Server{{Addr: netip.MustParseAddr("192.168.1.1")}, {Addr: netip.MustParseAddr("10.8.0.1")}}}
	rg := routes{"192.168.1.1": "wlan0", "10.8.0.1": "tun0"}
	if l := CheckLeak(ctx, plain, "tun0", true, rg); l.Verdict != VerdictWarning || !strings.Contains(l.Reasons[0], "192.168.1.1") {
		t.Fatalf("expected warning: %+v", l)
	}
	ok := Config{Servers: []Server{{Addr: netip.MustParseAddr("10.8.0.1")}}}
	l := CheckLeak(ctx, ok, "tun0", true, rg)
	if l.Verdict != VerdictOK {
		t.Fatalf("expected ok: %+v", l)
	}
	if !strings.Contains(strings.Join(l.Reasons, " "), "heuristic") {
		t.Fatal("the result must state that it is a heuristic")
	}
	if CheckLeak(ctx, ok, "", true, rg).Verdict != VerdictNA {
		t.Fatal("no VPN must be N/A")
	}
	if CheckLeak(ctx, Config{}, "tun0", true, rg).Verdict != VerdictUnknown {
		t.Fatal("no servers must be unknown")
	}
	stub := Config{StubLocal: true, Servers: []Server{{Addr: netip.MustParseAddr("127.0.0.53")}}}
	if CheckLeak(ctx, stub, "tun0", true, rg).Verdict != VerdictUnknown {
		t.Fatal("unknown upstream of a local stub must not be reported as OK")
	}
}
