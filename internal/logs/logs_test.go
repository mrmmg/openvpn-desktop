package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := map[string]Class{
		"2026-01-01 10:00:00 AUTH: Received control message: AUTH_FAILED":       Auth,
		"TLS Error: TLS key negotiation failed to occur within 60 seconds":      TLS,
		"Options error: Unrecognized option":                                    Error,
		"WARNING: --ns-cert-type is DEPRECATED":                                 Warning,
		"net_route_v4_add: 0.0.0.0/1 via 10.8.0.1 dev [NULL] table 0 metric -1": Route,
		"PUSH: Received control message: 'PUSH_REPLY,route 10.0.0.0 255.0.0.0'": Route,
		"Initialization Sequence Completed":                                     Connection,
		"MANAGEMENT: CMD 'state on all'":                                        Debug,
		"library versions: OpenSSL 3.6":                                         Plain,
	}
	for line, want := range cases {
		if got := Classify(line); got != want {
			t.Errorf("Classify(%q) = %d, want %d", line, got, want)
		}
	}
}

func TestSplitTimestamp(t *testing.T) {
	ts, msg := SplitTimestamp("2026-10-04 21:25:04 Initialization Sequence Completed")
	if ts != "2026-10-04 21:25:04" || msg != "Initialization Sequence Completed" {
		t.Fatalf("%q %q", ts, msg)
	}
	ts, msg = SplitTimestamp("Sun Oct  4 21:25:04 2026 hello")
	if ts == "" || msg != "hello" {
		t.Fatalf("legacy format: %q %q", ts, msg)
	}
	if ts, _ := SplitTimestamp("no timestamp"); ts != "" {
		t.Fatal("no timestamp expected")
	}
}

func TestFindFailure(t *testing.T) {
	cases := []struct {
		lines []string
		want  string
		auth  bool
	}{
		{[]string{"x AUTH: Received control message: AUTH_FAILED", "x SIGTERM[soft,auth-failure] received, process exiting"}, "Authentication failed", true},
		{[]string{"x Options error: --ca fails with 'ca.crt': No such file", "x Exiting due to fatal error"}, "Invalid configuration: --ca fails with 'ca.crt': No such file", false},
		{[]string{"x ERROR: Cannot ioctl TUNSETIFF tun: Operation not permitted (errno=1)", "x Exiting due to fatal error"}, "Cannot create the tunnel device", false},
		{[]string{"x RESOLVE: Cannot resolve host address: vpn.invalid:1194 (Name or service not known)"}, "Cannot resolve the VPN server's name", false},
		{[]string{"x Exiting due to fatal error"}, "OpenVPN exited due to a fatal error", false},
		{[]string{"x Initialization Sequence Completed", "x SIGTERM[hard,] received, process exiting"}, "Stopped (SIGTERM)", false},
		{[]string{"x all good"}, "", false},
	}
	for _, c := range cases {
		f := FindFailure(c.lines)
		switch {
		case c.want == "" && f != nil:
			t.Errorf("%v: unexpected %+v", c.lines, f)
		case c.want != "" && (f == nil || f.Summary != c.want || f.Auth != c.auth):
			t.Errorf("%v: got %+v, want %q", c.lines, f, c.want)
		}
	}
}

func TestReadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.log")
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	b.WriteString("partial")
	_ = os.WriteFile(path, []byte(b.String()), 0o600)
	lines, off, err := ReadTail(path, 10, 1<<20)
	if err != nil || len(lines) != 10 || strings.Contains(lines[9], "partial") {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	fi, _ := os.Stat(path)
	if off != fi.Size()-int64(len("partial")) {
		t.Fatalf("offset %d must exclude the partial line", off)
	}
	small, _, _ := ReadTail(path, 1000, 64)
	if len(small) == 0 || len(small) > 12 {
		t.Fatalf("maxBytes not honoured: %d", len(small))
	}
}

func TestFollow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.log")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := Follow(ctx, path, 0, 20*time.Millisecond)
	next := func() Batch {
		t.Helper()
		select {
		case b := <-ch:
			return b
		case <-time.After(2 * time.Second):
			t.Fatal("timeout")
		}
		return Batch{}
	}
	if b := next(); b.Err == nil {
		t.Fatal("missing file must report an error first")
	}
	_ = os.WriteFile(path, []byte("one\ntw"), 0o600)
	if b := next(); len(b.Lines) != 1 || b.Lines[0] != "one" {
		t.Fatalf("got %+v", b)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("o\nthree\n")
	_ = f.Close()
	if b := next(); strings.Join(b.Lines, ",") != "two,three" {
		t.Fatalf("partial line joining failed: %+v", b)
	}
	// Truncation (new session) resets.
	_ = os.WriteFile(path, []byte("new\n"), 0o600)
	b := next()
	if !b.Reset || len(b.Lines) != 1 || b.Lines[0] != "new" {
		t.Fatalf("truncate: %+v", b)
	}
	// Rotation (rename + new file) resets as well.
	_ = os.Rename(path, path+".1")
	_ = os.WriteFile(path, []byte("rotated\n"), 0o600)
	for {
		b = next()
		if b.Err == nil {
			break
		}
	}
	if !b.Reset || b.Lines[len(b.Lines)-1] != "rotated" {
		t.Fatalf("rotate: %+v", b)
	}
}
