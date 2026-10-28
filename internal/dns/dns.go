// Package dns inspects the system resolver configuration and applies
// heuristics to spot DNS queries that would bypass the VPN.
//
// These are heuristics, not guarantees: applications may use their own
// resolvers (DoH in browsers), and resolvers may forward queries elsewhere.
package dns

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/network"
)

// Server is a configured DNS server.
type Server struct {
	Addr netip.Addr
	// Link is the interface the server is configured on (systemd-resolved),
	// or "" for global servers.
	Link string
}

// Config is the observed resolver configuration.
type Config struct {
	ResolvConf  []netip.Addr
	Search      []string
	StubLocal   bool // resolv.conf points to a local stub (127.0.0.53, ...)
	Resolved    bool // systemd-resolved data available
	Servers     []Server
	DefaultLink map[string]bool // links with "~." or DefaultRoute
	Source      string
}

// Reader loads the resolver configuration.
type Reader struct {
	ResolvConfPath string
	// Resolvectl runs `resolvectl <args>`; nil disables systemd-resolved.
	Resolvectl func(ctx context.Context, args ...string) (string, error)
}

// NewReader returns a reader for the real system.
func NewReader() *Reader {
	r := &Reader{ResolvConfPath: "/etc/resolv.conf"}
	if p, err := exec.LookPath("resolvectl"); err == nil {
		r.Resolvectl = func(ctx context.Context, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, p, args...).Output()
			return string(out), err
		}
	}
	return r
}

// Read gathers the configuration.
func (r *Reader) Read(ctx context.Context) (Config, error) {
	var c Config
	data, err := os.ReadFile(r.ResolvConfPath)
	if err != nil {
		return c, fmt.Errorf("read %s: %w", r.ResolvConfPath, err)
	}
	c.ResolvConf, c.Search = ParseResolvConf(string(data))
	for _, a := range c.ResolvConf {
		if a.IsLoopback() {
			c.StubLocal = true
		}
	}
	c.Source = r.ResolvConfPath
	if r.Resolvectl != nil {
		if out, err := r.Resolvectl(ctx, "dns"); err == nil {
			c.Resolved = true
			c.Servers = ParseResolvectlDNS(out)
			c.Source = "systemd-resolved"
			c.DefaultLink = map[string]bool{}
			if dom, err := r.Resolvectl(ctx, "domain"); err == nil {
				for link, doms := range ParseResolvectlLinks(dom) {
					for _, d := range doms {
						if d == "~." {
							c.DefaultLink[link] = true
						}
					}
				}
			}
		}
	}
	if !c.Resolved {
		for _, a := range c.ResolvConf {
			c.Servers = append(c.Servers, Server{Addr: a})
		}
	}
	return c, nil
}

// ParseResolvConf returns nameservers and search domains.
func ParseResolvConf(s string) ([]netip.Addr, []string) {
	var ns []netip.Addr
	var search []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], ";") {
			continue
		}
		switch f[0] {
		case "nameserver":
			host := f[1]
			if i := strings.IndexByte(host, '%'); i >= 0 {
				host = host[:i]
			}
			if a, err := netip.ParseAddr(host); err == nil {
				ns = append(ns, a.Unmap())
			}
		case "search", "domain":
			search = append(search, f[1:]...)
		}
	}
	return ns, search
}

var linkLineRe = regexp.MustCompile(`^(Global|Link \d+ \(([^)]+)\)):\s*(.*)$`)

// ParseResolvectlLinks parses `resolvectl dns|domain` style output into
// link -> values ("" for Global).
func ParseResolvectlLinks(out string) map[string][]string {
	res := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		m := linkLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		link := m[2]
		res[link] = append(res[link], strings.Fields(m[3])...)
	}
	return res
}

