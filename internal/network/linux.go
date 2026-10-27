//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Linux implements Provider using rtnetlink and sysfs.
type Linux struct {
	// SysClassNet is normally /sys/class/net (overridable for tests).
	SysClassNet string
}

// NewLinux returns the production provider.
func NewLinux() *Linux { return &Linux{SysClassNet: "/sys/class/net"} }

// Links lists all interfaces with addresses and counters.
func (p *Linux) Links(ctx context.Context) ([]Link, error) {
	nlLinks, err := netlink.LinkList()
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	addrs, aerr := netlink.AddrList(nil, netlink.FAMILY_ALL)
	if aerr != nil && !errors.Is(aerr, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("list addresses: %w", aerr)
	}
	byIndex := map[int][]netip.Prefix{}
	for _, a := range addrs {
		if a.IPNet == nil {
			continue
		}
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		ones, _ := a.Mask.Size()
		byIndex[a.LinkIndex] = append(byIndex[a.LinkIndex], netip.PrefixFrom(ip.Unmap(), ones))
	}
	out := make([]Link, 0, len(nlLinks))
	for _, nl := range nlLinks {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attrs := nl.Attrs()
		l := Link{
			Name:      attrs.Name,
			Index:     attrs.Index,
			Kind:      nl.Type(),
			Up:        attrs.Flags&net.FlagUp != 0,
			OperState: attrs.OperState.String(),
			MTU:       attrs.MTU,
			Addrs:     byIndex[attrs.Index],
		}
		if t, ok := nl.(*netlink.Tuntap); ok {
			l.TunMode = "tun"
			if t.Mode == netlink.TUNTAP_MODE_TAP {
				l.TunMode = "tap"
			}
		}
		if s := attrs.Statistics; s != nil {
			l.Stats = Counters{
				RxBytes: s.RxBytes, TxBytes: s.TxBytes,
				RxPackets: s.RxPackets, TxPackets: s.TxPackets,
				RxErrors: s.RxErrors, TxErrors: s.TxErrors,
			}
		}
		// A tun device that has never been configured reports kind "device"
		// via some kernels; sysfs tun_flags identifies it reliably.
		if l.Kind == "device" || l.Kind == "" {
			if _, err := os.Stat(filepath.Join(p.SysClassNet, l.Name, "tun_flags")); err == nil {
				l.Kind = "tuntap"
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// Counters reads byte/packet counters from sysfs. This is cheap enough to be
// called every graph tick.
func (p *Linux) Counters(name string) (Counters, error) {
	if name == "" || strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
		return Counters{}, fmt.Errorf("invalid interface name %q", name)
	}
	dir := filepath.Join(p.SysClassNet, name, "statistics")
	read := func(f string) (uint64, error) {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	}
	var c Counters
	var err error
	if c.RxBytes, err = read("rx_bytes"); err != nil {
		return Counters{}, fmt.Errorf("interface %s: %w", name, err)
	}
	if c.TxBytes, err = read("tx_bytes"); err != nil {
		return Counters{}, fmt.Errorf("interface %s: %w", name, err)
	}
	c.RxPackets, _ = read("rx_packets")
	c.TxPackets, _ = read("tx_packets")
	c.RxErrors, _ = read("rx_errors")
	c.TxErrors, _ = read("tx_errors")
	return c, nil
}

// Routes lists IPv4 and IPv6 routes of the main table.
func (p *Linux) Routes(ctx context.Context) ([]Route, error) {
	nlRoutes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
	if err != nil && !errors.Is(err, netlink.ErrDumpInterrupted) {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	names := map[int]string{}
	if links, err := netlink.LinkList(); err == nil || errors.Is(err, netlink.ErrDumpInterrupted) {
		for _, l := range links {
			names[l.Attrs().Index] = l.Attrs().Name
		}
	}
	out := make([]Route, 0, len(nlRoutes))
	for _, r := range nlRoutes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if r.Type != unix.RTN_UNICAST && r.Type != 0 {
			continue
		}
		out = append(out, convertRoute(r, names))
	}
	return out, nil
}

func convertRoute(r netlink.Route, names map[int]string) Route {
	rt := Route{
		Dev:      names[r.LinkIndex],
		Metric:   r.Priority,
		Table:    r.Table,
		Protocol: r.Protocol.String(),
		Scope:    r.Scope.String(),
	}
	if r.Dst != nil {
		if ip, ok := netip.AddrFromSlice(r.Dst.IP); ok {
			ones, _ := r.Dst.Mask.Size()
			rt.Dst = netip.PrefixFrom(ip.Unmap(), ones)
		}
	} else if r.Family == unix.AF_INET6 {
		rt.Dst = netip.MustParsePrefix("::/0")
	} else {
		rt.Dst = netip.MustParsePrefix("0.0.0.0/0")
	}
	if ip, ok := netip.AddrFromSlice(r.Gw); ok {
		rt.Gateway = ip.Unmap()
	}
	if ip, ok := netip.AddrFromSlice(r.Src); ok {
		rt.Src = ip.Unmap()
	}
	if rt.Dev == "" && len(r.MultiPath) > 0 {
		rt.Dev = names[r.MultiPath[0].LinkIndex]
	}
	return rt
}

// RouteGet asks the kernel which route it would use for dst. No packet is
// sent; policy routing rules are honoured.
func (p *Linux) RouteGet(ctx context.Context, dst netip.Addr) RouteDecision {
	dec := RouteDecision{Dst: dst}
	if ctx.Err() != nil {
		dec.Err = ctx.Err()
		return dec
	}
	routes, err := netlink.RouteGet(net.IP(dst.AsSlice()))
	if err != nil {
		dec.Err = fmt.Errorf("route lookup for %s: %w", dst, err)
		return dec
	}
	if len(routes) == 0 {
		dec.Err = fmt.Errorf("no route to %s", dst)
		return dec
	}
	r := routes[0]
	if l, err := netlink.LinkByIndex(r.LinkIndex); err == nil {
		dec.Dev = l.Attrs().Name
	}
	if ip, ok := netip.AddrFromSlice(r.Gw); ok {
		dec.Gateway = ip.Unmap()
	}
	if ip, ok := netip.AddrFromSlice(r.Src); ok {
		dec.Src = ip.Unmap()
	}
	return dec
}

// NewDefault returns the provider for the running platform.
func NewDefault() Provider { return NewLinux() }
