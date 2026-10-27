// Package backend contains the desktop app's logic: the profile library,
// the connection controller, logs and settings. The Wails services in this
// package are thin wrappers; everything testable lives in plain types.
package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mrmmg/openvpn-wails/internal/appconfig"
	"github.com/mrmmg/openvpn-wails/internal/ovpn"
	"github.com/mrmmg/openvpn-wails/internal/profile"
)

const credExt = ".txt"

// Library manages the imported profiles. Every profile lives in its own
// directory below the profiles directory (<root>/<id>/<id>.ovpn), together
// with the certificates/keys it references. That keeps relative paths
// working and lets two profiles use files with the same name.
type Library struct {
	cfg *appconfig.Store
}

// NewLibrary returns a library backed by the settings store.
func NewLibrary(cfg *appconfig.Store) *Library { return &Library{cfg: cfg} }

// Root is the current profiles directory.
func (l *Library) Root() string { return l.cfg.Get().ProfilesDir }

// Entry is one profile with its metadata.
type Entry struct {
	ID      string
	Name    string
	File    string // original file name
	Profile profile.Profile
}

var slugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

func slugify(base string) string {
	s := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-._")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "profile"
	}
	return s
}

func (l *Library) store(id string) *profile.Store {
	return &profile.Store{Dir: filepath.Join(l.Root(), id), Extensions: []string{".ovpn"}, CredExt: credExt}
}

func fileName(id string) string { return id + ".ovpn" }

// isLoose reports whether id names a profile file placed directly in the
// profiles folder (e.g. copied there by hand) rather than a profile directory.
func isLoose(id string) bool {
	ext := strings.ToLower(filepath.Ext(id))
	return ext == ".ovpn" || ext == ".conf"
}

// locate returns the store and file name holding profile id. Profile
// directories normally contain <id>.ovpn; for hand-made directories the
// first .ovpn/.conf file inside is used.
func (l *Library) locate(id string) (*profile.Store, string, error) {
	if !validID(id) {
		return nil, "", fmt.Errorf("%w: %q", profile.ErrNotFound, id)
	}
	root := l.Root()
	if isLoose(id) {
		return &profile.Store{Dir: root, Extensions: []string{".ovpn", ".conf"}, CredExt: credExt}, id, nil
	}
	st := l.store(id)
	st.Extensions = []string{".ovpn", ".conf"}
	if _, err := os.Stat(filepath.Join(st.Dir, fileName(id))); err == nil {
		return st, fileName(id), nil
	}
	ents, err := os.ReadDir(st.Dir)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s", profile.ErrNotFound, id)
	}
	for _, e := range ents {
		if !e.IsDir() && isLoose(e.Name()) {
			return st, e.Name(), nil
		}
	}
	return nil, "", fmt.Errorf("%w: %s", profile.ErrNotFound, id)
}

// validID guards every path built from an ID.
func validID(id string) bool {
	return id != "" && id == filepath.Base(id) && !strings.HasPrefix(id, ".") && id != "." && id != ".."
}

// List returns all profiles, sorted by display name. A profile directory
// without its .ovpn file is skipped.
func (l *Library) List() ([]Entry, error) {
	root := l.Root()
	dirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profiles directory: %w", err)
	}
	var out []Entry
	for _, d := range dirs {
		if strings.HasPrefix(d.Name(), ".") || (!d.IsDir() && !isLoose(d.Name())) {
			continue
		}
		if e, err := l.Get(d.Name()); err == nil {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Entry) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// Get returns one profile.
func (l *Library) Get(id string) (Entry, error) {
	st, file, err := l.locate(id)
	if err != nil {
		return Entry{}, err
	}
	p, err := st.Get(file)
	if err != nil {
		return Entry{}, err
	}
	m := l.cfg.Get().Profiles[id]
	e := Entry{ID: id, Name: m.Name, File: m.File, Profile: p}
	if e.Name == "" {
		e.Name = strings.TrimSuffix(file, filepath.Ext(file))
	}
	if e.File == "" {
		e.File = file
	}
	return e, nil
}

// ImportResult describes a successful import.
type ImportResult struct {
	Entry   Entry
	Copied  []string
	Missing []string
}

// ErrNotProfile is returned for files that are not usable OpenVPN profiles.
var ErrNotProfile = errors.New("not a valid OpenVPN profile")

// CheckSource performs the cheap checks on a candidate file before it is
// copied anywhere: extension, size, text content and basic syntax.
func CheckSource(src string) (*ovpn.Config, error) {
	ext := strings.ToLower(filepath.Ext(src))
	if ext != ".ovpn" && ext != ".conf" {
		return nil, fmt.Errorf("%w: %q is not an .ovpn file", ErrNotProfile, filepath.Base(src))
	}
	fi, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(src), err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrNotProfile, filepath.Base(src))
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrNotProfile, filepath.Base(src))
	}
	if fi.Size() > ovpn.MaxFileSize {
		return nil, fmt.Errorf("%w: %s is larger than %d MB", ErrNotProfile, filepath.Base(src), ovpn.MaxFileSize>>20)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(src), err)
	}
	if slices.Contains(data[:min(len(data), 4096)], 0) {
		return nil, fmt.Errorf("%w: %s is a binary file, not a text configuration", ErrNotProfile, filepath.Base(src))
	}
	cfg, err := ovpn.ParseFile(src)
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", filepath.Base(src), err)
	}
	if len(cfg.Errors) > 0 {
		e := cfg.Errors[0]
		return nil, fmt.Errorf("%w: %s, %s", ErrNotProfile, filepath.Base(src), e.Error())
	}
	if len(cfg.Remotes()) == 0 {
		return nil, fmt.Errorf("%w: %s has no `remote` server, so it is not a client profile", ErrNotProfile, filepath.Base(src))
	}
	return cfg, nil
}