// ParseResolvectlDNS parses `resolvectl dns`.
func ParseResolvectlDNS(out string) []Server {
	var servers []Server
	for link, vals := range ParseResolvectlLinks(out) {
		for _, v := range vals {
			// Strip "%iface", "#servername" and ":port" decorations.
			if i := strings.IndexAny(v, "#%"); i >= 0 {
				v = v[:i]
			}
			if ap, err := netip.ParseAddrPort(v); err == nil {
				v = ap.Addr().String()
			}
			if a, err := netip.ParseAddr(v); err == nil {
				servers = append(servers, Server{Addr: a.Unmap(), Link: link})
			}
		}
	}
	return servers
}

// Verdict is the leak heuristic outcome.
type Verdict string

// Verdicts.
const (
	VerdictOK      Verdict = "LOOKS OK"
	VerdictWarning Verdict = "WARNING"
	VerdictUnknown Verdict = "UNKNOWN"
	VerdictNA      Verdict = "N/A"
)

// Finding explains the verdict per server.
type Finding struct {
	Server Server
	Dev    string
	ViaVPN bool
	Note   string
}

// Leak is the leak-check result.
type Leak struct {
	Verdict  Verdict
	Reasons  []string
	Findings []Finding
}

// RouteGetter is the subset of network.Provider used here.
type RouteGetter interface {
	RouteGet(ctx context.Context, dst netip.Addr) network.RouteDecision
}

// CheckLeak applies the heuristic for a connected VPN on iface. fullTunnel
// tells whether Internet traffic is routed through the VPN (runtime).
func CheckLeak(ctx context.Context, c Config, iface string, fullTunnel bool, rg RouteGetter) Leak {
	if iface == "" {
		return Leak{Verdict: VerdictNA, Reasons: []string{"no VPN interface"}}
	}
	if len(c.Servers) == 0 {
		return Leak{Verdict: VerdictUnknown, Reasons: []string{"no DNS servers found"}}
	}
	l := Leak{Verdict: VerdictOK}
	vpnHasDNS := false
	for _, s := range c.Servers {
		f := Finding{Server: s}
		switch {
		case s.Addr.IsLoopback():
			f.Note = "local stub resolver"
			f.ViaVPN = true
		case s.Link != "" && s.Link == iface:
			f.Dev, f.ViaVPN = iface, true
			vpnHasDNS = true
		default:
			d := rg.RouteGet(ctx, s.Addr)
			f.Dev = d.Dev
			if d.Err != nil {
				f.Note = "no route"
			}
			f.ViaVPN = d.Err == nil && d.Dev == iface
		}
		l.Findings = append(l.Findings, f)
	}
	for _, f := range l.Findings {
		if f.ViaVPN || f.Note == "no route" {
			continue
		}
		// With systemd-resolved, servers on other links are only used for
		// their own domains when the VPN link claims "~.".
		if c.Resolved && f.Server.Link != "" && f.Server.Link != iface && c.DefaultLink[iface] && !c.DefaultLink[f.Server.Link] {
			continue
		}
		l.Verdict = VerdictWarning
		where := f.Server.Link
		if where == "" {
			where = "global"
		}
		l.Reasons = append(l.Reasons, fmt.Sprintf("DNS server %s (%s) is reached via %s, outside the VPN", f.Server.Addr, where, f.Dev))
	}
	if l.Verdict == VerdictOK {
		switch {
		case c.StubLocal && !c.Resolved:
			l.Verdict = VerdictUnknown
			l.Reasons = append(l.Reasons, "a local resolver is used whose upstream servers are unknown")
		case vpnHasDNS:
			l.Reasons = append(l.Reasons, "DNS servers are configured on the VPN interface")
		default:
			l.Reasons = append(l.Reasons, "all DNS servers are routed through the VPN")
		}
		if !fullTunnel {
			l.Reasons = append(l.Reasons, "split tunnel: DNS may still be used for non-VPN traffic by design")
		}
	}
	l.Reasons = append(l.Reasons, "heuristic only: applications with their own resolver (e.g. DNS-over-HTTPS) are not covered")
	return l
}
