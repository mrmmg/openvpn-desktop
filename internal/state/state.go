// Package state persists machine-written application state: the active
// profile and per-profile launch overrides. It is kept separate from the
// user-edited configuration and never stored inside .ovpn files.
package state

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/mrmmg/openvpn-desktop/internal/paths"
)

// Overrides are extra OpenVPN options applied when launching a profile.
// They never modify the profile file itself.
type Overrides struct {
	// RouteNoPull adds --route-nopull.
	RouteNoPull bool `yaml:"route_nopull,omitempty"`
	// IgnorePushedGateway adds pull-filters that drop a pushed
	// redirect-gateway and IPv6 default routes.
	IgnorePushedGateway bool `yaml:"ignore_pushed_gateway,omitempty"`
	// ForceFullTunnel adds --redirect-gateway def1 ipv6.
	ForceFullTunnel bool `yaml:"force_full_tunnel,omitempty"`
	// BlockIPv6 adds --block-ipv6 (drop IPv6 instead of leaking it when the
	// VPN provides no IPv6).
	BlockIPv6 bool `yaml:"block_ipv6,omitempty"`
}

// IsZero reports whether no override is set.
func (o Overrides) IsZero() bool { return o == Overrides{} }

// Data is the on-disk structure.
type Data struct {
	ActiveProfile string               `yaml:"active_profile,omitempty"`
	Overrides     map[string]Overrides `yaml:"overrides,omitempty"`
}

// Store is a concurrency-safe, file-backed state store.
type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

// Open loads the state from path. A missing file yields empty state.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read state %s: %w", path, err)
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &s.data); err != nil {
			// Corrupt state must not prevent the application from starting.
			return s, fmt.Errorf("state %s is corrupt and was ignored: %w", path, err)
		}
	}
	return s, nil
}

// Snapshot returns a copy of the current state.
func (s *Store) Snapshot() Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := Data{ActiveProfile: s.data.ActiveProfile, Overrides: map[string]Overrides{}}
	for k, v := range s.data.Overrides {
		cp.Overrides[k] = v
	}
	return cp
}

// ActiveProfile returns the persisted active profile name ("" if none).
func (s *Store) ActiveProfile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.ActiveProfile
}

// SetActiveProfile persists the active profile ("" clears it).
func (s *Store) SetActiveProfile(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ActiveProfile = name
	return s.saveLocked()
}

// Overrides returns the launch overrides for a profile.
func (s *Store) Overrides(profile string) Overrides {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Overrides[profile]
}

// SetOverrides persists the launch overrides for a profile.
func (s *Store) SetOverrides(profile string, o Overrides) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Overrides == nil {
		s.data.Overrides = map[string]Overrides{}
	}
	if o.IsZero() {
		delete(s.data.Overrides, profile)
	} else {
		s.data.Overrides[profile] = o
	}
	return s.saveLocked()
}

// ForgetProfile removes all state for a profile (after deletion/rename).
func (s *Store) ForgetProfile(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Overrides, profile)
	if s.data.ActiveProfile == profile {
		s.data.ActiveProfile = ""
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	raw, err := yaml.Marshal(s.data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return paths.WriteFileAtomic(s.path, raw, 0o600)
}
