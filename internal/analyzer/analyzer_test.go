package analyzer

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/mrmmg/openvpn-desktop/internal/network"
	"github.com/mrmmg/openvpn-desktop/internal/ovpn"
	"github.com/mrmmg/openvpn-desktop/internal/state"
)

func cfg(t *testing.T, s string) *ovpn.Config {
	t.Helper()
	c, err := ovpn.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStaticModes(t *testing.T) {
	cases := []struct {
		name   string
		conf   string
		v4, v6 Mode
	}{
		{"redirect-gateway def1", "client\nredirect-gateway def1\n", ModeFull, ModeServer},
		{"redirect-gateway ipv6", "client\nredirect-gateway def1 ipv6\n", ModeFull, ModeFull},
		{"redirect-gateway !ipv4 ipv6", "client\nredirect-gateway ipv6 !ipv4\n", ModeServer, ModeFull},
		{"no redirect, pulls", "client\n", ModeServer, ModeServer},
		{"route-nopull only", "client\nroute-nopull\n", ModeNone, ModeNone},
		{"route-nopull + routes", "client\nroute-nopull\nroute 10.0.0.0 255.0.0.0\nroute-ipv6 fd00::/8\n", ModeSplit, ModeSplit},
		{"route-nopull + local redirect", "client\nroute-nopull\nredirect-gateway def1\n", ModeFull, ModeNone},
		{"def1-style routes", "client\nroute 0.0.0.0 128.0.0.0\nroute 128.0.0.0 128.0.0.0\n", ModeFull, ModeServer},
		{"default route", "client\nroute 0.0.0.0 0.0.0.0\n", ModeFull, ModeServer},
		{"default via net_gateway is not full", "tls-client\nroute 0.0.0.0 0.0.0.0 net_gateway\n", ModeNone, ModeNone},
		{"ipv6 2000::/3", "client\nroute-ipv6 2000::/3\n", ModeServer, ModeFull},
		{"block-ipv6", "client\nredirect-gateway def1\nblock-ipv6\n", ModeFull, ModeBlocked},
		{"static key p2p with routes", "secret s.key\nroute 192.168.0.0 255.255.0.0\n", ModeSplit, ModeNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := AnalyzeStatic(cfg(t, c.conf))
			if s.IPv4.Mode != c.v4 || s.IPv6.Mode != c.v6 {
				t.Errorf("got v4=%s v6=%s, want v4=%s v6=%s (reasons %v / %v)", s.IPv4.Mode, s.IPv6.Mode, c.v4, c.v6, s.IPv4.Reasons, s.IPv6.Reasons)
			}
			if len(s.IPv4.Reasons) == 0 {
				t.Error("verdict without reason")
			}
		})
	}
}

func TestDetection(t *testing.T) {
	s := AnalyzeStatic(cfg(t, "client\nredirect-gateway def1 bypass-dhcp\nroute-nopull\ndhcp-option DNS 10.0.0.1\n"))
	if s.RedirectGateway == nil || s.RedirectGateway.Arg(1) != "bypass-dhcp" {
		t.Error("redirect-gateway not detected")
	}
	if !s.RouteNoPull {
		t.Error("route-nopull not detected")
	}
	if len(s.DNS) != 1 || s.DNS[0] != "10.0.0.1" {
		t.Errorf("DNS = %v", s.DNS)
	}
	if len(s.Notes) == 0 {
		t.Error("expected notes about route-nopull")
	}
}

