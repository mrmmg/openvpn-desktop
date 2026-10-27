// Package appconfig is the small YAML file holding the desktop app's own
// settings and the display names of imported profiles.
package appconfig

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/mrmmg/openvpn-wails/internal/paths"
	"github.com/mrmmg/openvpn-wails/internal/process"
)

// Theme values.
const (
	ThemeLight  = "light"
	ThemeDark   = "dark"
	ThemeSystem = "system"
)

// Config is the on-disk document.
type Config struct {
	// ProfilesDir is where imported .ovpn files are stored.
	ProfilesDir string `yaml:"profiles_dir"`
	Theme       string `yaml:"theme"`
	// OpenVPNBinary is an absolute path; empty means auto-detect.
	OpenVPNBinary string `yaml:"openvpn_binary,omitempty"`
	// LogVerbosity is OpenVPN's --verb (0-9).
	LogVerbosity int `yaml:"log_verbosity"`
	// PublicIPCheck looks up the public address through the tunnel after
	// connecting (contacts third-party "what is my IP" services).
	PublicIPCheck bool `yaml:"public_ip_check"`
	// AutoReconnect restarts OpenVPN after an unexpected exit (OpenVPN itself
	// already re-establishes dropped sessions without restarting).
	AutoReconnect     bool `yaml:"auto_reconnect"`
	ReconnectAttempts int  `yaml:"reconnect_attempts"`
	// ReconnectInterval is the wait before each attempt, in seconds.
	ReconnectInterval int `yaml:"reconnect_interval_sec"`
	// WindowWidth/WindowHeight are the size of the window at start-up.
	WindowWidth  int `yaml:"window_width"`
	WindowHeight int `yaml:"window_height"`
	// IPServices are the endpoints queried for the public address.
	IPServices []IPService `yaml:"ip_services"`
	// Profiles holds per-profile metadata keyed by profile ID (the name of
	// its directory below ProfilesDir).
	Profiles map[string]ProfileMeta `yaml:"profiles,omitempty"`
}

