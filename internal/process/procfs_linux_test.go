//go:build linux

package process

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fakeProc(t *testing.T, root string, pid int, comm string, args []string, cgroup string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o644)
	stat := strconv.Itoa(pid) + " (" + comm + ") S 1 1 1 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 100"
	_ = os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\t"+comm+"\nUid:\t0\t0\t0\t0\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "cgroup"), []byte(cgroup), 0o644)
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	srvConf := filepath.Join(root, "server.conf")
	_ = os.WriteFile(srvConf, []byte("port 1194\nserver 10.8.0.0 255.255.255.0\n"), 0o644)
	fakeProc(t, root, 100, "openvpn", []string{"/usr/sbin/openvpn", "--log", "/l.log", "--cd", "/p", "--config", "office.ovpn", "--management", "/run/x.sock", "unix"}, "0::/user.slice/user-1000.slice/session-2.scope\n")
	fakeProc(t, root, 200, "openvpn", []string{"/usr/bin/openvpn", "--suppress-timestamps", "--nobind", "--config", "home.conf"}, "0::/system.slice/system-openvpn\\x2dclient.slice/openvpn-client@home.service\n")
	fakeProc(t, root, 300, "bash", []string{"bash"}, "")
	fakeProc(t, root, 400, "openvpn", []string{"openvpn", "--config", srvConf}, "")
	_ = os.MkdirAll(filepath.Join(root, "self"), 0o755)

	procs, err := ProcFS{Root: root}.FindOpenVPN()
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 3 {
		t.Fatalf("found %d processes: %+v", len(procs), procs)
	}
	byPID := map[int]Proc{}
	for _, p := range procs {
		byPID[p.PID] = p
	}
	p := byPID[100]
	if p.Config != "/p/office.ovpn" || p.Socket != "/run/x.sock" || p.Log != "/l.log" || p.Unit != "" || p.StartTicks != 987654 || p.UID != 0 || p.Server {
		t.Fatalf("pid 100: %+v", p)
	}
	if u := byPID[200].Unit; u != "openvpn-client@home.service" {
		t.Fatalf("unit = %q", u)
	}
	if byPID[200].Name() != "openvpn-client@home.service" {
		t.Fatal("Name should prefer the unit")
	}
	if !byPID[400].Server {
		t.Fatal("server instance not recognised")
	}
	fs := ProcFS{Root: root}
	if !fs.Alive(100, 987654) || fs.Alive(100, 1) || fs.Alive(300, 0) || fs.Alive(999, 0) {
		t.Fatal("Alive checks")
	}
}

func TestStatParsingWithParens(t *testing.T) {
	stat := []byte("42 (weird ) name) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 5555 0 0")
	if got := parseStartTicks(stat); got != 5555 {
		t.Fatalf("got %d", got)
	}
}