// Import copies src (and the relative files it references) into a new
// profile directory. The original is never modified.
func (l *Library) Import(src string) (ImportResult, error) {
	src, err := filepath.Abs(src)
	if err != nil {
		return ImportResult{}, err
	}
	if _, err := CheckSource(src); err != nil {
		return ImportResult{}, err
	}
	root := l.Root()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return ImportResult{}, fmt.Errorf("create profiles directory: %w", err)
	}
	base := slugify(filepath.Base(src))
	id := base
	for n := 2; ; n++ {
		if err := os.Mkdir(filepath.Join(root, id), 0o700); err == nil {
			break
		} else if !errors.Is(err, os.ErrExist) {
			return ImportResult{}, fmt.Errorf("create profile directory: %w", err)
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
	res, err := l.store(id).Import(src, fileName(id))
	if err != nil {
		_ = os.RemoveAll(filepath.Join(root, id))
		return ImportResult{}, err
	}
	display := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	_, err = l.cfg.Update(func(c *appconfig.Config) {
		c.Profiles[id] = appconfig.ProfileMeta{Name: display, File: filepath.Base(src), ImportedAt: time.Now().Format(time.RFC3339)}
	})
	if err != nil {
		_ = os.RemoveAll(filepath.Join(root, id))
		return ImportResult{}, fmt.Errorf("save settings: %w", err)
	}
	e, err := l.Get(id)
	return ImportResult{Entry: e, Copied: res.Copied, Missing: res.Missing}, err
}

// Rename changes the display name only; files are never touched.
func (l *Library) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("the name must not be empty")
	}
	if len([]rune(name)) > 80 {
		return errors.New("the name is too long (80 characters at most)")
	}
	e, err := l.Get(id)
	if err != nil {
		return err
	}
	_, err = l.cfg.Update(func(c *appconfig.Config) {
		m := c.Profiles[id]
		m.Name = name
		if m.File == "" {
			m.File = e.File
		}
		c.Profiles[id] = m
	})
	return err
}

// Remove deletes the profile directory (configuration, copied certificates
// and keys, saved credential file) and its metadata.
func (l *Library) Remove(id string) error {
	e, err := l.Get(id)
	if err != nil {
		return err
	}
	if isLoose(id) {
		// Only the file itself and its credential file; other files in the
		// folder may be shared with other profiles.
		if err := os.Remove(e.Profile.Path); err != nil {
			return fmt.Errorf("remove profile: %w", err)
		}
		_ = os.Remove(e.Profile.CredPath)
	} else {
		dir := filepath.Join(l.Root(), id)
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
			return fmt.Errorf("refusing to remove %s: not a profile directory", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove profile: %w", err)
		}
	}
	_, err = l.cfg.Update(func(c *appconfig.Config) { delete(c.Profiles, id) })
	return err
}

// IDForPath maps a profile file path (as reported by the connection
// manager) back to its ID.
func (l *Library) IDForPath(path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(l.Root(), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	id, _, _ := strings.Cut(rel, string(filepath.Separator))
	return id // a loose file's ID is its file name
}

// CopyAll copies every profile directory of the current root into newRoot
// (existing entries are left alone). Used when the user changes the
// profiles location and asks to bring their profiles along.
func (l *Library) CopyAll(newRoot string) (int, error) {
	if err := os.MkdirAll(newRoot, 0o700); err != nil {
		return 0, err
	}
	entries, err := l.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		dst := filepath.Join(newRoot, e.ID)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		src := filepath.Join(l.Root(), e.ID)
		if isLoose(e.ID) {
			if err := copyTree(src, dst); err == nil {
				_ = copyTree(e.Profile.CredPath, filepath.Join(newRoot, filepath.Base(e.Profile.CredPath)))
				n++
			}
			continue
		}
		if err := copyTree(src, dst); err != nil {
			return n, fmt.Errorf("copy %s: %w", e.Name, err)
		}
		n++
	}
	return n, nil
}

func copyTree(from, to string) error {
	if fi, err := os.Stat(from); err == nil && fi.Mode().IsRegular() {
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o600)
	}
	return filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		target := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case d.Type().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o600)
		}
		return nil // symlinks and special files are not copied
	})
}
