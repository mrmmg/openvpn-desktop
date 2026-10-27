package validate

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrmmg/openvpn-wails/internal/profile"
	"github.com/mrmmg/openvpn-wails/internal/state"
)

type fakeRunner struct {
	res   RunResult
	err   error
	extra []string
}

func (f *fakeRunner) TestConfig(_ context.Context, _, _ string, extra []string) (RunResult, error) {
	f.extra = extra
	return f.res, f.err
}

// pki writes ca.crt, client.crt and client.key (cert valid for validity).
func pki(t *testing.T, dir string, notAfter time.Time) {
	t.Helper()
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "client"}, NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: notAfter}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca, &k.PublicKey, caKey)
	write := func(name, typ string, b []byte) {
		_ = os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600)
	}
	write("ca.crt", "CERTIFICATE", caDER)
	write("client.crt", "CERTIFICATE", der)
	pk8, _ := x509.MarshalPKCS8PrivateKey(k)
	write("client.key", "PRIVATE KEY", pk8)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	write("other.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(other))
}

func setupProfile(t *testing.T, body string) profile.Profile {
	t.Helper()
	dir := t.TempDir()
	pki(t, dir, time.Now().AddDate(1, 0, 0))
	_ = os.WriteFile(filepath.Join(dir, "p.ovpn"), []byte(body), 0o600)
	s := &profile.Store{Dir: dir, Extensions: []string{".ovpn"}, CredExt: ".txt"}
	p, err := s.Get("p.ovpn")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func find(r Report, layer Layer, title string) *Check {
	for i, c := range r.Checks {
		if c.Layer == layer && strings.Contains(c.Title, title) {
			return &r.Checks[i]
		}
	}
	return nil
}

const goodProfile = "client\ndev tun\nremote vpn.example.com 1194\nca ca.crt\ncert client.crt\nkey client.key\nremote-cert-tls server\n"

func TestValidProfile(t *testing.T) {
	p := setupProfile(t, goodProfile)
	fr := &fakeRunner{res: RunResult{Output: "2026-01-01 10:00:00 OpenVPN crypto self-test mode SUCCEEDED.\n"}}
	v := &Validator{Runner: fr, CredExt: ".txt"}
	r := v.Validate(context.Background(), p, state.Overrides{RouteNoPull: true})
	if !r.Valid() {
		t.Fatalf("expected valid: %v", r.Checks)
	}
	if find(r, LayerCrypto, "private key matches certificate") == nil || find(r, LayerParser, "accepted") == nil {
		t.Fatalf("missing checks: %v", r.Checks)
	}
	if strings.Join(fr.extra, " ") != "--route-nopull" {
		t.Fatalf("overrides must be validated too: %v", fr.extra)
	}
	if r.Summary() != "VALID" {
		t.Fatalf("summary %s", r.Summary())
	}
}

func TestStaticAndCryptoErrors(t *testing.T) {
	body := "client\nremote x\nca ca.crt\ncert client.crt\nkey other.key\ntls-auth ta.key 1\ncomp-lzo\n<tls-crypt>\nnot a key\n</tls-crypt>\n"
	p := setupProfile(t, body)
	v := &Validator{Runner: &fakeRunner{res: RunResult{Output: "self-test mode SUCCEEDED"}}, CredExt: ".txt"}
	r := v.Validate(context.Background(), p, state.Overrides{})
	if r.Valid() {
		t.Fatal("expected invalid")
	}
	for _, c := range []struct {
		layer Layer
		title string
		sev   Severity
	}{
		{LayerStatic, "tls-auth file missing", Error},
		{LayerStatic, "compression", Warn},
		{LayerStatic, "server identity not pinned", Warn},
		{LayerCrypto, "does not match", Error},
		{LayerCrypto, "tls-crypt key invalid", Error},
	} {
		got := find(r, c.layer, c.title)
		if got == nil || got.Severity != c.sev {
			t.Errorf("%s/%q: got %+v", c.layer, c.title, got)
		}
	}
}

func TestExpiredCertificate(t *testing.T) {
	dir := t.TempDir()
	pki(t, dir, time.Now().Add(-time.Hour))
	_ = os.WriteFile(filepath.Join(dir, "p.ovpn"), []byte(goodProfile), 0o600)
	p, _ := (&profile.Store{Dir: dir, Extensions: []string{".ovpn"}}).Get("p.ovpn")
	r := (&Validator{CredExt: ".txt"}).Validate(context.Background(), p, state.Overrides{})
	if c := find(r, LayerCrypto, "client certificate expired"); c == nil || c.Severity != Error {
		t.Fatalf("expired cert not reported: %v", r.Checks)
	}
	if r.Worst(LayerParser) != Skipped {
		t.Fatal("nil runner must skip the parser layer")
	}
}

func TestParserOutput(t *testing.T) {
	p := setupProfile(t, goodProfile)
	out := "2026-01-01 10:00:00 WARNING: --foo is deprecated\nOptions error: Unrecognized option or missing or extra parameter(s) in p.ovpn:7: bogus (2.7.7)\nOptions error: Please correct these errors.\n"
	v := &Validator{Runner: &fakeRunner{res: RunResult{Output: out, ExitCode: 1}}, CredExt: ".txt"}
	r := v.Validate(context.Background(), p, state.Overrides{})
	c := find(r, LayerParser, "rejected")
	if c == nil || c.Line != 7 || c.Severity != Error {
		t.Fatalf("parser error: %+v", c)
	}
	if find(r, LayerParser, "warning") == nil {
		t.Fatal("warning lost")
	}
	if strings.Contains(r.FirstError(), "Please correct") {
		t.Fatal("boilerplate must be dropped")
	}
	v.Runner = &fakeRunner{err: ErrBinaryNotFound}
	if c := find(v.Validate(context.Background(), p, state.Overrides{}), LayerParser, "not found"); c == nil {
		t.Fatal("missing binary not reported")
	}
}

func TestCredentialLayer(t *testing.T) {
	p := setupProfile(t, goodProfile+"auth-user-pass\n")
	v := &Validator{CredExt: ".txt"}
	r := v.Validate(context.Background(), p, state.Overrides{})
	if c := find(r, LayerCredentials, "credentials missing"); c == nil || c.Severity != Info {
		t.Fatalf("missing creds should be info (prompted): %v", r.Checks)
	}
	_ = os.WriteFile(p.CredPath, []byte("alice\nsecret\n"), 0o644)
	r = v.Validate(context.Background(), p, state.Overrides{})
	if c := find(r, LayerCredentials, "permissions too open"); c == nil || c.Severity != Warn {
		t.Fatalf("insecure permissions not flagged: %v", r.Checks)
	}
	for _, c := range r.Checks {
		if strings.Contains(c.Detail, "secret") || strings.Contains(c.Title, "secret") {
			t.Fatal("password leaked into report")
		}
	}
	p2 := setupProfile(t, goodProfile+"auth-user-pass missing.txt\n")
	r = v.Validate(context.Background(), p2, state.Overrides{})
	if c := find(r, LayerCredentials, "credential file missing"); c == nil || c.Severity != Error {
		t.Fatalf("missing referenced file: %v", r.Checks)
	}
}

func TestUnreadableProfile(t *testing.T) {
	p := profile.Profile{Name: "x.ovpn", Path: filepath.Join(t.TempDir(), "x.ovpn")}
	r := (&Validator{}).Validate(context.Background(), p, state.Overrides{})
	if r.Valid() || r.Worst(LayerCrypto) != Skipped {
		t.Fatalf("%v", r.Checks)
	}
}

// TestRealOpenVPN runs the actual binary in --test-crypto mode.
func TestRealOpenVPN(t *testing.T) {
	bin, err := exec.LookPath("openvpn")
	if err != nil || testing.Short() {
		t.Skip("openvpn not available")
	}
	p := setupProfile(t, goodProfile+"log /tmp/should-not-be-created.log\ndaemon\n")
	v := &Validator{Runner: ExecRunner{Binary: bin, TempDir: t.TempDir()}, CredExt: ".txt"}
	r := v.Validate(context.Background(), p, state.Overrides{})
	if c := find(r, LayerParser, "accepted"); c == nil {
		t.Fatalf("real openvpn did not accept a valid profile: %v", r.Checks)
	}
	bad := setupProfile(t, goodProfile+"no-such-option 1\n")
	r = v.Validate(context.Background(), bad, state.Overrides{})
	if c := find(r, LayerParser, "rejected"); c == nil || c.Line != 8 {
		t.Fatalf("real openvpn error not parsed: %v", r.Checks)
	}
	missing := setupProfile(t, strings.Replace(goodProfile, "ca ca.crt", "ca nope.crt", 1))
	r = v.Validate(context.Background(), missing, state.Overrides{})
	if find(r, LayerParser, "rejected") == nil || find(r, LayerStatic, "ca file missing") == nil {
		t.Fatalf("missing ca: %v", r.Checks)
	}
}
