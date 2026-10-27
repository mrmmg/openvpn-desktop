//go:build linux

package network

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestSysfsCounters(t *testing.T) {
	root := t.TempDir()
	stats := filepath.Join(root, "tun0", "statistics")
	_ = os.MkdirAll(stats, 0o755)
	for f, v := range map[string]string{"rx_bytes": "12345\n", "tx_bytes": "678\n", "rx_packets": "9\n"} {
		_ = os.WriteFile(filepath.Join(stats, f), []byte(v), 0o644)
	}
	p := &Linux{SysClassNet: root}
	c, err := p.Counters("tun0")
	if err != nil || c.RxBytes != 12345 || c.TxBytes != 678 || c.RxPackets != 9 {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{"", "../etc", "missing"} {
		if _, err := p.Counters(bad); err == nil {
			t.Errorf("Counters(%q) must fail", bad)
		}
	}
}

// TestLinuxProvider is an integration test against the real kernel. It only
// reads state and needs no privileges.
func TestLinuxProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	p := NewLinux()
	ctx := context.Background()
	links, err := p.Links(ctx)
	if err != nil {
		t.Skipf("netlink unavailable: %v", err)
	}
	var lo *Link
	for i := range links {
		if links[i].Name == "lo" {
			lo = &links[i]
		}
	}
	if lo == nil || !lo.HasAddr(netip.MustParseAddr("127.0.0.1")) {
		t.Fatalf("loopback not found: %+v", links)
	}
	if _, err := p.Counters("lo"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Routes(ctx); err != nil {
		t.Fatal(err)
	}
	if d := p.RouteGet(ctx, netip.MustParseAddr("127.0.0.1")); d.Err != nil || d.Dev != "lo" {
		t.Fatalf("RouteGet loopback = %+v", d)
	}
}
