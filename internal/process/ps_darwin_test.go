//go:build darwin

package process

import "testing"

func TestParsePS(t *testing.T) {
	out := "  1     0 Sun Oct  4 08:00:00 2026     /sbin/launchd\n" +
		"  812   0 Sun Oct  4 21:25:04 2026     /opt/homebrew/opt/openvpn/sbin/openvpn --log /Users/a/l.log --cd /Users/a/p --config office.ovpn --management /var/folders/x/T/openvpn-desktop-501/mgmt.sock unix\n" +
		"  900 501 Sun Oct  4 21:30:00 2026     vim openvpn\n"
	ps := ParsePS(out)
	if len(ps) != 1 {
		t.Fatalf("%+v", ps)
	}
	p := ps[0]
	if p.PID != 812 || p.UID != 0 || p.Config != "/Users/a/p/office.ovpn" || p.Socket != "/var/folders/x/T/openvpn-desktop-501/mgmt.sock" || p.Log != "/Users/a/l.log" || p.StartTicks == 0 {
		t.Fatalf("%+v", p)
	}
}
