// Package credentials manages auth-user-pass credential files.
//
// Format (as read by OpenVPN's --auth-user-pass FILE):
//
//	line 1: username
//	line 2: password
//
// OpenVPN strips the trailing CR/LF of each line but keeps other whitespace,
// so values are stored verbatim. Passwords are never returned by this package
// except to the caller that explicitly asks for them, and never appear in
// errors.
package credentials

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mrmmg/openvpn-desktop/internal/paths"
)

// MaxFieldLen is OpenVPN's USER_PASS_LEN limit (including NUL).
const MaxFieldLen = 4095

// PathFor returns the credential file path for a profile path.
func PathFor(profilePath, ext string) string {
	return strings.TrimSuffix(profilePath, filepath.Ext(profilePath)) + ext
}

// Status describes a credential file without exposing its secret.
type Status struct {
	Path        string
	Exists      bool
	Mode        fs.FileMode
	OwnedByUser bool
	HasUsername bool
	HasPassword bool
	// Username is shown in the UI (never the password).
	Username string
	Err      error
}

// Insecure reports whether group/other can access the file.
func (s Status) Insecure() bool { return s.Exists && s.Mode.Perm()&0o077 != 0 }

// Complete reports whether both username and password are present.
func (s Status) Complete() bool { return s.Exists && s.HasUsername && s.HasPassword }

// Inspect reads the credential file metadata and the username.
func Inspect(path string) Status {
	st := Status{Path: path}
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return st
	}
	if err != nil {
		st.Err = err
		return st
	}
	st.Exists = true
	st.Mode = fi.Mode()
	if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
		st.OwnedByUser = int(sys.Uid) == os.Getuid()
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		st.Err = errors.New("credential file is a symlink")
		return st
	}
	user, pass, err := read(path)
	if err != nil {
		st.Err = err
		return st
	}
	st.Username = user
	st.HasUsername = user != ""
	st.HasPassword = pass != ""
	return st
}

// Read returns the username and password. Only call this when the password
// is about to be handed to OpenVPN.
func Read(path string) (username, password string, err error) {
	return read(path)
}

func read(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", sanitize(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 2*MaxFieldLen)
	var lines []string
	for sc.Scan() && len(lines) < 2 {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	if err := sc.Err(); err != nil {
		return "", "", fmt.Errorf("read credential file: %w", sanitize(err))
	}
	var u, p string
	if len(lines) > 0 {
		u = lines[0]
	}
	if len(lines) > 1 {
		p = lines[1]
	}
	return u, p, nil
}

// ValidateFields checks username/password for values OpenVPN cannot read
// from a file. The error never contains the values.
func ValidateFields(username, password string) error {
	if username == "" {
		return errors.New("username must not be empty")
	}
	if strings.ContainsAny(username, "\r\n") {
		return errors.New("username must not contain line breaks")
	}
	if strings.ContainsAny(password, "\r\n") {
		return errors.New("password must not contain line breaks")
	}
	if len(username) > MaxFieldLen || len(password) > MaxFieldLen {
		return fmt.Errorf("username and password must be shorter than %d bytes", MaxFieldLen)
	}
	return nil
}

// Write atomically creates or replaces the credential file with mode 0600.
// An empty password writes a username-only file (OpenVPN 2.6+ then asks for
// the password interactively).
func Write(path, username, password string) error {
	if err := ValidateFields(username, password); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to overwrite symlinked credential file")
	}
	content := username + "\n"
	if password != "" {
		content += password + "\n"
	}
	if err := paths.WriteFileAtomic(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write credential file: %w", sanitize(err))
	}
	return nil
}

// Delete removes the credential file; a missing file is not an error.
func Delete(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return sanitize(err)
	}
	return nil
}

// sanitize keeps only the operation and errno of filesystem errors so that no
// file content can leak through error messages.
func sanitize(err error) error {
	if err == nil {
		return nil
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return &fs.PathError{Op: pe.Op, Path: pe.Path, Err: pe.Err}
	}
	return err
}
