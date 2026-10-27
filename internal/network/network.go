// Package network inspects Linux network interfaces, addresses, counters and
// routes. All functions are read-only and need no privileges.
package network

import (
	"context"
	"net/netip"
	"slices"
	"strings"
)

// Link describes a network interface.
type Link struct {
	Name  string
	Index int
	// Kind is the link kind: "tuntap", "ovpn", "ovpn-dco", "device" (Linux,
	// from rtnetlink) or "utun", "tun", "device" (macOS, from the name).
	Kind string
	// TunMode is "tun" or "tap" for tuntap devices.
	TunMode string
	Up      bool
	// OperState is the kernel's operational state ("up", "unknown", "down").
	OperState string
	MTU       int
	Addrs     []netip.Prefix
	Stats     Counters
}

// Counters are cumulative interface counters.
type Counters struct {
	RxBytes, TxBytes     uint64
	RxPackets, TxPackets uint64
	RxErrors, TxErrors   uint64
}

// IsVPNCandidate reports whether the link could be an OpenVPN tunnel.
func (l Link) IsVPNCandidate() bool {
	switch l.Kind {
	case "tuntap", "tun", "utun", "ovpn", "ovpn-dco":
		return true
	}
	return false
}

// IPv4 returns the IPv4 addresses.
func (l Link) IPv4() []netip.Prefix {
	var out []netip.Prefix
	for _, a := range l.Addrs {
		if a.Addr().Is4() {
			out = append(out, a)
		}
	}
	return out
}

// IPv6 returns the IPv6 addresses (excluding link-local).
func (l Link) IPv6() []netip.Prefix {
	var out []netip.Prefix
	for _, a := range l.Addrs {
		if a.Addr().Is6() && !a.Addr().IsLinkLocalUnicast() {
			out = append(out, a)
		}
	}
	return out
}

// HasAddr reports whether the link carries addr.
func (l Link) HasAddr(addr netip.Addr) bool {
	for _, a := range l.Addrs {
		if a.Addr() == addr {
			return true
		}
	}
	return false
}

// Route is a routing table entry.
type Route struct {
	Dst      netip.Prefix
	Gateway  netip.Addr
	Dev      string
	Src      netip.Addr
	Metric   int
	Table    int
	Protocol string
	Scope    string
	Type     string
}

// IsDefault reports whether the route is a default route.
func (r Route) IsDefault() bool { return r.Dst.IsValid() && r.Dst.Bits() == 0 }

// String renders the route in `ip route` style.
func (r Route) String() string {
	var b strings.Builder
	if r.IsDefault() {
		b.WriteString("default")
	} else {
		b.WriteString(r.Dst.String())
	}
	if r.Gateway.IsValid() {
		b.WriteString(" via " + r.Gateway.String())
	}
	if r.Dev != "" {
		b.WriteString(" dev " + r.Dev)
	}
	if r.Protocol != "" && r.Protocol != "boot" {
		b.WriteString(" proto " + r.Protocol)
	}
	if r.Metric != 0 {
		b.WriteString(" metric ")
		b.WriteString(itoa(r.Metric))
	}
	return b.String()
}

// RouteDecision is the kernel's answer to "how would a packet to Dst leave?".
type RouteDecision struct {
	Dst     netip.Addr
	Dev     string
	Gateway netip.Addr
	Src     netip.Addr
	Err     error
}

// Provider abstracts the OS for testing.
type Provider interface {
	Links(ctx context.Context) ([]Link, error)
	Counters(name string) (Counters, error)
	Routes(ctx context.Context) ([]Route, error)
	RouteGet(ctx context.Context, dst netip.Addr) RouteDecision
}

// VPNLinks filters links that look like OpenVPN tunnels.
func VPNLinks(links []Link) []Link {
	var out []Link
	for _, l := range links {
		if l.IsVPNCandidate() {
			out = append(out, l)
		}
	}
	return out
}

// FindByName returns the named link.
func FindByName(links []Link, name string) (Link, bool) {
	i := slices.IndexFunc(links, func(l Link) bool { return l.Name == name })
	if i < 0 {
		return Link{}, false
	}
	return links[i], true
}

// FindByAddr returns the link carrying addr.
func FindByAddr(links []Link, addr netip.Addr) (Link, bool) {
	for _, l := range links {
		if l.HasAddr(addr) {
			return l, true
		}
	}
	return Link{}, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
