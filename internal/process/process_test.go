package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrmmg/openvpn-desktop/internal/paths"
)

func TestBuildArgs(t *testing.T) {
	s := LaunchSpec{
		Binary: "/usr/sbin/openvpn", ProfilePath: "/p/office.ovpn", Dir: "/p", LogPath: "/s/office.log",
		Socket: "/run/user/1000/openvpn-desktop/mgmt.sock", Verbosity: 3, CredFile: "/p/office.txt",
		ClientUser: "alice", ExtraArgs: []string{"--route-nopull"},
	}
	args := BuildArgs(s)
	joined := strings.Join(args, " ")
	logIdx, cfgIdx := indexOf(args, "--log"), indexOf(args, "--config")
	if logIdx < 0 || cfgIdx < 0 || logIdx > cfgIdx {
		t.Fatalf("--log must precede --config: %s", joined)
	}
	for _, want := range []string{"--daemon openvpn-desktop", "--management /run/user/1000/openvpn-desktop/mgmt.sock unix", "--management-client-user alice", "--auth-user-pass /p/office.txt", "--management-query-passwords"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if args[len(args)-1] != "--route-nopull" {
		t.Error("overrides must come last")
	}
	if strings.Contains(joined, "auth-retry") {
		t.Error("auth-retry interact only when prompting")
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Socket = "/" + strings.Repeat("x", paths.MaxSocketPath)
	if err := s.Validate(); err == nil {
		t.Fatal("long socket path must be rejected")
	}
	s.Socket, s.LogPath = "/run/s.sock", "relative.log"
	if err := s.Validate(); err == nil {
		t.Fatal("relative path must be rejected")
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestElevator(t *testing.T) {
	root := Elevator{Method: "auto", Sudo: "sudo", EUID: 0}
	if root.Effective() != "none" || len(root.Wrap([]string{"x"})) != 1 {
		t.Fatal("root needs no elevation")
	}
	user := Elevator{Method: "auto", Sudo: "/usr/bin/sudo", EUID: 1000}
	if w := user.Wrap([]string{"openvpn"}); strings.Join(w, " ") != "/usr/bin/sudo -n -- openvpn" {
		t.Fatalf("sudo wrap = %v", w)
	}
	if user.Interactive() || user.AuthCommand() == nil {
		t.Fatal("sudo is non-interactive with an auth step")
	}
	pk := Elevator{Method: "pkexec", Pkexec: "pkexec", EUID: 1000}
	if !pk.Interactive() || pk.AuthCommand() != nil {
		t.Fatal("pkexec is interactive")
	}
	if !IsAuthFailure("sudo: a password is required\n") || IsAuthFailure("permission denied") {
		t.Fatal("IsAuthFailure")
	}
}

func TestPrepareFiles(t *testing.T) {
	base := t.TempDir()
	s := LaunchSpec{LogPath: filepath.Join(base, "logs", "p.log"), Socket: filepath.Join(base, "rt", "mgmt.sock")}
	_ = os.MkdirAll(filepath.Dir(s.LogPath), 0o700)
	_ = os.WriteFile(s.LogPath, []byte("old session\n"), 0o644)
	_ = os.MkdirAll(filepath.Dir(s.Socket), 0o755)
	_ = os.WriteFile(s.Socket, nil, 0o600)
	if err := PrepareFiles(s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(s.LogPath + ".1"); string(b) != "old session\n" {
		t.Fatal("previous log not rotated")
	}
	if fi, _ := os.Stat(s.LogPath); fi.Size() != 0 || fi.Mode().Perm() != 0o600 {
		t.Fatal("fresh private log expected")
	}
	if _, err := os.Stat(s.Socket); !os.IsNotExist(err) {
		t.Fatal("stale socket not removed")
	}
	if fi, _ := os.Stat(filepath.Dir(s.Socket)); fi.Mode().Perm() != 0o700 {
		t.Fatal("runtime dir must be tightened to 0700")
	}
	// Refuse a symlinked log file.
	_ = os.Remove(s.LogPath)
	_ = os.Symlink("/etc/passwd", s.LogPath)
	if err := PrepareFiles(s); err == nil {
		t.Fatal("symlinked log must be refused")
	}
}

func TestInstanceRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt", "instance.json")
	if in, err := LoadInstance(path); in != nil || err != nil {
		t.Fatal("missing record")
	}
	if err := SaveInstance(path, Instance{Profile: "a", PID: 5, StartTicks: 9}); err != nil {
		t.Fatal(err)
	}
	in, err := LoadInstance(path)
	if err != nil || in.PID != 5 || in.StartTicks != 9 {
		t.Fatalf("%+v %v", in, err)
	}
	RemoveInstance(path)
	_ = os.WriteFile(path, []byte("{"), 0o600)
	if _, err := LoadInstance(path); err == nil {
		t.Fatal("corrupt record must error")
	}
}
