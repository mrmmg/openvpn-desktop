package credentials

import (
	"path/filepath"

	"github.com/mrmmg/openvpn-wails/internal/ovpn"
)

// Source says where OpenVPN will get username/password from.
type Source int

// Credential sources.
const (
	// NotRequired: the profile has no auth-user-pass.
	NotRequired Source = iota
	// SavedFile: the app passes --auth-user-pass <profile>.txt.
	SavedFile
	// ConfigFile: the profile names its own credential file.
	ConfigFile
	// Inline: the profile embeds <auth-user-pass>.
	Inline
	// Prompt: the app asks the user through the management interface.
	Prompt
)

func (s Source) String() string {
	switch s {
	case NotRequired:
		return "not required"
	case SavedFile:
		return "credential file"
	case ConfigFile:
		return "file named in profile"
	case Inline:
		return "inline in profile"
	case Prompt:
		return "asked when connecting"
	}
	return "?"
}

// Plan describes how credentials will be supplied for a launch.
type Plan struct {
	Source Source
	// Path is the credential file for SavedFile/ConfigFile.
	Path   string
	Status Status
	// PasswordPrompt is true when OpenVPN will ask for the password even
	// though a username is available (username-only file).
	PasswordPrompt bool
}

// NeedsManagementPrompt reports whether the launch must enable
// --management-query-passwords.
func (p Plan) NeedsManagementPrompt() bool {
	return p.Source == Prompt || p.PasswordPrompt
}

// MakePlan decides the credential source for a profile. The app's own
// credential file (<profile><ext>) takes precedence over a file named in
// the profile, which takes precedence over an interactive prompt.
func MakePlan(cfg *ovpn.Config, profilePath, credExt string) Plan {
	auth := cfg.Auth()
	if !auth.Required {
		return Plan{Source: NotRequired}
	}
	savedPath := PathFor(profilePath, credExt)
	if st := Inspect(savedPath); st.Exists {
		return Plan{Source: SavedFile, Path: savedPath, Status: st, PasswordPrompt: !st.HasPassword}
	}
	if auth.Inline {
		return Plan{Source: Inline}
	}
	if auth.File != "" {
		p := auth.File
		if !filepath.IsAbs(p) {
			p = filepath.Join(cfg.BaseDir(filepath.Dir(profilePath)), p)
		}
		st := Inspect(p)
		return Plan{Source: ConfigFile, Path: p, Status: st, PasswordPrompt: st.Exists && !st.HasPassword}
	}
	return Plan{Source: Prompt}
}
