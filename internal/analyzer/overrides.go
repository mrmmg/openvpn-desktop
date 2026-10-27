package analyzer

import (
	"github.com/mrmmg/openvpn-wails/internal/ovpn"
	"github.com/mrmmg/openvpn-wails/internal/state"
)

// gatewayFilters are the pushed options ignored by IgnorePushedGateway.
// Prefix semantics: "route 0.0.0.0 " matches both 0.0.0.0/0 and 0.0.0.0/1.
var gatewayFilters = []string{
	"redirect-gateway",
	"route 0.0.0.0 ",
	"route 128.0.0.0 128.0.0.0",
	"route-ipv6 ::/0",
	"route-ipv6 ::/1",
	"route-ipv6 8000::/1",
	"route-ipv6 2000::/",
}

// OverrideDirectives converts launch overrides into directives, in the same
// order they are appended to the OpenVPN command line (after --config).
func OverrideDirectives(o state.Overrides) []ovpn.Directive {
	var out []ovpn.Directive
	if o.RouteNoPull {
		out = append(out, ovpn.Directive{Name: "route-nopull"})
	}
	if o.IgnorePushedGateway {
		for _, f := range gatewayFilters {
			out = append(out, ovpn.Directive{Name: "pull-filter", Args: []string{"ignore", f}})
		}
	}
	if o.ForceFullTunnel {
		out = append(out, ovpn.Directive{Name: "redirect-gateway", Args: []string{"def1", "ipv6"}})
	}
	if o.BlockIPv6 {
		out = append(out, ovpn.Directive{Name: "block-ipv6"})
	}
	return out
}

// OverrideArgs renders overrides as OpenVPN command-line arguments.
func OverrideArgs(o state.Overrides) []string {
	var args []string
	for _, d := range OverrideDirectives(o) {
		args = append(args, "--"+d.Name)
		args = append(args, d.Args...)
	}
	return args
}
