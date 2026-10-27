package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mrmmg/openvpn-wails/internal/paths"
)

// LaunchSpec describes one OpenVPN start.
type LaunchSpec struct {
	Binary      string
	ProfilePath string
	Dir         string
	LogPath     string
	Socket      string
	Verbosity   int
	// CredFile is passed as --auth-user-pass when set.
	CredFile string
	// InteractiveAuth enables --auth-retry interact so wrong credentials can
	// be re-entered through the management interface.
	InteractiveAuth bool
	// ClientUser restricts the management socket to this user when OpenVPN
	// runs as root.
	ClientUser string
	ExtraArgs  []string
}

// BuildArgs returns the OpenVPN argv (without elevation prefix).
//
// Ordering matters: --log must precede --config because OpenVPN applies
// the first log option while parsing; everything that must override the
// profile comes after --config.
func BuildArgs(s LaunchSpec) []string {
	args := []string{
		s.Binary,
		"--log", s.LogPath,
		"--cd", s.Dir,
		"--config", s.ProfilePath,
		"--daemon", "openvpn-wails",
		"--verb", strconv.Itoa(s.Verbosity),
		"--management", s.Socket, "unix",
		"--management-query-passwords",
		"--management-log-cache", "2000",
		"--auth-nocache",
	}
	if s.ClientUser != "" {
		args = append(args, "--management-client-user", s.ClientUser)
	}
	if s.CredFile != "" {
		args = append(args, "--auth-user-pass", s.CredFile)
	}
	if s.InteractiveAuth {
		args = append(args, "--auth-retry", "interact")
	}
	return append(args, s.ExtraArgs...)
}

// Validate checks a spec before launching.
func (s LaunchSpec) Validate() error {
	for name, v := range map[string]string{"binary": s.Binary, "profile": s.ProfilePath, "log": s.LogPath, "socket": s.Socket, "dir": s.Dir} {
		if v == "" {
			return fmt.Errorf("launch: %s path is empty", name)
		}
		if !filepath.IsAbs(v) {
			return fmt.Errorf("launch: %s path %q must be absolute", name, v)
		}
	}
	return paths.CheckSocketPath(s.Socket)
}

// PrepareFiles rotates the previous log, pre-creates a fresh log file owned
// by the current user (mode 0600) and removes a stale socket. Root then only
// writes into files this user created in private directories.
func PrepareFiles(s LaunchSpec) error {
	if err := paths.EnsurePrivateDir(filepath.Dir(s.Socket)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.LogPath), 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	if fi, err := os.Lstat(s.LogPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("log file %s is a symlink; refusing to use it", s.LogPath)
		}
		if fi.Size() > 0 {
			_ = os.Rename(s.LogPath, s.LogPath+".1")
		} else {
			_ = os.Remove(s.LogPath)
		}
	}
	f, err := os.OpenFile(s.LogPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	_ = f.Close()
	if err := os.Remove(s.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

// Launch runs OpenVPN (which daemonises) and waits for the foreground part
// to exit. Errors before daemonising are written to the log file.
func (e Elevator) Launch(ctx context.Context, s LaunchSpec) error {
	argv := BuildArgs(s)
	if e.Effective() != "none" {
		_, err := e.RunPrivileged(ctx, argv)
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(out.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// InteractiveLaunchCmd returns an *exec.Cmd for launching through an
// interactive helper (pkexec, or sudo without -n after a failed
// non-interactive attempt). The caller hands the terminal to it.
func (e Elevator) InteractiveLaunchCmd(s LaunchSpec) *exec.Cmd {
	argv := BuildArgs(s)
	switch e.Effective() {
	case "pkexec":
		argv = append([]string{e.Pkexec}, argv...)
	case "sudo":
		argv = append([]string{e.Sudo, "--"}, argv...)
	}
	return exec.Command(argv[0], argv[1:]...)
}

// Instance is the persisted record of an OpenVPN process started by the app,
// used to re-attach after the app restarts.
type Instance struct {
	Profile     string    `json:"profile"`
	ProfilePath string    `json:"profile_path"`
	PID         int       `json:"pid"`
	StartTicks  uint64    `json:"start_ticks"`
	Socket      string    `json:"socket"`
	LogPath     string    `json:"log_path"`
	StartedAt   time.Time `json:"started_at"`
	Overrides   []string  `json:"override_args,omitempty"`
}

// SaveInstance writes the record atomically (0600).
func SaveInstance(path string, in Instance) error {
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	if err := paths.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	return paths.WriteFileAtomic(path, data, 0o600)
}

// LoadInstance reads a record; a missing file returns (nil, nil).
func LoadInstance(path string) (*Instance, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var in Instance
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("instance record %s is corrupt: %w", path, err)
	}
	return &in, nil
}

// RemoveInstance deletes the record.
func RemoveInstance(path string) {
	_ = os.Remove(path)
}