// IPService is one "what is my IP" endpoint.
type IPService struct {
	Name    string `yaml:"name" json:"name"`
	URL     string `yaml:"url" json:"url"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
}

// Window size limits (the default is a tall, phone-like window).
const (
	DefaultWindowWidth  = 440
	DefaultWindowHeight = 820
	MinWindowWidth      = 360
	MinWindowHeight     = 480
	MaxWindowSize       = 4000
)

// MaxIPServices bounds the list a user can configure.
const MaxIPServices = 12

// DefaultIPServices are used on first run and by "reset to defaults".
func DefaultIPServices() []IPService {
	return []IPService{
		{Name: "ip.me", URL: "https://ip.me", Enabled: true},
		{Name: "ifconfig.me", URL: "https://ifconfig.me/ip", Enabled: true},
		{Name: "ipify", URL: "https://api.ipify.org", Enabled: true},
		{Name: "icanhazip", URL: "https://icanhazip.com", Enabled: true},
	}
}

// ValidateIPService checks one entry.
func ValidateIPService(s IPService) error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("every service needs a name")
	}
	u, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%s: the address must be a full http(s) URL, e.g. https://api.ipify.org", s.Name)
	}
	return nil
}

// ProfileMeta is what the app remembers about an imported profile.
type ProfileMeta struct {
	// Name is the display name the user can edit.
	Name string `yaml:"name"`
	// File is the original .ovpn file name at import time.
	File string `yaml:"file"`
	// ImportedAt is the import time (RFC 3339).
	ImportedAt string `yaml:"imported_at,omitempty"`
}

// Defaults returns the first-run configuration.
func Defaults(d paths.Dirs) Config {
	return Config{
		ProfilesDir:       filepath.Join(d.Config, "profiles"),
		Theme:             ThemeSystem,
		LogVerbosity:      3,
		PublicIPCheck:     true,
		IPServices:        DefaultIPServices(),
		AutoReconnect:     true,
		ReconnectAttempts: 3,
		ReconnectInterval: 5,
		WindowWidth:       DefaultWindowWidth,
		WindowHeight:      DefaultWindowHeight,
	}
}

// Validate checks values that came from the user or from disk.
func (c Config) Validate() error {
	switch c.Theme {
	case ThemeLight, ThemeDark, ThemeSystem:
	default:
		return fmt.Errorf("unknown theme %q", c.Theme)
	}
	if c.LogVerbosity < 0 || c.LogVerbosity > 9 {
		return fmt.Errorf("log verbosity must be between 0 and 9, got %d", c.LogVerbosity)
	}
	if c.ReconnectAttempts < 1 || c.ReconnectAttempts > 20 {
		return fmt.Errorf("reconnect attempts must be between 1 and 20, got %d", c.ReconnectAttempts)
	}
	if c.ReconnectInterval < 1 || c.ReconnectInterval > 300 {
		return fmt.Errorf("reconnect interval must be between 1 and 300 seconds, got %d", c.ReconnectInterval)
	}
	if c.WindowWidth < MinWindowWidth || c.WindowWidth > MaxWindowSize || c.WindowHeight < MinWindowHeight || c.WindowHeight > MaxWindowSize {
		return fmt.Errorf("window size must be between %dx%d and %dx%d", MinWindowWidth, MinWindowHeight, MaxWindowSize, MaxWindowSize)
	}
	if len(c.IPServices) > MaxIPServices {
		return fmt.Errorf("at most %d public IP services are supported", MaxIPServices)
	}
	seen := map[string]bool{}
	for _, svc := range c.IPServices {
		if err := ValidateIPService(svc); err != nil {
			return err
		}
		if seen[strings.ToLower(svc.Name)] {
			return fmt.Errorf("the service name %q is used twice", svc.Name)
		}
		seen[strings.ToLower(svc.Name)] = true
	}
	if !filepath.IsAbs(c.ProfilesDir) {
		return fmt.Errorf("profiles directory %q must be an absolute path", c.ProfilesDir)
	}
	if c.OpenVPNBinary != "" {
		if err := process.ValidateBinary(c.OpenVPNBinary); err != nil {
			return fmt.Errorf("OpenVPN executable: %w", err)
		}
	}
	return nil
}

// Store loads and saves a Config; it is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	cfg  Config
}

// Open reads path, creating it with defaults on first run. A damaged file
// is reported as an error rather than silently replaced.
func Open(path string, d paths.Dirs) (*Store, error) {
	s := &Store{path: path, cfg: Defaults(d)}
	s.cfg.Profiles = map[string]ProfileMeta{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, s.saveLocked()
	case err != nil:
		return s, err
	}
	loaded := Defaults(d)
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return s, fmt.Errorf("%s: %w (using defaults)", path, err)
	}
	if loaded.Profiles == nil {
		loaded.Profiles = map[string]ProfileMeta{}
	}
	if err := loaded.Validate(); err != nil {
		// Keep going with what is usable; Settings lets the user fix it.
		return s, fmt.Errorf("%s: %w (using defaults)", path, err)
	}
	s.cfg = loaded
	return s, nil
}

// Get returns a copy of the configuration.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyLocked()
}

func (s *Store) copyLocked() Config {
	c := s.cfg
	c.IPServices = append([]IPService(nil), s.cfg.IPServices...)
	c.Profiles = make(map[string]ProfileMeta, len(s.cfg.Profiles))
	for k, v := range s.cfg.Profiles {
		c.Profiles[k] = v
	}
	return c
}

// Update applies fn to a copy, validates and persists it.
func (s *Store) Update(fn func(*Config)) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyLocked()
	fn(&next)
	if err := next.Validate(); err != nil {
		return s.copyLocked(), err
	}
	prev := s.cfg
	s.cfg = next
	if err := s.saveLocked(); err != nil {
		s.cfg = prev
		return s.copyLocked(), err
	}
	return s.copyLocked(), nil
}

func (s *Store) saveLocked() error {
	data, err := yaml.Marshal(s.cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return paths.WriteFileAtomic(s.path, append([]byte("# OpenVPN Client settings\n"), data...), 0o600)
}

// Path returns the file location.
func (s *Store) Path() string { return s.path }
