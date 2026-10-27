package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	env := map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/cfg", "XDG_STATE_HOME": "relative", "XDG_RUNTIME_DIR": "/run/user/1000"}
	d, err := Resolve(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if d.Config != "/cfg/openvpn-wails" || d.State != "/home/u/.local/state/openvpn-wails" || d.Runtime != "/run/user/1000/openvpn-wails" {
		t.Fatalf("%+v", d)
	}
	delete(env, "XDG_RUNTIME_DIR")
	d, _ = Resolve(func(k string) string { return env[k] })
	if !strings.HasPrefix(filepath.Base(d.Runtime), "openvpn-wails-") {
		t.Fatalf("runtime fallback %s", d.Runtime)
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("FOO", "bar")
	if Expand("~/x/$FOO", "/h") != "/h/x/bar" || Expand("~", "/h") != "/h" || Expand("", "/h") != "" {
		t.Fatal("expand")
	}
}

func TestPrivateDirAndAtomicWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0o755)
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	link := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink(dir, link)
	if err := EnsurePrivateDir(link); err == nil {
		t.Fatal("symlinked dir must be refused")
	}
	f := filepath.Join(dir, "f")
	if err := WriteFileAtomic(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temp file left behind")
	}
	if CheckSocketPath("/run/user/1000/openvpn-wails/mgmt.sock") != nil || CheckSocketPath("/"+strings.Repeat("a", 120)) == nil {
		t.Fatal("socket path check")
	}
}
