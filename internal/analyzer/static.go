// Package analyzer determines how an OpenVPN profile routes traffic.
//
// Three sources of truth are kept strictly apart:
//
//   - Static: what the .ovpn file plus launch overrides request.
//   - Pushed: what the server sent in PUSH_REPLY during this session.
//   - Runtime: what the kernel routing table actually does now.
//
// Only Runtime results are observations; Static results are requests and are
// labelled as such.
package analyzer

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/mrmmg/openvpn-wails/internal/ovpn"
)

// Mode is a routing classification.
type Mode string

// Routing modes.
const (
	ModeFull    Mode = "FULL TUNNEL"
	ModeSplit   Mode = "SPLIT TUNNEL"
	ModeNone    Mode = "NO ROUTES"      // only the tunnel subnet is reachable
	ModeServer  Mode = "SERVER DECIDES" // depends on pushed options
	ModeBlocked Mode = "BLOCKED"        // block-ipv6: traffic is dropped
	ModeUnknown Mode = "UNKNOWN"
)

// Family is a per-address-family verdict.
type Family struct {
	Mode    Mode
	Reasons []string
}

// PullFilter is one pull-filter directive.
type PullFilter struct {
	Action string // accept, ignore, reject
	Text   string
	Line   int
}

// RouteSpec is a route requested by configuration.
type RouteSpec struct {
	Prefix  netip.Prefix
	Gateway string // as written: vpn_gateway, net_gateway, address or ""
	Raw     string
	IPv6    bool
	Line    int
}

// BypassesVPN reports whether the route explicitly uses the pre-VPN gateway.
func (r RouteSpec) BypassesVPN() bool { return r.Gateway == "net_gateway" }

// Static is the result of analysing a configuration file.
type Static struct {
	IPv4, IPv6 Family

	Pulls           bool
	RouteNoPull     bool
	RedirectGateway *ovpn.Directive
	RedirectPrivate *ovpn.Directive
	BlockIPv6       bool
	RouteNoExec     bool
	Routes          []RouteSpec
	PullFilters     []PullFilter
	// PushedGatewayAccepted is false when a pushed redirect-gateway would be
	// dropped by route-nopull or pull-filter.
	PushedGatewayAccepted bool
	DNS                   []string
	Notes                 []string
}

// Directives lists the routing-relevant directives, for display.
func (s Static) Directives() []string {
	var out []string
	if s.RedirectGateway != nil {
		out = append(out, s.RedirectGateway.String())
	}
	if s.RedirectPrivate != nil {
		out = append(out, s.RedirectPrivate.String())
	}
	if s.RouteNoPull {
		out = append(out, "route-nopull")
	}
	if s.BlockIPv6 {
		out = append(out, "block-ipv6")
	}
	if s.RouteNoExec {
		out = append(out, "route-noexec")
	}
	for _, r := range s.Routes {
		out = append(out, r.Raw)
	}
	for _, f := range s.PullFilters {
		out = append(out, fmt.Sprintf("pull-filter %s %q", f.Action, f.Text))
	}
	return out
}

// FilterDecision returns how a pushed option string would be handled by the
// pull-filters (first prefix match wins; default accept).
func FilterDecision(filters []PullFilter, option string) string {
	for _, f := range filters {
		if strings.HasPrefix(option, f.Text) {
			return f.Action
		}
	}
	return "accept"
}

// routeOptionNames are pushed options dropped by --route-nopull.
var routeOptionNames = []string{"route", "route-ipv6", "redirect-gateway", "redirect-private", "route-gateway", "route-metric", "block-outside-dns", "dhcp-option", "dns"}

// AcceptsPushed reports whether a pushed option would be applied.
func (s Static) AcceptsPushed(option string) bool {
	if s.RouteNoPull {
		name, _, _ := strings.Cut(option, " ")
		if slices.Contains(routeOptionNames, name) {
			return false
		}
	}
	return FilterDecision(s.PullFilters, option) == "accept"
}

// AnalyzeStatic inspects a parsed configuration (with launch overrides
// already appended).
func AnalyzeStatic(c *ovpn.Config) Static {
	s := Static{Pulls: c.Pulls(), RouteNoPull: c.Has("route-nopull"), BlockIPv6: c.Has("block-ipv6"), RouteNoExec: c.Has("route-noexec")}
	if d, ok := c.Last("redirect-gateway"); ok {
		s.RedirectGateway = &d
	}
	if d, ok := c.Last("redirect-private"); ok {
		s.RedirectPrivate = &d
	}
	for _, d := range c.All("pull-filter") {
		s.PullFilters = append(s.PullFilters, PullFilter{Action: d.Arg(0), Text: d.Arg(1), Line: d.Line})
	}
	for _, d := range c.Directives {
		switch d.Name {
		case "route":
			if rs, ok := parseRoute4(d); ok {
				s.Routes = append(s.Routes, rs)
			}
		case "route-ipv6":
			if p, err := netip.ParsePrefix(d.Arg(0)); err == nil {
				s.Routes = append(s.Routes, RouteSpec{Prefix: p.Masked(), Gateway: d.Arg(1), Raw: d.String(), IPv6: true, Line: d.Line})
			}
		case "dhcp-option":
			if strings.EqualFold(d.Arg(0), "DNS") || strings.EqualFold(d.Arg(0), "DNS6") {
				s.DNS = append(s.DNS, d.Arg(1))
			}
		case "dns":
			if d.Arg(0) == "server" && d.Arg(2) == "address" {
				s.DNS = append(s.DNS, d.Args[3:]...)
			}
		}
	}
	s.PushedGatewayAccepted = s.Pulls && s.AcceptsPushed("redirect-gateway def1")

	s.IPv4 = s.family(false)
	s.IPv6 = s.family(true)

	if s.RouteNoExec {
		s.Notes = append(s.Notes, "route-noexec: OpenVPN will not install routes itself; a script must do it.")
	}
	if s.RouteNoPull {
		s.Notes = append(s.Notes, "route-nopull also drops DNS servers pushed by the server.")
	}
	if s.RouteNoPull && s.RedirectGateway != nil {
		s.Notes = append(s.Notes, "route-nopull does not affect the redirect-gateway written in this file.")
	}
	for _, f := range s.PullFilters {
		if f.Action == "accept" && strings.HasPrefix("redirect-gateway", f.Text) && f.Text != "" {
			s.Notes = append(s.Notes, fmt.Sprintf("pull-filter accept %q (line %d) takes precedence over later filters.", f.Text, f.Line))
		}
	}
	return s
}

