//go:build darwin

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"time"
)

// Darwin implements Provider on macOS using the standard library and the
// system tools netstat(1) and route(8). No privileges are required.
type Darwin struct{}

// NewDefault returns the provider for the running platform.
func NewDefault() Provider { return Darwin{} }

func runTool(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Links lists interfaces with addresses and counters.
func (Darwin) Links(ctx context.Context) ([]Link, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	stats := map[string]Counters{}
	if out, err := runTool(ctx, "/usr/sbin/netstat", "-ibn"); err == nil {
		stats = ParseNetstatInterfaces(out)
	}
	links := make([]Link, 0, len(ifs))
	for _, ifc := range ifs {
		l := Link{
			Name:  ifc.Name,
			Index: ifc.Index,
			Kind:  KindFromName(ifc.Name),
			Up:    ifc.Flags&net.FlagUp != 0,
			MTU:   ifc.MTU,
			Stats: stats[ifc.Name],
		}
		l.OperState = "down"
		if l.Up {
			l.OperState = "up"
		}
		if l.Kind == "tun" {
			l.TunMode = "tun"
			if strings.HasPrefix(ifc.Name, "tap") {
				l.TunMode = "tap"
			}
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(ipn.IP); ok {
					ones, _ := ipn.Mask.Size()
					l.Addrs = append(l.Addrs, netip.PrefixFrom(ip.Unmap(), ones))
				}
			}
		}
		links = append(links, l)
	}
	return links, nil
}

// Counters reads the counters of one interface.
func (Darwin) Counters(name string) (Counters, error) {
	if name == "" || strings.ContainsAny(name, " /\x00") {
		return Counters{}, fmt.Errorf("invalid interface name %q", name)
	}
	out, err := runTool(context.Background(), "/usr/sbin/netstat", "-ibn", "-I", name)
	if err != nil {
		return Counters{}, err
	}
	c, ok := ParseNetstatInterfaces(out)[name]
	if !ok {
		return Counters{}, fmt.Errorf("interface %s not found", name)
	}
	return c, nil
}

// Routes lists IPv4 and IPv6 routes.
func (Darwin) Routes(ctx context.Context) ([]Route, error) {
	var routes []Route
	var errs []error
	for _, fam := range []struct {
		flag string
		v6   bool
	}{{"inet", false}, {"inet6", true}} {
		out, err := runTool(ctx, "/usr/sbin/netstat", "-rn", "-f", fam.flag)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		routes = append(routes, ParseNetstatRoutes(out, fam.v6)...)
	}
	if len(routes) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return routes, nil
}

// RouteGet asks the kernel which route it would use (no packet is sent).
func (Darwin) RouteGet(ctx context.Context, dst netip.Addr) RouteDecision {
	dec := RouteDecision{Dst: dst}
	args := []string{"-n", "get"}
	if dst.Is6() {
		args = append(args, "-inet6")
	}
	out, err := runTool(ctx, "/sbin/route", append(args, dst.String())...)
	if err != nil {
		dec.Err = fmt.Errorf("no route to %s", dst)
		return dec
	}
	dec.Dev, dec.Gateway = ParseRouteGet(out)
	if dec.Dev == "" {
		dec.Err = fmt.Errorf("no route to %s", dst)
	}
	return dec
}
