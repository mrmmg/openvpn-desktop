//go:build darwin

package network

import (
	"bufio"
	"net/netip"
	"strconv"
	"strings"
)

// Parsers for the BSD/macOS networking tools (netstat, route). They live in
// an untagged file so they are tested on every platform.

// KindFromName derives the link kind from a macOS interface name.
func KindFromName(name string) string {
	switch {
	case strings.HasPrefix(name, "utun"):
		return "utun"
	case strings.HasPrefix(name, "tun"), strings.HasPrefix(name, "tap"):
		return "tun"
	}
	return "device"
}

// ParseNetstatInterfaces parses `netstat -ibn` and returns the link-level
// counters per interface. Columns are read from the right because the
// Address column is empty for point-to-point interfaces such as utun.
func ParseNetstatInterfaces(out string) map[string]Counters {
	res := map[string]Counters{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[0] == "Name" || !strings.HasPrefix(f[2], "<Link#") {
			continue
		}
		n := len(f)
		num := func(s string) uint64 {
			v, _ := strconv.ParseUint(s, 10, 64)
			return v
		}
		name := strings.TrimSuffix(f[0], "*")
		res[name] = Counters{
			RxPackets: num(f[n-7]), RxErrors: num(f[n-6]), RxBytes: num(f[n-5]),
			TxPackets: num(f[n-4]), TxErrors: num(f[n-3]), TxBytes: num(f[n-2]),
		}
	}
	return res
}

// ParseBSDDestination converts netstat's abbreviated destinations
// ("default", "10.8/24", "192.168.1", "0/1", "fe80::%lo0/64") to a prefix.
func ParseBSDDestination(dst string, ipv6 bool) (netip.Prefix, bool) {
	if dst == "default" {
		if ipv6 {
			return netip.MustParsePrefix("::/0"), true
		}
		return netip.MustParsePrefix("0.0.0.0/0"), true
	}
	addrPart, maskPart, hasMask := strings.Cut(dst, "/")
	if i := strings.IndexByte(addrPart, '%'); i >= 0 {
		addrPart = addrPart[:i]
	}
	if ipv6 || strings.Contains(addrPart, ":") {
		a, err := netip.ParseAddr(addrPart)
		if err != nil {
			return netip.Prefix{}, false
		}
		bits := 128
		if hasMask {
			if bits, err = strconv.Atoi(maskPart); err != nil {
				return netip.Prefix{}, false
			}
		}
		p, err := a.Prefix(bits)
		return p, err == nil
	}
	octets := strings.Split(addrPart, ".")
	if len(octets) == 0 || len(octets) > 4 {
		return netip.Prefix{}, false
	}
	bits := 8 * len(octets)
	for len(octets) < 4 {
		octets = append(octets, "0")
	}
	a, err := netip.ParseAddr(strings.Join(octets, "."))
	if err != nil {
		return netip.Prefix{}, false
	}
	if hasMask {
		if bits, err = strconv.Atoi(maskPart); err != nil {
			return netip.Prefix{}, false
		}
	}
	p, err := a.Prefix(bits)
	return p, err == nil
}

// ParseNetstatRoutes parses `netstat -rn -f inet|inet6`.
func ParseNetstatRoutes(out string, ipv6 bool) []Route {
	var routes []Route
	header := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if f[0] == "Destination" {
			header = true
			continue
		}
		if !header || len(f) < 4 {
			continue
		}
		dst, ok := ParseBSDDestination(f[0], ipv6)
		if !ok {
			continue
		}
		r := Route{Dst: dst, Dev: f[3], Table: 0}
		gw := f[1]
		if i := strings.IndexByte(gw, '%'); i >= 0 {
			gw = gw[:i]
		}
		if g, err := netip.ParseAddr(gw); err == nil && strings.Contains(f[2], "G") {
			r.Gateway = g
		}
		if strings.Contains(f[2], "S") {
			r.Protocol = "static"
		}
		routes = append(routes, r)
	}
	return routes
}

// ParseRouteGet parses `route -n get <addr>`.
func ParseRouteGet(out string) (dev string, gw netip.Addr) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "interface":
			dev = v
		case "gateway":
			if i := strings.IndexByte(v, '%'); i >= 0 {
				v = v[:i]
			}
			if a, err := netip.ParseAddr(v); err == nil {
				gw = a
			}
		}
	}
	return dev, gw
}
