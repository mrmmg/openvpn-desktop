// Package paths resolves the XDG base directories used by openvpn-desktop and
// expands user-supplied paths.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AppName is the directory name used below every XDG base directory.
const AppName = "openvpn-desktop"

// Dirs holds the resolved per-user directories.
type Dirs struct {
	Home    string
	Config  string // ~/.config/openvpn-desktop
	State   string // ~/.local/state/openvpn-desktop
	Runtime string // $XDG_RUNTIME_DIR/openvpn-desktop (or /tmp/openvpn-desktop-<uid>)
}

// Env abstracts environment lookups for testing.
type Env func(string) string

// Resolve computes the XDG directories from the environment.
func Resolve(getenv Env) (Dirs, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Dirs{}, fmt.Errorf("cannot determine home directory: %w", err)
		}
		home = h
	}
	xdg := func(key, fallback string) string {
		if v := getenv(key); v != "" && filepath.IsAbs(v) {
			return v
		}
		return fallback
	}
	cfgBase, stateBase := filepath.Join(home, ".config"), filepath.Join(home, ".local", "state")
	if runtime.GOOS == "darwin" {
		// The platform convention for per-user application data.
		cfgBase = filepath.Join(home, "Library", "Application Support")
		stateBase = cfgBase
	}
	d := Dirs{
		Home:   home,
		Config: filepath.Join(xdg("XDG_CONFIG_HOME", cfgBase), AppName),
		State:  filepath.Join(xdg("XDG_STATE_HOME", stateBase), AppName),
	}
	if rt := getenv("XDG_RUNTIME_DIR"); rt != "" && filepath.IsAbs(rt) {
		d.Runtime = filepath.Join(rt, AppName)
	} else {
		// os.TempDir is per-user on macOS ($TMPDIR) and /tmp on Linux.
		d.Runtime = filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", AppName, os.Getuid()))
	}
	// The management socket lives in the runtime directory and must fit
	// into a unix socket address.
	if len(filepath.Join(d.Runtime, "mgmt.sock")) > MaxSocketPath {
		d.Runtime = filepath.Join("/tmp", fmt.Sprintf("%s-%d", AppName, os.Getuid()))
	}
	return d, nil
}

// Expand replaces a leading "~" with home and expands $VARS.
func Expand(p, home string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	return filepath.Clean(os.ExpandEnv(p))
}

// EnsurePrivateDir creates dir (and parents) with mode 0700 and verifies that
// an existing directory is owned by the current user and not accessible by
// others. It refuses symlinks to avoid being redirected by another user.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("directory %s is a symlink; refusing to use it", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("tighten permissions of %s: %w", dir, err)
		}
	}
	return nil
}

// WriteFileAtomic writes data to path via a temporary file in the same
// directory followed by rename, so readers never observe partial content.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ErrSocketPathTooLong is returned when a unix socket path would be truncated.
var ErrSocketPathTooLong = errors.New("unix socket path too long")

// MaxSocketPath is a conservative limit below sun_path (108 bytes on Linux).
// OpenVPN silently truncates longer paths.
const MaxSocketPath = 100

// CheckSocketPath verifies that p fits into a sockaddr_un.
func CheckSocketPath(p string) error {
	if len(p) > MaxSocketPath {
		return fmt.Errorf("%w (%d > %d bytes): %s", ErrSocketPathTooLong, len(p), MaxSocketPath, p)
	}
	return nil
}
