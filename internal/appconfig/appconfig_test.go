package appconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrmmg/openvpn-desktop/internal/paths"
)

func dirs(t *testing.T) paths.Dirs {
	return paths.Dirs{Home: t.TempDir(), Config: t.TempDir()}
}

func TestFirstRunWritesDefaults(t *testing.T) {
	d := dirs(t)
	p := filepath.Join(d.Config, "config.yaml")
	s, err := Open(p, d)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got.Theme != ThemeSystem || got.ProfilesDir != filepath.Join(d.Config, "profiles") {
		t.Fatalf("defaults: %+v", got)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("config not written: %v", err)
	}
}

func TestUpdatePersistsAndValidates(t *testing.T) {
	d := dirs(t)
	p := filepath.Join(d.Config, "config.yaml")
	s, _ := Open(p, d)
	if _, err := s.Update(func(c *Config) { c.Theme = ThemeDark; c.Profiles["a"] = ProfileMeta{Name: "Work", File: "a.ovpn"} }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(func(c *Config) { c.Theme = "neon" }); err == nil {
		t.Fatal("invalid theme accepted")
	}
	if _, err := s.Update(func(c *Config) { c.OpenVPNBinary = "/nonexistent/openvpn" }); err == nil {
		t.Fatal("missing binary accepted")
	}
	if _, err := s.Update(func(c *Config) { c.ProfilesDir = "relative" }); err == nil {
		t.Fatal("relative dir accepted")
	}
	s2, err := Open(p, d)
	if err != nil {
		t.Fatal(err)
	}
	if g := s2.Get(); g.Theme != ThemeDark || g.Profiles["a"].Name != "Work" {
		t.Fatalf("reload: %+v", g)
	}
}

func TestCorruptFileIsReportedNotOverwritten(t *testing.T) {
	d := dirs(t)
	p := filepath.Join(d.Config, "config.yaml")
	if err := os.WriteFile(p, []byte("theme: [oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, d); err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(p); string(b) != "theme: [oops" {
		t.Fatal("corrupt file was overwritten")
	}
}

func TestIPServicesValidation(t *testing.T) {
	d := dirs(t)
	s, _ := Open(filepath.Join(d.Config, "config.yaml"), d)
	if len(s.Get().IPServices) != 4 {
		t.Fatalf("defaults: %+v", s.Get().IPServices)
	}
	bad := [][]IPService{
		{{Name: "", URL: "https://x.example", Enabled: true}},
		{{Name: "a", URL: "ftp://x.example"}},
		{{Name: "a", URL: "not a url"}},
		{{Name: "a", URL: "https://x.example"}, {Name: "A", URL: "https://y.example"}},
	}
	for i, b := range bad {
		if _, err := s.Update(func(c *Config) { c.IPServices = b }); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if _, err := s.Update(func(c *Config) {
		c.IPServices = []IPService{{Name: "mine", URL: "https://x.example/ip", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(filepath.Join(d.Config, "config.yaml"), d)
	if g := s2.Get().IPServices; len(g) != 1 || g[0].Name != "mine" {
		t.Fatalf("reload: %+v", g)
	}
}
