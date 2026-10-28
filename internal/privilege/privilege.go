// Package privilege starts OpenVPN with the least privilege the platform
// offers for a GUI: polkit (pkexec) on Linux and the system authorization
// dialog (osascript) on macOS. The GUI itself never runs as root and no
// helper service is installed; elevation is requested per connection.
//
// Only the OpenVPN launch needs root. Disconnecting goes through the
// management socket, which is restricted to the current user.
package privilege

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/process"
)

// Errors reported to the UI.
var (
	ErrCancelled     = process.ErrCancelled
	ErrNotAuthorized = errors.New("authorization failed: this user is not allowed to run OpenVPN as administrator")
	ErrNoHelper      = errors.New("no graphical privilege helper found (install polkit and a polkit authentication agent)")
)

// Launcher implements vpn.Launcher for a desktop session.
type Launcher struct {
	method  string
	helper  string
	euid    int
	timeout time.Duration
	run     runFunc
}

type runFunc func(ctx context.Context, argv []string) (string, int, error)

// New picks the mechanism for the current platform.
func New() *Launcher {
	l := &Launcher{euid: os.Geteuid(), timeout: 3 * time.Minute, run: execRun}
	switch {
	case l.euid == 0:
		l.method = "none"
	case runtime.GOOS == "darwin":
		l.method = "osascript"
		l.helper = "/usr/bin/osascript"
	default:
		l.method = "pkexec"
		if p, err := exec.LookPath("pkexec"); err == nil {
			l.helper = p
		}
	}
	return l
}

// Effective is "pkexec", "osascript" or "none" (already root).
func (l *Launcher) Effective() string { return l.method }

// Interactive is always false: authentication happens in a graphical
// dialog, never on a terminal.
func (l *Launcher) Interactive() bool { return false }

// Describe is a short human description for the UI.
func (l *Launcher) Describe() string {
	switch l.method {
	case "pkexec":
		return "polkit (pkexec)"
	case "osascript":
		return "macOS administrator authorization"
	}
	return "running as root"
}

// InteractiveLaunchCmd is unused by the GUI.
func (l *Launcher) InteractiveLaunchCmd(process.LaunchSpec) *exec.Cmd { return nil }

// Wrap prefixes argv with the elevation helper.
func (l *Launcher) Wrap(argv []string) []string {
	switch l.method {
	case "pkexec":
		return append([]string{l.helper}, argv...)
	case "osascript":
		return []string{l.helper, "-e", AppleScript(argv)}
	}
	return argv
}

// Launch starts OpenVPN (which daemonises) and waits for the foreground
// part, which returns right after the configuration was parsed.
func (l *Launcher) Launch(ctx context.Context, s process.LaunchSpec) error {
	return l.runElevated(ctx, process.BuildArgs(s))
}

func (l *Launcher) runElevated(ctx context.Context, argv []string) error {
	if l.method != "none" && l.helper == "" {
		return ErrNoHelper
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	out, code, err := l.run(ctx, l.Wrap(argv))
	if err == nil {
		return nil
	}
	return l.classify(out, code, err)
}

func (l *Launcher) classify(out string, code int, err error) error {
	out = strings.TrimSpace(out)
	switch l.method {
	case "pkexec":
		switch code {
		case 126:
			return ErrCancelled
		case 127:
			return ErrNotAuthorized
		}
	case "osascript":
		if strings.Contains(out, "-128") || strings.Contains(strings.ToLower(out), "user canceled") {
			return ErrCancelled
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("timed out waiting for OpenVPN to start")
	}
	if out != "" {
		return fmt.Errorf("%w: %s", err, out)
	}
	return err
}

// Signal sends sig to p, elevating only when p belongs to another user.
func (l *Launcher) Signal(ctx context.Context, p process.Proc, sig string) error {
	if p.UID == os.Getuid() || l.euid == 0 {
		return process.Elevator{Method: "none", EUID: os.Geteuid()}.Signal(ctx, p, sig)
	}
	return l.runElevated(ctx, process.KillCommand(p.PID, sig))
}

func execRun(ctx context.Context, argv []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	return out.String(), code, err
}

// ShellQuote quotes s for POSIX sh.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// AppleScript builds the `do shell script … with administrator privileges`
// program that runs argv. Every argument is shell-quoted, then the whole
// command is escaped for an AppleScript string literal; output is
// discarded so the daemonised OpenVPN does not keep the pipe open.
func AppleScript(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = ShellQuote(a)
	}
	cmd := strings.Join(q, " ") + " >/dev/null 2>&1"
	cmd = strings.ReplaceAll(cmd, `\`, `\\`)
	cmd = strings.ReplaceAll(cmd, `"`, `\"`)
	return `do shell script "` + cmd + `" with administrator privileges`
}