func parseRoute4(d ovpn.Directive) (RouteSpec, bool) {
	netStr, maskStr := d.Arg(0), d.Arg(1)
	if maskStr == "" || maskStr == "default" {
		maskStr = "255.255.255.255"
	}
	gw := d.Arg(2)
	if netStr == "vpn_gateway" || netStr == "net_gateway" || netStr == "remote_host" {
		// e.g. "route remote_host 255.255.255.255 net_gateway" (server bypass)
		return RouteSpec{Gateway: gw, Raw: d.String(), Line: d.Line}, false
	}
	addr, err := netip.ParseAddr(netStr)
	if err != nil || !addr.Is4() {
		return RouteSpec{}, false
	}
	mask, err := netip.ParseAddr(maskStr)
	if err != nil || !mask.Is4() {
		return RouteSpec{}, false
	}
	bits := maskBits(mask)
	if bits < 0 {
		return RouteSpec{}, false
	}
	return RouteSpec{Prefix: netip.PrefixFrom(addr, bits).Masked(), Gateway: gw, Raw: d.String(), Line: d.Line}, true
}

func maskBits(mask netip.Addr) int {
	b := mask.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	n := 0
	for v&0x80000000 != 0 {
		n++
		v <<= 1
	}
	if v != 0 {
		return -1 // non-contiguous mask
	}
	return n
}

// coversDefault reports whether routes (through the VPN) cover the whole
// address space of the family.
func coversDefault(routes []RouteSpec, ipv6 bool) (bool, string) {
	var vpn []netip.Prefix
	for _, r := range routes {
		if r.IPv6 != ipv6 || r.BypassesVPN() || !r.Prefix.IsValid() {
			continue
		}
		if r.Prefix.Bits() == 0 {
			return true, r.Raw
		}
		vpn = append(vpn, r.Prefix)
	}
	if ipv6 {
		// 2000::/3 covers all global unicast space.
		for _, p := range vpn {
			if p.Bits() <= 3 && p.Contains(netip.MustParseAddr("2000::")) {
				return true, "route-ipv6 " + p.String()
			}
		}
		return false, ""
	}
	half := func(s string) bool { return slices.Contains(vpn, netip.MustParsePrefix(s)) }
	if half("0.0.0.0/1") && half("128.0.0.0/1") {
		return true, "routes 0.0.0.0/1 + 128.0.0.0/1"
	}
	return false, ""
}

func (s Static) family(ipv6 bool) Family {
	var f Family
	label := "IPv4"
	if ipv6 {
		label = "IPv6"
	}
	if ipv6 && s.BlockIPv6 {
		return Family{Mode: ModeBlocked, Reasons: []string{"block-ipv6: IPv6 traffic is dropped instead of leaking"}}
	}
	if rg := s.RedirectGateway; rg != nil {
		flags := rg.Args
		applies := !slices.Contains(flags, "!ipv4")
		if ipv6 {
			applies = slices.Contains(flags, "ipv6")
		}
		if applies {
			f.Mode = ModeFull
			f.Reasons = append(f.Reasons, fmt.Sprintf("%s (line %d)", rg.String(), rg.Line))
			return f
		}
	}
	if ok, why := coversDefault(s.Routes, ipv6); ok {
		return Family{Mode: ModeFull, Reasons: []string{why}}
	}
	var specific []string
	for _, r := range s.Routes {
		if r.IPv6 == ipv6 && !r.BypassesVPN() && r.Prefix.IsValid() {
			specific = append(specific, r.Prefix.String())
		}
	}
	switch {
	case s.Pulls && !s.RouteNoPull:
		f.Mode = ModeServer
		if s.PushedGatewayAccepted {
			f.Reasons = append(f.Reasons, "no local redirect-gateway; the server may push one")
		} else {
			f.Reasons = append(f.Reasons, "a pushed redirect-gateway would be ignored by pull-filter")
			if len(specific) == 0 {
				f.Reasons = append(f.Reasons, "pushed routes for specific networks are still accepted")
			}
		}
		if len(specific) > 0 {
			f.Reasons = append(f.Reasons, label+" routes in config: "+strings.Join(specific, ", "))
		}
	case len(specific) > 0:
		f.Mode = ModeSplit
		f.Reasons = append(f.Reasons, label+" routes in config: "+strings.Join(specific, ", "))
		if s.RouteNoPull {
			f.Reasons = append(f.Reasons, "route-nopull: server routes are ignored")
		}
	default:
		f.Mode = ModeNone
		if s.RouteNoPull {
			f.Reasons = append(f.Reasons, "route-nopull and no routes in config")
		} else {
			f.Reasons = append(f.Reasons, "configuration does not pull options and defines no routes")
		}
	}
	return f
}
