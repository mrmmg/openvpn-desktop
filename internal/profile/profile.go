// Package profile manages OpenVPN profile files in the configured directory:
// discovery, import, creation and deletion.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/credentials"
	"github.com/mrmmg/openvpn-desktop/internal/ovpn"
	"github.com/mrmmg/openvpn-desktop/internal/paths"
)

// Profile is one OpenVPN configuration file.
type Profile struct {
	// Name is the file name, e.g. "office.ovpn". It is the profile's ID.
	Name     string
	Path     string
	Dir      string
	Size     int64
	ModTime  time.Time
	CredPath string
}

// BaseName returns the name without extension ("office").
func (p Profile) BaseName() string { return strings.TrimSuffix(p.Name, filepath.Ext(p.Name)) }

// Store provides access to the profile directory.
type Store struct {
	Dir        string
	Extensions []string
	CredExt    string
}

// ErrNotFound is returned for unknown profiles.
var ErrNotFound = errors.New("profile not found")

// ErrExists is returned when a target file already exists.
var ErrExists = errors.New("profile already exists")

func (s *Store) isProfileName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range s.Extensions {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}

// List returns all profiles sorted by name. A missing directory yields an
// empty list and an error wrapping fs.ErrNotExist.
func (s *Store) List() ([]Profile, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, fmt.Errorf("read profile directory %s: %w", s.Dir, err)
	}
	var out []Profile
	for _, e := range entries {
		if !s.isProfileName(e.Name()) {
			continue
		}
		p, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Profile) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// Get returns a single profile by file name.
func (s *Store) Get(name string) (Profile, error) {
	if err := ValidateName(name); err != nil {
		return Profile{}, err
	}
	path := filepath.Join(s.Dir, name)
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return Profile{}, err
	}
	if !fi.Mode().IsRegular() {
		return Profile{}, fmt.Errorf("%s is not a regular file", name)
	}
	return Profile{
		Name:     name,
		Path:     path,
		Dir:      s.Dir,
		Size:     fi.Size(),
		ModTime:  fi.ModTime(),
		CredPath: credentials.PathFor(path, s.CredExt),
	}, nil
}

// ValidateName rejects names that could escape the profile directory.
func ValidateName(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return errors.New("profile name must not be empty")
	case strings.ContainsAny(name, "/\x00"):
		return fmt.Errorf("profile name %q must not contain '/'", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("profile name %q must not start with '.'", name)
	case len(name) > 200:
		return errors.New("profile name is too long")
	}
	return nil
}

// NormalizeName adds the default extension if the name has none of the
// recognised extensions.
func (s *Store) NormalizeName(name string) string {
	name = strings.TrimSpace(name)
	if !s.isProfileName(name) && len(s.Extensions) > 0 {
		name += s.Extensions[0]
	}
	return name
}

// ImportResult describes what an import copied.
type ImportResult struct {
	Profile Profile
	// Copied lists referenced files copied alongside the profile.
	Copied []string
	// Skipped lists referenced files that already existed with identical content.
	Skipped []string
	// Missing lists referenced relative files that were not found next to the source.
	Missing []string
}

