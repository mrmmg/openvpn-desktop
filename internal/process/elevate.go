package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrAuthRequired means the privilege helper needs interactive
// authentication (e.g. a sudo password) before the command can run.
var ErrAuthRequired = errors.New("authentication required to run OpenVPN as root")

// ErrCancelled means the user dismissed the authorization dialog.
var ErrCancelled = errors.New("authorization was cancelled")

// Elevator wraps commands that need root.
type Elevator struct {
	// Method: auto, sudo, pkexec, none.
	Method string
	Sudo   string
	Pkexec string
	// EUID is the effective uid (injectable for tests).
	EUID int
}

// Effective resolves "auto" to a concrete method.
func (e Elevator) Effective() string {
	if e.EUID == 0 {
		return "none"
	}
	switch e.Method {
	case "", "auto":
		return "sudo"
	}
	return e.Method
}

// Interactive reports whether every elevated command needs the terminal
// (pkexec authenticates per invocation through a polkit agent).
func (e Elevator) Interactive() bool { return e.Effective() == "pkexec" }

// Wrap prefixes argv with the elevation helper. sudo runs non-interactively
// (-n) so it can never block on a hidden password prompt.
func (e Elevator) Wrap(argv []string) []string {
	switch e.Effective() {
	case "sudo":
		return append([]string{e.Sudo, "-n", "--"}, argv...)
	case "pkexec":
		return append([]string{e.Pkexec}, argv...)
	default:
		return argv
	}
}

// AuthCommand returns the interactive command that caches credentials
// (sudo -v), or nil when the method has no such step.
func (e Elevator) AuthCommand() *exec.Cmd {
	if e.Effective() != "sudo" {
		return nil
	}
	return exec.Command(e.Sudo, "-v")
}

// Describe returns a short human description.
func (e Elevator) Describe() string {
	switch e.Effective() {
	case "none":
		if e.EUID == 0 {
			return "running as root"
		}
		return "no elevation (OpenVPN must be allowed to run as this user)"
	case "sudo":
		return "sudo (" + e.Sudo + ")"
	case "pkexec":
		return "pkexec (polkit)"
	}
	return e.Method
}

// IsAuthFailure reports whether stderr of a failed sudo invocation means a
// password is needed.
func IsAuthFailure(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "a password is required") ||
		strings.Contains(s, "a terminal is required") ||
		strings.Contains(s, "no askpass program") ||
		strings.Contains(s, "interactive authentication required")
}

// RunPrivileged runs argv with elevation non-interactively and returns
// ErrAuthRequired when authentication is needed.
func (e Elevator) RunPrivileged(ctx context.Context, argv []string) (string, error) {
	if e.Interactive() {
		return "", ErrAuthRequired
	}
	full := e.Wrap(argv)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Stdin = nil
	err := cmd.Run()
	if err != nil {
		if e.Effective() == "sudo" && IsAuthFailure(out.String()) {
			return out.String(), ErrAuthRequired
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), fmt.Errorf("%s exited with status %d", full[0], ee.ExitCode())
		}
		return out.String(), err
	}
	return out.String(), nil
}

// KillCommand returns argv that sends sig to pid.
func KillCommand(pid int, sig string) []string {
	kill := "/usr/bin/kill"
	if _, err := os.Stat(kill); err != nil {
		kill = "/bin/kill"
	}
	return []string{kill, "-s", sig, fmt.Sprint(pid)}
}

// Signal sends sig to pid, elevating when the process belongs to another
// user.
func (e Elevator) Signal(ctx context.Context, p Proc, sig string) error {
	if p.UID == os.Getuid() || e.EUID == 0 {
		proc, err := os.FindProcess(p.PID)
		if err != nil {
			return err
		}
		return proc.Signal(signalFromName(sig))
	}
	out, err := e.RunPrivileged(ctx, KillCommand(p.PID, sig))
	if err != nil && !errors.Is(err, ErrAuthRequired) && strings.TrimSpace(out) != "" {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
	}
	return err
}

// SystemctlCommand builds a systemctl invocation that never prompts.
func SystemctlCommand(action, unit string) []string {
	return []string{"systemctl", "--no-ask-password", action, unit}
}

// RunSystemctl controls a systemd unit; polkit may require interactive
// authentication, reported as ErrAuthRequired.
func RunSystemctl(ctx context.Context, action, unit string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	argv := SystemctlCommand(action, unit)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if IsAuthFailure(string(out)) || strings.Contains(strings.ToLower(string(out)), "access denied") {
			return ErrAuthRequired
		}
		return fmt.Errorf("systemctl %s %s: %s", action, unit, strings.TrimSpace(string(out)))
	}
	return nil
}
