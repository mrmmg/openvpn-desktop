package vpn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/analyzer"
	"github.com/mrmmg/openvpn-desktop/internal/credentials"
	"github.com/mrmmg/openvpn-desktop/internal/network"
	"github.com/mrmmg/openvpn-desktop/internal/ovpn"
	"github.com/mrmmg/openvpn-desktop/internal/process"
	"github.com/mrmmg/openvpn-desktop/internal/profile"
	"github.com/mrmmg/openvpn-desktop/internal/state"
)

// These tests run a real OpenVPN server and client on 127.0.0.1 using
// "dev null" with ifconfig/route-noexec, so they need neither root nor a
// tunnel device and never change the host's network configuration.

type fakeNet struct{}

func (fakeNet) Links(context.Context) ([]network.Link, error) { return nil, nil }
func (fakeNet) Counters(string) (network.Counters, error)     { return network.Counters{}, nil }
func (fakeNet) Routes(context.Context) ([]network.Route, error) {
	return nil, nil
}
func (fakeNet) RouteGet(_ context.Context, dst netip.Addr) network.RouteDecision {
	return network.RouteDecision{Dst: dst, Dev: "eth0"}
}

func openvpnBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	p, err := exec.LookPath("openvpn")
	if err != nil {
		t.Skip("openvpn not installed")
	}
	return p
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// makePKI creates a CA, a server and a client certificate in dir.
func makePKI(t *testing.T, dir string) {
	t.Helper()
	newKey := func() *rsa.PrivateKey {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	caKey := newKey()
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	writePEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", caDER)
	issue := func(name string, serial int64, eku x509.ExtKeyUsage) {
		k := newKey()
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{eku},
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		writePEM(t, filepath.Join(dir, name+".crt"), "CERTIFICATE", der)
		writePEM(t, filepath.Join(dir, name+".key"), "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(k))
	}
	issue("server", 2, x509.ExtKeyUsageServerAuth)
	issue("client", 3, x509.ExtKeyUsageClientAuth)
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

type env struct {
	mgr   *Manager
	store *profile.Store
	dir   string
}

func setup(t *testing.T) env {
	bin := openvpnBinary(t)
	// Short base path: unix socket paths are limited to ~100 bytes.
	base, err := os.MkdirTemp("", "ovt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	pki := filepath.Join(base, "pki")
	profiles := filepath.Join(base, "profiles")
	runtime := filepath.Join(base, "rt")
	for _, d := range []string{pki, profiles, runtime} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	makePKI(t, pki)
	port := freeUDPPort(t)

	verify := filepath.Join(pki, "verify.sh")
	script := "#!/bin/sh\n[ \"$(sed -n 1p \"$1\")\" = alice ] && [ \"$(sed -n 2p \"$1\")\" = s3cret ]\n"
	if err := os.WriteFile(verify, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	serverConf := fmt.Sprintf(`mode server
tls-server
dev null
proto udp4
local 127.0.0.1
port %d
ca %[2]s/ca.crt
cert %[2]s/server.crt
key %[2]s/server.key
dh none
topology subnet
ifconfig 10.99.0.1 255.255.255.0
ifconfig-pool 10.99.0.10 10.99.0.20
ifconfig-noexec
route-noexec
script-security 2
auth-user-pass-verify %[3]s via-file
push "redirect-gateway def1"
push "route 10.50.0.0 255.255.0.0"
push "dhcp-option DNS 10.99.0.1"
verb 3
`, port, pki, verify)
	serverPath := filepath.Join(pki, "server.conf")
	if err := os.WriteFile(serverPath, []byte(serverConf), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(bin, "--config", serverPath, "--log", filepath.Join(pki, "server.log"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	})

	// The client profile references its PKI with relative paths.
	for _, f := range []string{"ca.crt", "client.crt", "client.key"} {
		data, _ := os.ReadFile(filepath.Join(pki, f))
		_ = os.WriteFile(filepath.Join(profiles, f), data, 0o600)
	}
	clientConf := fmt.Sprintf(`client
dev null
proto udp4
remote 127.0.0.1 %d
nobind
ca ca.crt
cert client.crt
key client.key
remote-cert-tls server
auth-user-pass
ifconfig-noexec
route-noexec
connect-retry 1
verb 3
`, port)
	if err := os.WriteFile(filepath.Join(profiles, "test.ovpn"), []byte(clientConf), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &profile.Store{Dir: profiles, Extensions: []string{".ovpn"}, CredExt: ".txt"}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mgr := New(Options{
		Binary: bin, Verbosity: 3, LogDir: filepath.Join(base, "logs"), RuntimeDir: runtime, CredExt: ".txt",
		ConnectTimeout: time.Minute, StopTimeout: 10 * time.Second,
	}, process.Elevator{Method: "none", EUID: os.Geteuid()}, process.DefaultTable(), fakeNet{}, nil,
		state.NewHistory(filepath.Join(base, "history.jsonl")), log)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		mgr.Close(ctx, true)
	})
	return env{mgr: mgr, store: store, dir: profiles}
}

func waitFor(t *testing.T, m *Manager, what string, cond func(Status) bool) Status {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		st := m.Status()
		if cond(st) {
			return st
		}
		select {
		case <-m.Updates():
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			lines := m.SessionLog()
			t.Fatalf("timeout waiting for %s; status=%+v\nlog tail:\n%s", what, st, strings.Join(lines[max(0, len(lines)-20):], "\n"))
		}
	}
}

func start(t *testing.T, e env) {
	t.Helper()
	p, err := e.store.Get("test.ovpn")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.mgr.PrepareStart(p, state.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.mgr.Launch(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := e.mgr.FinishStart(ctx, plan); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationPromptedCredentials(t *testing.T) {
	e := setup(t)
	start(t, e)

	// No credential file: OpenVPN asks through the management interface.
	st := waitFor(t, e.mgr, "password prompt", func(s Status) bool { return s.Password != nil })
	if !st.Password.NeedUsername || st.Password.Type != "Auth" {
		t.Fatalf("unexpected prompt %+v", st.Password)
	}
	ctx := context.Background()
	if err := e.mgr.SubmitCredentials(ctx, "alice", "wrong", false); err != nil {
		t.Fatal(err)
	}
	// auth-retry interact: failure is reported and a new prompt follows.
	waitFor(t, e.mgr, "second prompt after failure", func(s Status) bool { return s.Password != nil && s.Failure != nil && s.Failure.Auth })
	if err := e.mgr.SubmitCredentials(ctx, "alice", "s3cret", true); err != nil {
		t.Fatal(err)
	}
	st = waitFor(t, e.mgr, "connected", func(s Status) bool { return s.Phase == Connected })
	if st.LocalIP == "" || st.RemoteIP != "127.0.0.1" || st.Source != SourceManaged || !st.Attached {
		t.Fatalf("unexpected connected status %+v", st)
	}
	// Saved credential file must be private and contain both lines.
	credPath := filepath.Join(e.dir, "test.txt")
	fi, err := os.Stat(credPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("credential file: %v %v", fi, err)
	}
	if u, p, _ := credentials.Read(credPath); u != "alice" || p != "s3cret" {
		t.Fatalf("credential file content mismatch")
	}
	// The password must not appear in the OpenVPN log.
	for _, l := range e.mgr.SessionLog() {
		if strings.Contains(l, "s3cret") {
			t.Fatalf("password leaked into log: %q", l)
		}
	}

	// Pushed options are visible and classified.
	cfg, _ := ovpn.ParseFile(filepath.Join(e.dir, "test.ovpn"))
	pushed := analyzer.ParsePushed(e.mgr.SessionLog(), analyzer.AnalyzeStatic(cfg))
	if !pushed.Received {
		t.Fatal("PUSH_REPLY not found in session log")
	}
	eff := analyzer.Effective(cfg, pushed)
	if eff.IPv4.Mode != analyzer.ModeFull {
		t.Fatalf("effective IPv4 mode = %s, want full (server pushed redirect-gateway)", eff.IPv4.Mode)
	}

	if err := e.mgr.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	st = e.mgr.Status()
	// The local test server is an OpenVPN *server* and must not be mistaken
	// for a client connection.
	if st.Phase != Disconnected || st.Source != SourceNone || st.LocalIP != "" || len(st.Servers) != 1 || len(st.External) != 0 {
		t.Fatalf("after stop: %+v", st)
	}
	entries, err := e.mgr.history.List()
	if err != nil || len(entries) == 0 || entries[0].Reason != "stopped by user" || entries[0].Connected.IsZero() {
		t.Fatalf("history after stop: %+v %v", entries, err)
	}
	if _, err := os.Stat(e.mgr.instancePath()); !os.IsNotExist(err) {
		t.Fatal("instance record not removed after stop")
	}
}

func TestIntegrationCredentialFileAndReattach(t *testing.T) {
	e := setup(t)
	if err := credentials.Write(filepath.Join(e.dir, "test.txt"), "alice", "s3cret"); err != nil {
		t.Fatal(err)
	}
	start(t, e)
	st := waitFor(t, e.mgr, "connected", func(s Status) bool { return s.Phase == Connected })
	pid := st.PID

	// Simulate an app restart: a new manager re-attaches via the instance record.
	m2 := New(e.mgr.opts, e.mgr.launch, e.mgr.procs, fakeNet{}, nil, nil, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Detach the first manager without stopping OpenVPN.
	e.mgr.Close(ctx, false)
	if err := m2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	st2 := waitFor(t, m2, "re-attached and connected", func(s Status) bool { return s.Phase == Connected && s.Attached })
	if st2.PID != pid || st2.Profile != "test.ovpn" {
		t.Fatalf("re-attached to wrong instance: %+v", st2)
	}
	if err := m2.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	m2.Close(ctx, false)
}

func TestIntegrationUnexpectedExit(t *testing.T) {
	e := setup(t)
	if err := credentials.Write(filepath.Join(e.dir, "test.txt"), "alice", "s3cret"); err != nil {
		t.Fatal(err)
	}
	start(t, e)
	st := waitFor(t, e.mgr, "connected", func(s Status) bool { return s.Phase == Connected })
	// Kill OpenVPN behind the manager's back.
	proc, _ := os.FindProcess(st.PID)
	_ = proc.Kill()
	st = waitFor(t, e.mgr, "failure", func(s Status) bool { return s.Phase == Failed })
	if st.Failure == nil {
		t.Fatal("no failure reason")
	}
	select {
	case n := <-e.mgr.Notices():
		_ = n // connected notice first
	default:
	}
}

func TestIntegrationAuthFailureWithFile(t *testing.T) {
	e := setup(t)
	if err := credentials.Write(filepath.Join(e.dir, "test.txt"), "alice", "bad"); err != nil {
		t.Fatal(err)
	}
	start(t, e)
	st := waitFor(t, e.mgr, "auth failure", func(s Status) bool { return s.Phase == Failed })
	if st.Failure == nil || !st.Failure.Auth {
		t.Fatalf("expected an authentication failure, got %+v", st.Failure)
	}
}
