package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// binaryCandidates are searched after $PATH. OpenVPN lives in sbin on most
// distributions and Homebrew prefixes, which GUI apps often lack in PATH.
var binaryCandidates = []string{
	"/usr/sbin/openvpn",
	"/usr/bin/openvpn",
	"/usr/local/sbin/openvpn",
	"/usr/local/bin/openvpn",
	"/opt/homebrew/sbin/openvpn",
	"/opt/homebrew/bin/openvpn",
	"/opt/local/sbin/openvpn",
}

// ValidateBinary checks that path is an absolute, executable regular file.
func ValidateBinary(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q is not an absolute path", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

// ErrBinaryNotFound means no OpenVPN executable could be located.
var ErrBinaryNotFound = errors.New("OpenVPN executable not found; install OpenVPN or set its path in Settings")

// LocateBinary returns custom when set (validated), otherwise the first
// OpenVPN executable found in $PATH or the well-known locations.
func LocateBinary(custom string) (string, error) {
	if custom != "" {
		if err := ValidateBinary(custom); err != nil {
			return "", err
		}
		return custom, nil
	}
	if p, err := exec.LookPath("openvpn"); err == nil {
		if abs, err := filepath.Abs(p); err == nil && ValidateBinary(abs) == nil {
			return abs, nil
		}
	}
	for _, p := range binaryCandidates {
		if ValidateBinary(p) == nil {
			return p, nil
		}
	}
	return "", ErrBinaryNotFound
}

// Version returns the first line of `openvpn --version`, or "".
func Version(ctx context.Context, binary string) string {
	out, _ := exec.CommandContext(ctx, binary, "--version").Output()
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}
