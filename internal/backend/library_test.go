package backend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrmmg/openvpn-desktop/internal/appconfig"
	"github.com/mrmmg/openvpn-desktop/internal/paths"
)

func newLib(t *testing.T) (*Library, string) {
	t.Helper()
	d := paths.Dirs{Home: t.TempDir(), Config: t.TempDir()}
	cfg, err := appconfig.Open(filepath.Join(d.Config, "config.yaml"), d)
	if err != nil {
		t.Fatal(err)
	}
	return NewLibrary(cfg), t.TempDir()
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const sample = "client\ndev tun\nproto udp\nremote vpn.example.com 1194\nca ca.crt\n"

func TestImportKeepsReferencedFilesAndOriginal(t *testing.T) {
	lib, src := newLib(t)
	write(t, filepath.Join(src, "Office VPN.ovpn"), sample)
	write(t, filepath.Join(src, "ca.crt"), "CERT")
	res, err := lib.Import(filepath.Join(src, "Office VPN.ovpn"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Entry.ID != "office-vpn" || res.Entry.Name != "Office VPN" || res.Entry.File != "Office VPN.ovpn" {
		t.Fatalf("entry: %+v", res.Entry)
	}
	if b, _ := os.ReadFile(filepath.Join(lib.Root(), "office-vpn", "ca.crt")); string(b) != "CERT" {
		t.Fatal("referenced ca.crt not copied next to the profile")
	}
	if b, _ := os.ReadFile(filepath.Join(src, "Office VPN.ovpn")); string(b) != sample {
		t.Fatal("original modified")
	}
}

func TestSameNamedCertsDoNotCollide(t *testing.T) {
	lib, _ := newLib(t)
	for i, cert := range []string{"A", "B"} {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "vpn.ovpn"), sample)
		write(t, filepath.Join(dir, "ca.crt"), cert)
		res, err := lib.Import(filepath.Join(dir, "vpn.ovpn"))
		if err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
		if b, _ := os.ReadFile(filepath.Join(lib.Root(), res.Entry.ID, "ca.crt")); string(b) != cert {
			t.Fatalf("profile %d has wrong cert %q", i, b)
		}
	}
	list, _ := lib.List()
	if len(list) != 2 || list[0].ID == list[1].ID {
		t.Fatalf("list: %+v", list)
	}
}

func TestImportRejectsBadFiles(t *testing.T) {
	lib, src := newLib(t)
	cases := map[string]string{
		"notes.txt":     sample,
		"empty.ovpn":    "",
		"noremote.ovpn": "client\ndev tun\n",
		"bin.ovpn":      "client\x00\x01remote a 1",
	}
	for name, content := range cases {
		write(t, filepath.Join(src, name), content)
		if _, err := lib.Import(filepath.Join(src, name)); !errors.Is(err, ErrNotProfile) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if list, _ := lib.List(); len(list) != 0 {
		t.Fatalf("failed imports left profiles behind: %+v", list)
	}
	if ents, _ := os.ReadDir(lib.Root()); len(ents) != 0 {
		t.Fatalf("failed imports left directories behind: %v", ents)
	}
}

func TestRenameAndRemove(t *testing.T) {
	lib, src := newLib(t)
	write(t, filepath.Join(src, "a.ovpn"), "client\nremote h 1\n")
	res, err := lib.Import(filepath.Join(src, "a.ovpn"))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Entry.ID
	if err := lib.Rename(id, "  Home  "); err != nil {
		t.Fatal(err)
	}
	if e, _ := lib.Get(id); e.Name != "Home" || e.File != "a.ovpn" || e.Profile.Name != id+".ovpn" {
		t.Fatalf("after rename: %+v", e)
	}
	if err := lib.Rename(id, " "); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := lib.Remove("../x"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := lib.Remove(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib.Root(), id)); !os.IsNotExist(err) {
		t.Fatal("directory still exists")
	}
	if _, ok := lib.cfg.Get().Profiles[id]; ok {
		t.Fatal("metadata still present")
	}
}

func TestIDForPath(t *testing.T) {
	lib, _ := newLib(t)
	p := filepath.Join(lib.Root(), "work", "work.ovpn")
	if got := lib.IDForPath(p); got != "work" {
		t.Fatalf("got %q", got)
	}
	if got := lib.IDForPath("/etc/x.ovpn"); got != "" {
		t.Fatalf("outside root: %q", got)
	}
}

func TestLooseFilesAreListed(t *testing.T) {
	lib, _ := newLib(t)
	root := lib.Root()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "My VPN.ovpn"), "client\nremote h 1\n")
	write(t, filepath.Join(root, "My VPN.txt"), "user\npass\n")
	write(t, filepath.Join(root, "notes.txt"), "x")
	hand := filepath.Join(root, "handmade")
	_ = os.MkdirAll(hand, 0o700)
	write(t, filepath.Join(hand, "other.ovpn"), "client\nremote h 2\n")
	list, err := lib.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	e, err := lib.Get("My VPN.ovpn")
	if err != nil || e.Name != "My VPN" || e.Profile.Path != filepath.Join(root, "My VPN.ovpn") {
		t.Fatalf("loose: %v %+v", err, e)
	}
	if id := lib.IDForPath(e.Profile.Path); id != "My VPN.ovpn" {
		t.Fatalf("id %q", id)
	}
	if err := lib.Remove("My VPN.ovpn"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "My VPN.txt")); !os.IsNotExist(err) {
		t.Fatal("credential file should go with the profile")
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal("unrelated file removed")
	}
}
