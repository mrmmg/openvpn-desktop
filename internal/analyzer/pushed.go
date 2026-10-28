package analyzer

import (
	"regexp"
	"strings"

	"github.com/mrmmg/openvpn-desktop/internal/ovpn"
)

// PushedOption is one option received in PUSH_REPLY.
type PushedOption struct {
	Text string
	// Applied is false when route-nopull or a pull-filter drops it.
	Applied bool
}

// Pushed is the server-pushed configuration of the most recent session.
type Pushed struct {
	Received bool
	Options  []PushedOption
	// Filtered lists options OpenVPN logged as removed by a pull-filter.
	Filtered []string
}

var (
	pushReplyRe = regexp.MustCompile(`PUSH: Received control message: '(PUSH_REPLY,.*)'`)
	filteredRe  = regexp.MustCompile(`Pushed option removed by filter: '([^']*)'`)
)

// ParsePushed extracts the latest PUSH_REPLY from OpenVPN log lines and
// marks which options the static configuration accepts.
func ParsePushed(lines []string, st Static) Pushed {
	var p Pushed
	continuation := false
	for _, line := range lines {
		if m := pushReplyRe.FindStringSubmatch(line); m != nil {
			if !continuation {
				p = Pushed{}
			}
			p.Received = true
			continuation = false
			for _, opt := range splitPush(strings.TrimPrefix(m[1], "PUSH_REPLY,")) {
				if opt == "push-continuation 2" {
					continuation = true
					continue
				}
				if opt == "push-continuation 1" {
					continue
				}
				p.Options = append(p.Options, PushedOption{Text: opt, Applied: st.AcceptsPushed(opt)})
			}
			continue
		}
		if m := filteredRe.FindStringSubmatch(line); m != nil {
			p.Filtered = append(p.Filtered, m[1])
		}
	}
	return p
}

// splitPush splits a PUSH_REPLY payload on commas (OpenVPN does not escape
// commas inside pushed options).
func splitPush(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Effective combines the static configuration with the applied pushed
// options and re-runs the static analysis, describing what OpenVPN was told
// to install for this session.
func Effective(base *ovpn.Config, p Pushed) Static {
	if !p.Received {
		return AnalyzeStatic(base)
	}
	var extra []ovpn.Directive
	for _, o := range p.Options {
		if !o.Applied {
			continue
		}
		toks := strings.Fields(o.Text)
		if len(toks) == 0 {
			continue
		}
		switch toks[0] {
		case "redirect-gateway", "redirect-private", "route", "route-ipv6", "block-ipv6", "dhcp-option", "dns":
			extra = append(extra, ovpn.Directive{Name: toks[0], Args: toks[1:]})
		}
	}
	eff := AnalyzeStatic(base.Append(extra...))
	// After the push the server has decided: "server decides" no longer applies.
	for _, f := range []*Family{&eff.IPv4, &eff.IPv6} {
		if f.Mode == ModeServer {
			specific := false
			for _, r := range eff.Routes {
				if r.IPv6 == (f == &eff.IPv6) && !r.BypassesVPN() && r.Prefix.IsValid() {
					specific = true
				}
			}
			if specific {
				f.Mode = ModeSplit
			} else {
				f.Mode = ModeNone
			}
			f.Reasons = append([]string{"server did not push a default route"}, f.Reasons[1:]...)
		}
	}
	return eff
}
