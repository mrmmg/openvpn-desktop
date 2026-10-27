package privilege

import (
	"context"
	"errors"
	"testing"

	"github.com/mrmmg/openvpn-wails/internal/process"
)

func TestAppleScriptEscaping(t *testing.T) {
	got := AppleScript([]string{"/usr/bin/openvpn", "--config", `/p/it's "x".ovpn`})
	want := `do shell script "'/usr/bin/openvpn' '--config' '/p/it'\\''s \"x\".ovpn' >/dev/null 2>&1" with administrator privileges`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func fake(out string, code int, err error) runFunc {
	return func(context.Context, []string) (string, int, error) { return out, code, err }
}

func TestClassify(t *testing.T) {
	spec := process.LaunchSpec{Binary: "/b", ProfilePath: "/p", Dir: "/d", LogPath: "/l", Socket: "/s"}
	boom := errors.New("exit status")
	cases := []struct {
		method string
		code   int
		out    string
		want   error
	}{
		{"pkexec", 126, "", ErrCancelled},
		{"pkexec", 127, "", ErrNotAuthorized},
		{"osascript", 1, "execution error: User canceled. (-128)", ErrCancelled},
	}
	for _, c := range cases {
		l := &Launcher{method: c.method, helper: "/h", timeout: 1, run: fake(c.out, c.code, boom)}
		l.timeout = 1e9
		if err := l.Launch(context.Background(), spec); !errors.Is(err, c.want) {
			t.Errorf("%s/%d: got %v want %v", c.method, c.code, err, c.want)
		}
	}
	l := &Launcher{method: "pkexec", timeout: 1e9, run: fake("", 0, nil)}
	if err := l.Launch(context.Background(), spec); !errors.Is(err, ErrNoHelper) {
		t.Errorf("missing helper: %v", err)
	}
	l = &Launcher{method: "pkexec", helper: "/h", timeout: 1e9, run: fake("", 0, nil)}
	if err := l.Launch(context.Background(), spec); err != nil {
		t.Errorf("success: %v", err)
	}
}

func TestWrapPkexec(t *testing.T) {
	l := &Launcher{method: "pkexec", helper: "/usr/bin/pkexec"}
	got := l.Wrap([]string{"/usr/bin/openvpn", "--x"})
	if got[0] != "/usr/bin/pkexec" || got[1] != "/usr/bin/openvpn" {
		t.Fatalf("%v", got)
	}
}
