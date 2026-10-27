package network

import (
	"math"
	"net/netip"
	"testing"
	"time"
)

func TestMeterRates(t *testing.T) {
	m := NewMeter(3)
	t0 := time.Unix(1000, 0)
	if _, ok := m.Add(Counters{RxBytes: 1000, TxBytes: 500}, t0); ok {
		t.Fatal("first sample only primes")
	}
	r, ok := m.Add(Counters{RxBytes: 3000, TxBytes: 1500}, t0.Add(2*time.Second))
	if !ok || r.Down != 1000 || r.Up != 500 {
		t.Fatalf("rate = %+v", r)
	}
	// Counter reset (interface recreated): zero rate, no negative numbers.
	r, _ = m.Add(Counters{RxBytes: 10, TxBytes: 10}, t0.Add(3*time.Second))
	if r.Down != 0 || r.Up != 0 {
		t.Fatalf("reset rate = %+v", r)
	}
	m.Add(Counters{RxBytes: 1010, TxBytes: 10}, t0.Add(4*time.Second))
	m.Add(Counters{RxBytes: 1010, TxBytes: 10}, t0.Add(5*time.Second))
	s := m.Samples()
	if len(s) != 3 || s[0].Down != 0 || s[1].Down != 1000 || s[2].Down != 0 {
		t.Fatalf("ring order wrong: %+v", s)
	}
	if d, _ := m.Peak(); d != 1000 {
		t.Fatalf("peak %v", d)
	}
	if last, _ := m.Last(); !last.At.Equal(t0.Add(5 * time.Second)) {
		t.Fatalf("last %+v", last)
	}
	if m.TotalDown != 3000 {
		t.Fatalf("total down %d", m.TotalDown)
	}
	// Same timestamp is ignored.
	if _, ok := m.Add(Counters{}, t0.Add(5*time.Second)); ok {
		t.Fatal("dt=0 must be ignored")
	}
	m.Reset()
	if len(m.Samples()) != 0 || m.TotalDown != 0 {
		t.Fatal("reset")
	}
}

func TestFormat(t *testing.T) {
	cases := map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.00 KB", 1536 * 1024: "1.50 MB", 150 << 30: "150 GB"}
	for in, want := range cases {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%d) = %s, want %s", in, got, want)
		}
	}
	if got := FormatRate(125000, true); got != "1.00 Mbit/s" {
		t.Errorf("bits: %s", got)
	}
	if got := FormatRate(-5, false); got != "0 B/s" {
		t.Errorf("negative: %s", got)
	}
	if got := FormatRate(math.Inf(1), false); got == "" {
		t.Error("inf")
	}
}

func TestHelpers(t *testing.T) {
	links := []Link{
		{Name: "eth0", Kind: "device", Addrs: []netip.Prefix{netip.MustParsePrefix("192.168.1.2/24"), netip.MustParsePrefix("fe80::1/64")}},
		{Name: "tun0", Kind: "tuntap", Addrs: []netip.Prefix{netip.MustParsePrefix("10.8.0.6/24"), netip.MustParsePrefix("fd00::6/64")}},
		{Name: "ovpn0", Kind: "ovpn"},
	}
	if len(VPNLinks(links)) != 2 {
		t.Fatal("VPNLinks should include tuntap and DCO (ovpn)")
	}
	if l, ok := FindByAddr(links, netip.MustParseAddr("10.8.0.6")); !ok || l.Name != "tun0" {
		t.Fatal("FindByAddr")
	}
	if len(links[0].IPv6()) != 0 || len(links[1].IPv6()) != 1 || len(links[1].IPv4()) != 1 {
		t.Fatal("IPv6 must exclude link-local")
	}
	r := Route{Dst: netip.MustParsePrefix("0.0.0.0/0"), Gateway: netip.MustParseAddr("10.0.0.1"), Dev: "eth0", Metric: 100, Protocol: "dhcp"}
	if r.String() != "default via 10.0.0.1 dev eth0 proto dhcp metric 100" || !r.IsDefault() {
		t.Fatal(r.String())
	}
}