// Import copies src into the store under name (src's base name if empty).
// Referenced files given as relative paths (ca, cert, key, ...) are copied as
// well so the profile keeps working. Existing files are never overwritten.
func (s *Store) Import(src, name string) (ImportResult, error) {
	var res ImportResult
	src = filepath.Clean(src)
	fi, err := os.Stat(src)
	if err != nil {
		return res, fmt.Errorf("cannot read %s: %w", src, err)
	}
	if !fi.Mode().IsRegular() {
		return res, fmt.Errorf("%s is not a regular file", src)
	}
	if fi.Size() > ovpn.MaxFileSize {
		return res, fmt.Errorf("%s is too large to be an OpenVPN profile", src)
	}
	if name == "" {
		name = filepath.Base(src)
	}
	name = s.NormalizeName(name)
	if err := ValidateName(name); err != nil {
		return res, err
	}
	dst := filepath.Join(s.Dir, name)
	if abs, _ := filepath.Abs(src); abs == dst {
		return res, errors.New("the file is already in the profile directory")
	}
	if _, err := os.Stat(dst); err == nil {
		return res, fmt.Errorf("%w: %s", ErrExists, name)
	}
	cfg, err := ovpn.ParseFile(src)
	if err != nil {
		return res, fmt.Errorf("cannot parse %s: %w", src, err)
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return res, fmt.Errorf("create profile directory: %w", err)
	}

	// Plan referenced files first so we fail before writing anything.
	type copyJob struct{ from, to, rel string }
	var jobs []copyJob
	srcDir := filepath.Dir(src)
	for _, ref := range cfg.FileRefs(srcDir) {
		if ref.Inline || filepath.IsAbs(ref.Path) || ref.Directive == "auth-user-pass" {
			continue
		}
		if strings.Contains(ref.Path, "..") {
			res.Missing = append(res.Missing, ref.Path+" (outside the source directory, not copied)")
			continue
		}
		from := ref.Resolved
		to := filepath.Join(s.Dir, ref.Path)
		if _, err := os.Stat(from); err != nil {
			res.Missing = append(res.Missing, ref.Path)
			continue
		}
		if existing, err := os.ReadFile(to); err == nil {
			incoming, _ := os.ReadFile(from)
			if bytes.Equal(existing, incoming) {
				res.Skipped = append(res.Skipped, ref.Path)
				continue
			}
			return res, fmt.Errorf("referenced file %s already exists in %s with different content; import aborted", ref.Path, s.Dir)
		}
		jobs = append(jobs, copyJob{from, to, ref.Path})
	}

	if err := copyFile(src, dst, 0o600); err != nil {
		return res, err
	}
	for _, j := range jobs {
		if err := os.MkdirAll(filepath.Dir(j.to), 0o700); err != nil {
			return res, err
		}
		if err := copyFile(j.from, j.to, 0o600); err != nil {
			return res, fmt.Errorf("copy %s: %w", j.rel, err)
		}
		res.Copied = append(res.Copied, j.rel)
	}
	// Bring an adjacent credential file along, keeping it private.
	srcCred := credentials.PathFor(src, s.CredExt)
	dstCred := credentials.PathFor(dst, s.CredExt)
	if _, err := os.Stat(srcCred); err == nil {
		if _, err := os.Stat(dstCred); errors.Is(err, fs.ErrNotExist) {
			if err := copyFile(srcCred, dstCred, 0o600); err == nil {
				res.Copied = append(res.Copied, filepath.Base(dstCred))
			}
		}
	}
	res.Profile, err = s.Get(name)
	return res, err
}

// Template is the skeleton used for new profiles.
const Template = `# OpenVPN client profile created by openvpn-desktop.
client
dev tun
proto udp
remote vpn.example.com 1194
resolv-retry infinite
nobind
persist-key
persist-tun
remote-cert-tls server
verb 3

# Credentials: the app passes --auth-user-pass <profile>.txt when that file exists.
auth-user-pass

<ca>
# paste the CA certificate here
</ca>
`

// Create writes a new profile with the given content (Template if empty).
func (s *Store) Create(name, content string) (Profile, error) {
	name = s.NormalizeName(name)
	if err := ValidateName(name); err != nil {
		return Profile{}, err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Profile{}, fmt.Errorf("create profile directory: %w", err)
	}
	if content == "" {
		content = Template
	}
	path := filepath.Join(s.Dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return Profile{}, fmt.Errorf("%w: %s", ErrExists, name)
	}
	if err != nil {
		return Profile{}, err
	}
	if _, err := io.WriteString(f, content); err != nil {
		_ = f.Close()
		return Profile{}, err
	}
	if err := f.Close(); err != nil {
		return Profile{}, err
	}
	return s.Get(name)
}

// Delete removes a profile and, if withCredentials, its credential file.
// Referenced certificate files are left alone because other profiles may
// share them.
func (s *Store) Delete(name string, withCredentials bool) error {
	p, err := s.Get(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p.Path); err != nil {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	if withCredentials {
		if err := credentials.Delete(p.CredPath); err != nil {
			return fmt.Errorf("profile deleted, but its credential file could not be removed: %w", err)
		}
	}
	return nil
}

func copyFile(from, to string, perm os.FileMode) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(to)
		return err
	}
	return f.Close()
}

// EnsureDir creates the profile directory when it lives below the
// application's own config directory. System directories are never created.
func (s *Store) EnsureDir(appConfigDir string) error {
	if _, err := os.Stat(s.Dir); err == nil {
		return nil
	}
	rel, err := filepath.Rel(appConfigDir, s.Dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	return paths.EnsurePrivateDir(s.Dir)
}
