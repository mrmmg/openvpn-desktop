package analyzer

import (
	"fmt"

	"github.com/mrmmg/openvpn-desktop/internal/network"
)

// Runtime is the observed routing state.
type Runtime struct {
	Iface      string
	IPv4, IPv6 Family
	Probe4     network.RouteDecision
	Probe6     network.RouteDecision
	VPNRoutes  []network.Route
	Defaults   []network.Route
}

// AnalyzeRuntime classifies observed routing for the VPN interface iface.
// probe4/probe6 are the kernel's route decisions for public addresses.
func AnalyzeRuntime(iface string, routes []network.Route, probe4, probe6 network.RouteDecision) Runtime {
	rt := Runtime{Iface: iface, Probe4: probe4, Probe6: probe6}
	for _, r := range routes {
		if r.IsDefault() {
			rt.Defaults = append(rt.Defaults, r)
		}
		if iface != "" && r.Dev == iface {
			rt.VPNRoutes = append(rt.VPNRoutes, r)
		}
	}
	rt.IPv4 = rt.family(false)
	rt.IPv6 = rt.family(true)
	return rt
}

func (rt Runtime) family(ipv6 bool) Family {
	probe := rt.Probe4
	label := "IPv4"
	if ipv6 {
		probe = rt.Probe6
		label = "IPv6"
	}
	if rt.Iface == "" {
		return Family{Mode: ModeUnknown, Reasons: []string{"no VPN interface"}}
	}
	if probe.Err != nil && !probe.Dst.IsValid() {
		return Family{Mode: ModeUnknown, Reasons: []string{label + " probe not configured"}}
	}
	var specific []string
	for _, r := range rt.VPNRoutes {
		if r.Dst.Addr().Is6() != ipv6 {
			continue
		}
		// Ignore the interface's own connected subnet and link-local space.
		if r.Scope == "link" || r.Dst.Addr().IsLinkLocalUnicast() || r.Protocol == "kernel" {
			continue
		}
		specific = append(specific, r.Dst.String())
	}
	switch {
	case probe.Err != nil:
		if len(specific) > 0 {
			return Family{Mode: ModeSplit, Reasons: []string{fmt.Sprintf("no %s route to %s; VPN routes: %v", label, probe.Dst, specific)}}
		}
		return Family{Mode: ModeNone, Reasons: []string{fmt.Sprintf("no %s route to %s", label, probe.Dst)}}
	case probe.Dev == rt.Iface:
		return Family{Mode: ModeFull, Reasons: []string{fmt.Sprintf("%s is routed via %s", probe.Dst, rt.Iface)}}
	case len(specific) > 0:
		return Family{Mode: ModeSplit, Reasons: []string{
			fmt.Sprintf("%s is routed via %s (not the VPN)", probe.Dst, probe.Dev),
			fmt.Sprintf("%d %s route(s) via %s", len(specific), label, rt.Iface),
		}}
	default:
		return Family{Mode: ModeNone, Reasons: []string{fmt.Sprintf("%s is routed via %s; no %s routes via %s", probe.Dst, probe.Dev, label, rt.Iface)}}
	}
}
