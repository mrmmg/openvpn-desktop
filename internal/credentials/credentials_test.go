package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrmmg/openvpn-wails/internal/ovpn"
)

func TestPathFor(t *testing.T) {
	if got := PathFor("/p/office.ovpn", ".txt"); got != "/p/office.txt" {
		t.Fatal(got)
	}
	if got := PathFor("/p/my.vpn.conf", ".auth"); got != "/p/my.vpn.auth" {
		t.Fatal(got)
	}
}

func TestWriteInspectRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	if st := Inspect(path); st.Exists {
		t.Fatal("should not exist")
	}
	if err := Write(path, "alice", "p@ss word"); err != nil {
		t.Fatal(err)
	}
	st := Inspect(path)
	if !st.Exists || !st.Complete() || st.Username != "alice" || st.Insecure() || !st.OwnedByUser {
		t.Fatalf("status = %+v", st)
	}
	if st.Mode.Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode.Perm())
	}
	u, p, err := Read(path)
	if err != nil || u != "alice" || p != "p@ss word" {
		t.Fatalf("read %q %q %v", u, p, err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "alice\np@ss word\n" {
		t.Fatalf("file format %q", raw)
	}
	// Username only.
	if err := Write(path, "bob", ""); err != nil {
		t.Fatal(err)
	}
	if st := Inspect(path); st.HasPassword || !st.HasUsername {
		t.Fatalf("username-only status %+v", st)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if !Inspect(path).Insecure() {
		t.Fatal("0644 must be insecure")
	}
	if err := Write(path, "bob", "x"); err != nil || Inspect(path).Insecure() {
		t.Fatal("rewriting must restore mode 0600")
	}
	if err := Delete(path); err != nil || Inspect(path).Exists {
		t.Fatal("delete failed")
	}
	if err := Delete(path); err != nil {
		t.Fatal("deleting a missing file must not fail")
	}
}

func TestCRLFAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	_ = os.WriteFile(path, []byte("user\r\nsecret\r\nextra\n"), 0o600)
	u, p, _ := Read(path)
	if u != "user" || p != "secret" {
		t.Fatalf("CRLF handling: %q %q", u, p)
	}
	for _, c := range [][2]string{{"", "x"}, {"a\nb", "x"}, {"a", "x\ny"}, {strings.Repeat("u", MaxFieldLen+1), "x"}} {
		err := ValidateFields(c[0], c[1])
		if err == nil {
			t.Errorf("expected error for %q", c[0])
		} else if strings.Contains(err.Error(), c[1]) && c[1] != "x" {
			t.Errorf("error leaks password: %v", err)
		}
	}
}

func TestWriteRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	_ = os.WriteFile(target, []byte("keep"), 0o600)
	link := filepath.Join(dir, "x.txt")
	_ = os.Symlink(target, link)
	if err := Write(link, "u", "p"); err == nil {
		t.Fatal("symlink must be refused")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatal("symlink target modified")
	}
}

func TestMakePlan(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "office.ovpn")
	parse := func(s string) *ovpn.Config {
		c, _ := ovpn.Parse(strings.NewReader(s))
		return c
	}
	if p := MakePlan(parse("client\n"), profilePath, ".txt"); p.Source != NotRequired {
		t.Fatalf("no auth: %v", p.Source)
	}
	if p := MakePlan(parse("auth-user-pass\n"), profilePath, ".txt"); p.Source != Prompt || !p.NeedsManagementPrompt() {
		t.Fatalf("prompt: %v", p.Source)
	}
	if p := MakePlan(parse("auth-user-pass other.txt\n"), profilePath, ".txt"); p.Source != ConfigFile || p.Path != filepath.Join(dir, "other.txt") {
		t.Fatalf("config file: %+v", p)
	}
	if p := MakePlan(parse("<auth-user-pass>\na\nb\n</auth-user-pass>\n"), profilePath, ".txt"); p.Source != Inline {
		t.Fatalf("inline: %v", p.Source)
	}
	_ = Write(filepath.Join(dir, "office.txt"), "alice", "")
	p := MakePlan(parse("auth-user-pass other.txt\n"), profilePath, ".txt")
	if p.Source != SavedFile || !p.PasswordPrompt {
		t.Fatalf("saved file must win and username-only needs prompt: %+v", p)
	}
}