func TestPullFilter(t *testing.T) {
	filters := []PullFilter{{Action: "accept", Text: "route 10."}, {Action: "ignore", Text: "route"}, {Action: "reject", Text: "dhcp-option"}}
	cases := map[string]string{
		"route 10.1.0.0 255.255.0.0":    "accept",
		"route 192.168.0.0 255.255.0.0": "ignore",
		"route-ipv6 ::/0":               "ignore", // prefix match like OpenVPN
		"dhcp-option DNS 1.1.1.1":       "reject",
		"redirect-gateway def1":         "accept",
	}
	for opt, want := range cases {
		if got := FilterDecision(filters, opt); got != want {
			t.Errorf("FilterDecision(%q) = %s, want %s", opt, got, want)
		}
	}
	s := AnalyzeStatic(cfg(t, "client\npull-filter ignore redirect-gateway\n"))
	if s.PushedGatewayAccepted {
		t.Error("pushed redirect-gateway should be ignored")
	}
	if s.AcceptsPushed("redirect-gateway def1") || !s.AcceptsPushed("route 10.0.0.0 255.0.0.0") {
		t.Error("AcceptsPushed mismatch")
	}
	nopull := AnalyzeStatic(cfg(t, "client\nroute-nopull\n"))
	for _, opt := range []string{"route 10.0.0.0 255.0.0.0", "redirect-gateway def1", "dhcp-option DNS 1.1.1.1", "dns server 0 address 1.1.1.1"} {
		if nopull.AcceptsPushed(opt) {
			t.Errorf("route-nopull should drop %q", opt)
		}
	}
	if !nopull.AcceptsPushed("ping 10") {
		t.Error("route-nopull must keep non-route options")
	}
}

func TestOverrides(t *testing.T) {
	ov := state.Overrides{RouteNoPull: true, IgnorePushedGateway: true, BlockIPv6: true}
	args := OverrideArgs(ov)
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "--route-nopull --pull-filter ignore redirect-gateway") || !strings.HasSuffix(joined, "--block-ipv6") {
		t.Fatalf("args = %q", joined)
	}
	if len(OverrideArgs(state.Overrides{})) != 0 {
		t.Fatal("zero overrides must add no args")
	}
	// Ignoring the pushed gateway turns "server decides" into split for a
	// config that would otherwise accept a pushed redirect-gateway.
	base := cfg(t, "client\n")
	s := AnalyzeStatic(base.Append(OverrideDirectives(state.Overrides{IgnorePushedGateway: true})...))
	if s.PushedGatewayAccepted || s.AcceptsPushed("route 0.0.0.0 128.0.0.0") || s.AcceptsPushed("route-ipv6 2000::/3") {
		t.Fatal("gateway filters ineffective")
	}
	if !s.AcceptsPushed("route 10.8.0.0 255.255.0.0") {
		t.Fatal("specific pushed routes must still be accepted")
	}
	// An earlier "accept" in the profile wins over appended filters.
	s = AnalyzeStatic(cfg(t, "client\npull-filter accept redirect-gateway\n").Append(OverrideDirectives(state.Overrides{IgnorePushedGateway: true})...))
	if !s.PushedGatewayAccepted {
		t.Fatal("profile accept filter should take precedence")
	}
	full := AnalyzeStatic(cfg(t, "client\nroute-nopull\n").Append(OverrideDirectives(state.Overrides{ForceFullTunnel: true})...))
	if full.IPv4.Mode != ModeFull || full.IPv6.Mode != ModeFull {
		t.Fatalf("force full tunnel: %s %s", full.IPv4.Mode, full.IPv6.Mode)
	}
}

const pushLog = `2026-10-04 10:00:00 SENT CONTROL [server]: 'PUSH_REQUEST' (status=1)
2026-10-04 10:00:00 PUSH: Received control message: 'PUSH_REPLY,route 10.50.0.0 255.255.0.0,push-continuation 2'
2026-10-04 10:00:00 PUSH: Received control message: 'PUSH_REPLY,redirect-gateway def1,dhcp-option DNS 10.99.0.1,ifconfig 10.99.0.10 255.255.255.0,push-continuation 1'
2026-10-04 10:00:00 Pushed option removed by filter: 'block-outside-dns'
2026-10-04 10:00:01 Initialization Sequence Completed`

