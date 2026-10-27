//go:build darwin

package network

import (
	"net/netip"
	"testing"
)

const netstatIbn = `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
lo0        16384 <Link#1>                          1200     0     345678     1200     0     345678     0
lo0        16384 127           127.0.0.1           1200     -     345678     1200     -     345678     -
en0        1500  <Link#6>    a4:83:e7:00:00:01   900000     0 1234567890   400000     0   98765432     0
en0        1500  192.168.1     192.168.1.20      900000     - 1234567890   400000     -   98765432     -
en5*       1500  <Link#7>    a4:83:e7:00:00:02        0     0          0        0     0          0     0
utun3      1500  <Link#20>                          150     1      84000      120     2      21000     0
utun3      1500  10.8/24       10.8.0.6             150     -      84000      120     -      21000     -
`

func TestParseNetstatInterfaces(t *testing.T) {
	c := ParseNetstatInterfaces(netstatIbn)
	if got := c["utun3"]; got.RxBytes != 84000 || got.TxBytes != 21000 || got.RxPackets != 150 || got.RxErrors != 1 || got.TxErrors != 2 {
		t.Fatalf("utun3 = %+v", got)
	}
	if got := c["en0"]; got.RxBytes != 1234567890 || got.TxBytes != 98765432 {
		t.Fatalf("en0 = %+v", got)
	}
	if _, ok := c["en5"]; !ok {
		t.Fatal("down interface marker '*' not stripped")
	}
	if len(c) != 4 {
		t.Fatalf("only link-level rows expected, got %d", len(c))
	}
}

func TestParseBSDDestination(t *testing.T) {
	cases := map[string]string{
		"default":        "0.0.0.0/0",
		"0/1":            "0.0.0.0/1",
		"128.0/1":        "128.0.0.0/1",
		"10.8/24":        "10.8.0.0/24",
		"192.168.1":      "192.168.1.0/24",
		"127":            "127.0.0.0/8",
		"192.168.1.1/32": "192.168.1.1/32",
		"203.0.113.7":    "203.0.113.7/32",
	}
	for in, want := range cases {
		p, ok := ParseBSDDestination(in, false)
		if !ok || p.String() != want {
			t.Errorf("%q → %v %v, want %s", in, p, ok, want)
		}
	}
	for in, want := range map[string]string{"default": "::/0", "::1": "::1/128", "fe80::%lo0/64": "fe80::/64", "2000::/3": "2000::/3"} {
		p, ok := ParseBSDDestination(in, true)
		if !ok || p.String() != want {
			t.Errorf("v6 %q → %v %v, want %s", in, p, ok, want)
		}
	}
	if _, ok := ParseBSDDestination("garbage", false); ok {
		t.Error("garbage accepted")
	}
}

func TestParseNetstatRoutes(t *testing.T) {
	out := `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.1.1        UGScg                 en0
0/1                10.8.0.1           UGScg               utun3
128.0/1            10.8.0.1           UGSc                utun3
10.8/24            10.8.0.1           UGSc                utun3
192.168.1          link#6             UCS                   en0      !
`
	rs := ParseNetstatRoutes(out, false)
	if len(rs) != 5 {
		t.Fatalf("routes = %+v", rs)
	}
	if !rs[0].IsDefault() || rs[0].Gateway != netip.MustParseAddr("192.168.1.1") || rs[0].Dev != "en0" {
		t.Fatalf("default = %+v", rs[0])
	}
	if rs[1].Dst.String() != "0.0.0.0/1" || rs[1].Dev != "utun3" {
		t.Fatalf("0/1 = %+v", rs[1])
	}
	if rs[4].Gateway.IsValid() {
		t.Fatal("link# gateway must be empty")
	}
	v6 := ParseNetstatRoutes("Internet6:\nDestination   Gateway    Flags  Netif Expire\ndefault       fe80::%utun0  UGcIg  utun0\n::1  ::1  UHL  lo0\n", true)
	if len(v6) != 2 || !v6[0].IsDefault() || v6[0].Dev != "utun0" {
		t.Fatalf("v6 = %+v", v6)
	}
}

func TestParseRouteGet(t *testing.T) {
	dev, gw := ParseRouteGet("   route to: 1.1.1.1\ndestination: default\n       mask: default\n    gateway: 10.8.0.1\n  interface: utun3\n      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING>\n")
	if dev != "utun3" || gw != netip.MustParseAddr("10.8.0.1") {
		t.Fatalf("%s %s", dev, gw)
	}
	if KindFromName("utun3") != "utun" || KindFromName("tap0") != "tun" || KindFromName("en0") != "device" {
		t.Fatal("KindFromName")
	}
}
