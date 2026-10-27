package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	return &Store{Dir: filepath.Join(t.TempDir(), "profiles"), Extensions: []string{".ovpn", ".conf"}, CredExt: ".txt"}
}

func TestListAndGet(t *testing.T) {
	s := newStore(t)
	if _, err := s.List(); err == nil {
		t.Fatal("missing directory must be reported")
	}
	_ = os.MkdirAll(s.Dir, 0o700)
	for _, f := range []string{"b.ovpn", "A.conf", "c.txt", ".hidden.ovpn"} {
		_ = os.WriteFile(filepath.Join(s.Dir, f), []byte("client\n"), 0o600)
	}
	_ = os.Mkdir(filepath.Join(s.Dir, "dir.ovpn"), 0o700)
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "A.conf" || list[1].Name != "b.ovpn" {
		t.Fatalf("list = %+v", list)
	}
	if list[1].CredPath != filepath.Join(s.Dir, "b.txt") || list[1].BaseName() != "b" {
		t.Fatalf("cred path %s", list[1].CredPath)
	}
	if _, err := s.Get("missing.ovpn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	for _, bad := range []string{"../x.ovpn", "a/b.ovpn", "", ".x.ovpn"} {
		if _, err := s.Get(bad); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("name %q must be rejected, got %v", bad, err)
		}
	}
}

func TestImportCopiesReferencedFiles(t *testing.T) {
	s := newStore(t)
	src := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "work.ovpn"), []byte("client\nca ca.crt\ncert keys/client.crt\nkey /abs/client.key\ntls-auth ../outside.key 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "ca.crt"), []byte("CA"), 0o644)
	_ = os.MkdirAll(filepath.Join(src, "keys"), 0o700)
	_ = os.WriteFile(filepath.Join(src, "keys", "client.crt"), []byte("CERT"), 0o644)
	_ = os.WriteFile(filepath.Join(src, "work.txt"), []byte("u\np\n"), 0o644)

	res, err := s.Import(filepath.Join(src, "work.ovpn"), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile.Name != "work.ovpn" {
		t.Fatalf("name %s", res.Profile.Name)
	}
	if len(res.Copied) != 3 { // ca.crt, keys/client.crt, work.txt
		t.Fatalf("copied = %v", res.Copied)
	}
	if len(res.Missing) != 1 { // ../outside.key
		t.Fatalf("missing = %v", res.Missing)
	}
	for _, f := range []string{"work.ovpn", "ca.crt", "keys/client.crt", "work.txt"} {
		fi, err := os.Stat(filepath.Join(s.Dir, f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, fi, err)
		}
	}
	// Importing again under the same name must not overwrite.
	if _, err := s.Import(filepath.Join(src, "work.ovpn"), ""); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	// Under another name: identical ca.crt is skipped, not an error.
	res, err = s.Import(filepath.Join(src, "work.ovpn"), "work2")
	if err != nil || res.Profile.Name != "work2.ovpn" || len(res.Skipped) != 2 {
		t.Fatalf("second import: %+v %v", res, err)
	}
	// A conflicting referenced file aborts before anything is written.
	_ = os.WriteFile(filepath.Join(src, "ca.crt"), []byte("OTHER CA"), 0o644)
	if _, err := s.Import(filepath.Join(src, "work.ovpn"), "work3"); err == nil {
		t.Fatal("conflicting ca.crt must abort")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "work3.ovpn")); !os.IsNotExist(err) {
		t.Fatal("aborted import left a profile behind")
	}
}

func TestCreateAndDelete(t *testing.T) {
	s := newStore(t)
	p, err := s.Create("new", "")
	if err != nil || p.Name != "new.ovpn" {
		t.Fatalf("create: %+v %v", p, err)
	}
	if _, err := s.Create("new.ovpn", "x"); !errors.Is(err, ErrExists) {
		t.Fatal("create must not overwrite")
	}
	_ = os.WriteFile(p.CredPath, []byte("u\np\n"), 0o600)
	if err := s.Delete("new.ovpn", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.CredPath); err != nil {
		t.Fatal("credentials must be kept when not requested")
	}
	p, _ = s.Create("new", "")
	if err := s.Delete("new.ovpn", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.CredPath); !os.IsNotExist(err) {
		t.Fatal("credentials should be deleted")
	}
}

func TestEnsureDirOnlyBelowAppDir(t *testing.T) {
	base := t.TempDir()
	s := &Store{Dir: filepath.Join(base, "app", "profiles")}
	if err := s.EnsureDir(filepath.Join(base, "app")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Dir); err != nil {
		t.Fatal("dir not created")
	}
	other := &Store{Dir: filepath.Join(base, "etc", "openvpn")}
	_ = other.EnsureDir(filepath.Join(base, "app"))
	if _, err := os.Stat(other.Dir); !os.IsNotExist(err) {
		t.Fatal("system directories must never be created")
	}
}