func TestParsePushed(t *testing.T) {
	base := cfg(t, "client\n")
	st := AnalyzeStatic(base)
	p := ParsePushed(strings.Split(pushLog, "\n"), st)
	if !p.Received || len(p.Options) != 4 {
		t.Fatalf("pushed = %+v", p)
	}
	if len(p.Filtered) != 1 {
		t.Fatalf("filtered=%v", p.Filtered)
	}
	eff := Effective(base, p)
	if eff.IPv4.Mode != ModeFull {
		t.Fatalf("effective v4 = %s", eff.IPv4.Mode)
	}
	if eff.IPv6.Mode != ModeNone {
		t.Fatalf("effective v6 = %s (server pushed no IPv6)", eff.IPv6.Mode)
	}

	// With route-nopull nothing route-related is applied.
	nb := cfg(t, "client\nroute-nopull\n")
	p = ParsePushed(strings.Split(pushLog, "\n"), AnalyzeStatic(nb))
	for _, o := range p.Options {
		if o.Applied && !strings.HasPrefix(o.Text, "ifconfig") {
			t.Fatalf("route-nopull applied %q", o.Text)
		}
	}
	if Effective(nb, p).IPv4.Mode != ModeNone {
		t.Fatal("route-nopull effective mode should be NONE")
	}
	// A new session resets earlier replies.
	lines := append(strings.Split(pushLog, "\n"), "x PUSH: Received control message: 'PUSH_REPLY,route 172.16.0.0 255.240.0.0'")
	p = ParsePushed(lines, st)
	if len(p.Options) != 1 {
		t.Fatalf("latest session only: %+v", p.Options)
	}
	if !Effective(base, ParsePushed(nil, st)).PushedGatewayAccepted {
		t.Fatal("without push data Effective must equal static")
	}
}

func TestRuntime(t *testing.T) {
	routes := []network.Route{
		{Dst: netip.MustParsePrefix("0.0.0.0/0"), Dev: "wlan0", Gateway: netip.MustParseAddr("192.168.1.1")},
		{Dst: netip.MustParsePrefix("0.0.0.0/1"), Dev: "tun0"},
		{Dst: netip.MustParsePrefix("128.0.0.0/1"), Dev: "tun0"},
		{Dst: netip.MustParsePrefix("10.8.0.0/24"), Dev: "tun0", Protocol: "kernel", Scope: "link"},
	}
	p4 := network.RouteDecision{Dst: netip.MustParseAddr("1.1.1.1"), Dev: "tun0"}
	p6 := network.RouteDecision{Dst: netip.MustParseAddr("2606:4700:4700::1111"), Dev: "wlan0"}
	rt := AnalyzeRuntime("tun0", routes, p4, p6)
	if rt.IPv4.Mode != ModeFull || rt.IPv6.Mode != ModeNone || len(rt.VPNRoutes) != 3 || len(rt.Defaults) != 1 {
		t.Fatalf("runtime = %+v", rt)
	}
	split := AnalyzeRuntime("tun0", routes[3:], network.RouteDecision{Dst: p4.Dst, Dev: "wlan0"}, p6)
	if split.IPv4.Mode != ModeNone {
		t.Fatalf("connected subnet only should be NONE, got %s", split.IPv4.Mode)
	}
	withNet := append(routes[3:], network.Route{Dst: netip.MustParsePrefix("10.0.0.0/8"), Dev: "tun0"})
	split = AnalyzeRuntime("tun0", withNet, network.RouteDecision{Dst: p4.Dst, Dev: "wlan0"}, p6)
	if split.IPv4.Mode != ModeSplit {
		t.Fatalf("got %s, want split", split.IPv4.Mode)
	}
	none := AnalyzeRuntime("", routes, p4, p6)
	if none.IPv4.Mode != ModeUnknown {
		t.Fatal("no interface must be unknown")
	}
	noRoute := AnalyzeRuntime("tun0", nil, network.RouteDecision{Dst: p4.Dst, Err: errors.New("unreachable")}, p6)
	if noRoute.IPv4.Mode != ModeNone {
		t.Fatalf("unreachable probe: %s", noRoute.IPv4.Mode)
	}
}
