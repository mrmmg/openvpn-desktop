package applog

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedaction(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewHandler(&buf, slog.LevelDebug))
	log.Info("send", "password", "hunter2", "Auth_Token", "abc", "cmd", `password "Auth" "hunter2"`, "err", errors.New(`username "Auth" "bob" rejected`))
	out := buf.String()
	for _, secret := range []string{"hunter2", "abc", `"bob"`} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked: %s", secret, out)
		}
	}
	if !strings.Contains(out, Redacted) {
		t.Fatal("no redaction marker")
	}
	if got := RedactString(`password "Private Key" "a\"b"`); strings.Contains(got, `a\"b`) {
		t.Fatalf("escaped quote leaked: %s", got)
	}
}

func TestOpenRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "app.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, bytes.Repeat([]byte("x"), 2<<20), 0o600)
	log, c, err := Open(path, "debug", 1)
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("hello")
	_ = c.Close()
	if fi, err := os.Stat(path + ".1"); err != nil || fi.Size() != 2<<20 {
		t.Fatal("not rotated")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "hello") {
		t.Fatal("new log missing entry")
	}
	if ParseLevel("warning") != slog.LevelWarn || ParseLevel("nonsense") != slog.LevelInfo {
		t.Fatal("ParseLevel")
	}
}
