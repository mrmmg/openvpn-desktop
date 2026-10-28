package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mrmmg/openvpn-desktop/internal/appconfig"
	"github.com/mrmmg/openvpn-desktop/internal/credentials"
	"github.com/mrmmg/openvpn-desktop/internal/ovpn"
	"github.com/mrmmg/openvpn-desktop/internal/process"
)

// ---------------------------------------------------------------------------
// ProfilesService

// ProfilesService is bound to the frontend.
type ProfilesService struct {
	core *Core
	ctrl *Controller
	logs *LogBuffer
}

// NewProfilesService creates the service.
func NewProfilesService(core *Core, ctrl *Controller, logs *LogBuffer) *ProfilesService {
	return &ProfilesService{core: core, ctrl: ctrl, logs: logs}
}

func (s *ProfilesService) toProfile(e Entry, last map[string]int64) Profile {
	p := Profile{ID: e.ID, Name: e.Name, File: e.File, Auth: "none", Risks: []string{}, LastConnected: last[e.Profile.Name]}
	cfg, err := ovpn.ParseFile(e.Profile.Path)
	if err != nil {
		p.Problem = err.Error()
		return p
	}
	if r := cfg.Remotes(); len(r) > 0 {
		p.Server = fmt.Sprintf("%s:%d", r[0].Host, r[0].Port)
		p.Protocol = strings.ToUpper(r[0].Proto)
	}
	switch plan := credentials.MakePlan(cfg, e.Profile.Path, credExt); plan.Source {
	case credentials.Prompt:
		p.Auth = "prompt"
	case credentials.SavedFile, credentials.ConfigFile:
		p.Auth = "file"
		if plan.PasswordPrompt {
			p.Auth = "prompt"
		}
	}
	p.Risks = append(p.Risks, scriptRisks(cfg)...)
	if len(cfg.Errors) > 0 {
		p.Problem = cfg.Errors[0].Error()
	}
	return p
}

// List returns all profiles.
func (s *ProfilesService) List() ([]Profile, error) {
	entries, err := s.core.Lib.List()
	if err != nil {
		return nil, err
	}
	last := s.ctrl.lastConnected()
	out := make([]Profile, 0, len(entries))
	for _, e := range entries {
		out = append(out, s.toProfile(e, last))
	}
	return out, nil
}

// Details returns the read-only configuration summary of a profile.
func (s *ProfilesService) Details(id string) (*ProfileDetails, error) {
	e, err := s.core.Lib.Get(id)
	if err != nil {
		return nil, err
	}
	cfg, err := ovpn.ParseFile(e.Profile.Path)
	if err != nil {
		return nil, err
	}
	d := &ProfileDetails{Profile: s.toProfile(e, s.ctrl.lastConnected()), Directory: filepath.Dir(e.Profile.Path),
		Remotes: []string{}, Files: []string{}, Missing: []string{}, Scripts: []string{}, Routing: []string{}}
	for _, r := range cfg.Remotes() {
		d.Remotes = append(d.Remotes, fmt.Sprintf("%s:%d (%s)", r.Host, r.Port, strings.ToUpper(r.Proto)))
	}
	name, typ := cfg.Device()
	d.Device = strings.TrimSpace(typ + " " + name)
	for _, ref := range cfg.FileRefs(filepath.Dir(e.Profile.Path)) {
		if ref.Inline {
			d.Files = append(d.Files, ref.Directive+" (embedded)")
			continue
		}
		if _, err := os.Stat(ref.Resolved); err != nil && !ref.Optional {
			d.Missing = append(d.Missing, ref.Directive+" "+ref.Path)
			continue
		}
		d.Files = append(d.Files, ref.Directive+" "+ref.Path)
	}
	for _, sc := range cfg.Scripts() {
		d.Scripts = append(d.Scripts, sc.Directive+" "+sc.Command)
	}
	for _, name := range []string{"redirect-gateway", "route-nopull", "pull-filter", "block-outside-dns"} {
		for _, dir := range cfg.All(name) {
			d.Routing = append(d.Routing, dir.String())
		}
	}
	return d, nil
}

func (s *ProfilesService) changed() { s.core.emit(EventProfiles, nil) }

// Import imports the profile at path (file picker or drag and drop).
func (s *ProfilesService) Import(path string) ImportOutcome {
	out := ImportOutcome{File: filepath.Base(path)}
	res, err := s.core.Lib.Import(path)
	if err != nil {
		s.core.Log.Warn("import failed", "file", path, "err", err)
		out.Result = fail(CodeInvalid, importError(err))
		return out
	}
	out.Result = ok()
	p := s.toProfile(res.Entry, nil)
	out.Profile = &p
	for _, m := range res.Missing {
		out.Notes = append(out.Notes, "Referenced file not found next to the profile and not copied: "+m)
	}
	if len(p.Risks) > 0 {
		out.Notes = append(out.Notes, "This profile runs external commands when connecting; you will be asked to confirm.")
	}
	s.logs.Add("app", "info", "", "Imported profile "+res.Entry.Name)
	s.core.Log.Info("profile imported", "id", res.Entry.ID)
	s.changed()
	return out
}

func importError(err error) string {
	if errors.Is(err, ErrNotProfile) {
		return strings.TrimPrefix(err.Error(), ErrNotProfile.Error()+": ")
	}
	return err.Error()
}

// Browse opens the platform file picker and imports the chosen files.
func (s *ProfilesService) Browse() []ImportOutcome {
	paths, err := application.Get().Dialog.OpenFile().
		SetTitle("Import OpenVPN profile").
		AddFilter("OpenVPN profiles (*.ovpn, *.conf)", "*.ovpn;*.conf").
		CanChooseFiles(true).
		PromptForMultipleSelection()
	if err != nil || len(paths) == 0 {
		return nil
	}
	return s.importAll(paths)
}

func (s *ProfilesService) importAll(paths []string) []ImportOutcome {
	var out []ImportOutcome
	for _, p := range paths {
		out = append(out, s.Import(p))
	}
	return out
}

// HandleDrop imports dropped files and tells the UI the outcome.
func (s *ProfilesService) HandleDrop(paths []string) {
	s.core.emit(EventImport, s.importAll(paths))
}

// Rename changes the display name; the imported file is untouched.
func (s *ProfilesService) Rename(id, name string) Result {
	if err := s.core.Lib.Rename(id, name); err != nil {
		return failErr(CodeError, err)
	}
	s.changed()
	return ok()
}

// Remove deletes a profile. The active profile cannot be removed.
func (s *ProfilesService) Remove(id string) Result {
	if st := s.ctrl.State(); st.ActiveID == id && st.State != "disconnected" {
		return fail(CodeBusy, "Disconnect this profile before removing it.")
	}
	if err := s.core.Lib.Remove(id); err != nil {
		return failErr(CodeError, err)
	}
	s.logs.Add("app", "info", "", "Removed profile "+id)
	s.changed()
	return ok()
}

// RevealFolder opens the profile's folder in the system file manager.
func (s *ProfilesService) RevealFolder(id string) Result {
	e, err := s.core.Lib.Get(id)
	if err != nil {
		return failErr(CodeNotFound, err)
	}
	return openPath(filepath.Dir(e.Profile.Path))
}

func openPath(p string) Result {
	if err := application.Get().Browser.OpenFile(p); err != nil {
		return failErr(CodeError, err)
	}
	return ok()
}

// ---------------------------------------------------------------------------
// ConnectionService

// ConnectionService is bound to the frontend.
type ConnectionService struct{ ctrl *Controller }

// NewConnectionService creates the service.
func NewConnectionService(c *Controller) *ConnectionService { return &ConnectionService{ctrl: c} }

// State returns the current connection state.
func (s *ConnectionService) State() ConnectionState { return s.ctrl.State() }

// Traffic returns the recent traffic samples.
func (s *ConnectionService) Traffic() []TrafficSample { return s.ctrl.Traffic() }

// Connect starts a profile; acknowledgeScripts confirms a profile that
// runs external commands; askCredentials ignores a saved credential file
// and prompts for username/password instead.
func (s *ConnectionService) Connect(id string, acknowledgeScripts, askCredentials bool) Result {
	return s.ctrl.Connect(id, acknowledgeScripts, askCredentials)
}

// Disconnect stops the active connection.
func (s *ConnectionService) Disconnect() Result { return s.ctrl.Disconnect() }

// ForceDisconnect kills OpenVPN after a clean stop failed.
func (s *ConnectionService) ForceDisconnect() Result { return s.ctrl.ForceDisconnect() }

// SubmitCredentials answers OpenVPN's credential prompt.
func (s *ConnectionService) SubmitCredentials(username, password string) Result {
	return s.ctrl.SubmitCredentials(username, password)
}

// CancelCredentials aborts the attempt that is waiting for credentials.
func (s *ConnectionService) CancelCredentials() Result { return s.ctrl.CancelCredentials() }

// ---------------------------------------------------------------------------
// LogsService

// LogsService is bound to the frontend.
type LogsService struct {
	core *Core
	buf  *LogBuffer
}

// NewLogsService creates the service.
func NewLogsService(core *Core, buf *LogBuffer) *LogsService {
	return &LogsService{core: core, buf: buf}
}

// Entries returns the buffered log lines.
func (s *LogsService) Entries() []LogEntry { return s.buf.Snapshot() }

// Clear empties the visible log (files on disk are kept).
func (s *LogsService) Clear() { s.buf.Clear() }

// Export asks for a destination and saves the visible log there.
func (s *LogsService) Export() Result {
	path, err := application.Get().Dialog.SaveFile().
		SetFilename("openvpn-client-" + time.Now().Format("20060102-150405") + ".log").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return fail(CodeCancelled, "")
	}
	var b strings.Builder
	for _, e := range s.buf.Snapshot() {
		fmt.Fprintf(&b, "%s [%s] %s: %s\n", e.Time, strings.ToUpper(e.Level), e.Source, e.Message)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return failErr(CodeError, err)
	}
	return ok()
}

// OpenLogFolder shows the folder holding the saved OpenVPN logs.
func (s *LogsService) OpenLogFolder() Result {
	return openPath(filepath.Join(s.core.Dirs.State, "logs"))
}

// ---------------------------------------------------------------------------
// SettingsService

// SettingsService is bound to the frontend.
type SettingsService struct {
	core *Core
	ctrl *Controller
	logs *LogBuffer
}

// NewSettingsService creates the service.
func NewSettingsService(core *Core, ctrl *Controller, logs *LogBuffer) *SettingsService {
	return &SettingsService{core: core, ctrl: ctrl, logs: logs}
}

// Get returns the settings.
func (s *SettingsService) Get() Settings {
	c := s.core.Cfg.Get()
	detected, _ := process.LocateBinary("")
	return Settings{
		Theme: c.Theme, LogVerbosity: c.LogVerbosity, OpenVPNBinary: c.OpenVPNBinary,
		DetectedBinary: detected, ProfilesDir: c.ProfilesDir, PublicIPCheck: c.PublicIPCheck, IPServices: toDTOServices(c.IPServices), AutoReconnect: c.AutoReconnect, CheckUpdates: c.CheckUpdates, CloseToTray: c.CloseToTray, TrayAvailable: TrayAvailable != nil && TrayAvailable(), ReconnectAttempts: c.ReconnectAttempts, ReconnectInterval: c.ReconnectInterval, WindowWidth: c.WindowWidth, WindowHeight: c.WindowHeight,
		ConfigFile: s.core.Cfg.Path(), LogFile: s.core.LogFile, Privilege: s.core.Launcher.Describe(), Version: Version,
	}
}

// Startup returns warnings collected while starting (once).
func (s *SettingsService) Startup() []string { return s.core.Warnings }

func (s *SettingsService) update(fn func(*appconfig.Config)) Result {
	if _, err := s.core.Cfg.Update(fn); err != nil {
		return failErr(CodeError, err)
	}
	return ok()
}

// SetTheme stores the theme (light, dark or system).
func (s *SettingsService) SetTheme(theme string) Result {
	return s.update(func(c *appconfig.Config) { c.Theme = theme })
}

// SetLogVerbosity sets OpenVPN's --verb for future connections.
func (s *SettingsService) SetLogVerbosity(v int) Result {
	return s.update(func(c *appconfig.Config) { c.LogVerbosity = v })
}

// SetPublicIPCheck enables or disables the public address lookup.
func (s *SettingsService) SetPublicIPCheck(on bool) Result {
	return s.update(func(c *appconfig.Config) { c.PublicIPCheck = on })
}

func toDTOServices(in []appconfig.IPService) []IPService {
	out := make([]IPService, 0, len(in))
	for _, s := range in {
		out = append(out, IPService{Name: s.Name, URL: s.URL, Enabled: s.Enabled})
	}
	return out
}

// SetIPServices replaces the list of public-IP lookup services.
func (s *SettingsService) SetIPServices(list []IPService) Result {
	next := make([]appconfig.IPService, 0, len(list))
	for _, e := range list {
		next = append(next, appconfig.IPService{Name: strings.TrimSpace(e.Name), URL: strings.TrimSpace(e.URL), Enabled: e.Enabled})
	}
	return s.update(func(c *appconfig.Config) { c.IPServices = next })
}

// ResetIPServices restores the default services.
func (s *SettingsService) ResetIPServices() Result {
	return s.update(func(c *appconfig.Config) { c.IPServices = appconfig.DefaultIPServices() })
}

// SetAutoReconnect stores the auto-reconnect policy (applies to the next connection).
func (s *SettingsService) SetAutoReconnect(enabled bool, attempts, intervalSec int) Result {
	return s.update(func(c *appconfig.Config) {
		c.AutoReconnect, c.ReconnectAttempts, c.ReconnectInterval = enabled, attempts, intervalSec
	})
}

// TrayAvailable reports whether the desktop has a tray to hide into; set by main.
var TrayAvailable func() bool

// SetCloseToTray chooses whether closing the window hides it to the tray.
func (s *SettingsService) SetCloseToTray(on bool) Result {
	return s.update(func(c *appconfig.Config) { c.CloseToTray = on })
}

// SetCheckUpdates enables or disables the check for a newer release at start-up.
func (s *SettingsService) SetCheckUpdates(on bool) Result {
	return s.update(func(c *appconfig.Config) { c.CheckUpdates = on })
}

// CheckForUpdate looks for a newer release on GitHub.
func (s *SettingsService) CheckForUpdate() UpdateInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return checkUpdate(ctx, updateClient(), releasesAPI, Version)
}

// OpenURL opens an https link in the default browser.
func (s *SettingsService) OpenURL(url string) Result {
	if !strings.HasPrefix(url, "https://") {
		return fail(CodeError, "only https links can be opened")
	}
	if err := application.Get().Browser.OpenURL(url); err != nil {
		return failErr(CodeError, err)
	}
	return ok()
}

// SetWindowSize stores the start-up window size and applies it right away.
func (s *SettingsService) SetWindowSize(width, height int) Result {
	if r := s.update(func(c *appconfig.Config) { c.WindowWidth, c.WindowHeight = width, height }); !r.OK {
		return r
	}
	if w := application.Get().Window.Current(); w != nil {
		w.SetSize(width, height)
	}
	return ok()
}

// UseCurrentWindowSize stores the window's present size as the start-up size.
func (s *SettingsService) UseCurrentWindowSize() Result {
	w := application.Get().Window.Current()
	if w == nil {
		return fail(CodeError, "No window available.")
	}
	width, height := w.Size()
	return s.update(func(c *appconfig.Config) { c.WindowWidth, c.WindowHeight = width, height })
}

// SetOpenVPNBinary validates and stores the executable path ("" = auto).
func (s *SettingsService) SetOpenVPNBinary(path string) Result {
	path = strings.TrimSpace(path)
	return s.update(func(c *appconfig.Config) { c.OpenVPNBinary = path })
}

// BrowseOpenVPNBinary lets the user pick the executable.
func (s *SettingsService) BrowseOpenVPNBinary() string {
	p, err := application.Get().Dialog.OpenFile().SetTitle("Select the OpenVPN executable").CanChooseFiles(true).PromptForSingleSelection()
	if err != nil {
		return ""
	}
	return p
}

// BrowseProfilesDir lets the user pick a folder.
func (s *SettingsService) BrowseProfilesDir() string {
	p, err := application.Get().Dialog.OpenFile().SetTitle("Select the profiles folder").
		CanChooseFiles(false).CanChooseDirectories(true).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil {
		return ""
	}
	return p
}

// SetProfilesDir changes where profiles are stored. With copyExisting the
// current profiles are copied to the new place first. It is refused while a
// connection is active because OpenVPN reads files from the old location.
func (s *SettingsService) SetProfilesDir(dir string, copyExisting bool) Result {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) {
		return fail(CodeError, "Choose an absolute folder path.")
	}
	if st := s.core.VPN.Status(); st.Active() {
		return fail(CodeBusy, "Disconnect before changing the profiles folder.")
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		return fail(CodeError, dir+" is not a folder.")
	}
	if dir == filepath.Clean(s.core.Lib.Root()) {
		return ok()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(CodeError, "Cannot use this folder: "+err.Error())
	}
	probe, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return fail(CodeError, "This folder is not writable: "+err.Error())
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	if copyExisting {
		if _, err := s.core.Lib.CopyAll(dir); err != nil {
			return failErr(CodeError, err)
		}
	}
	if r := s.update(func(c *appconfig.Config) { c.ProfilesDir = dir }); !r.OK {
		return r
	}
	s.core.emit(EventProfiles, nil)
	return ok()
}

// OpenProfilesDir opens the profiles folder in the system file manager.
func (s *SettingsService) OpenProfilesDir() Result {
	dir := s.core.Lib.Root()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return failErr(CodeError, err)
	}
	return openPath(dir)
}

// OpenConfigFile reveals the settings file's folder.
func (s *SettingsService) OpenConfigFolder() Result { return openPath(filepath.Dir(s.core.Cfg.Path())) }

// About returns application information.
func (s *SettingsService) About() AboutInfo {
	ver := "not found"
	if bin, err := process.LocateBinary(s.core.Cfg.Get().OpenVPNBinary); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if v := process.Version(ctx, bin); v != "" {
			ver = v
		}
	}
	return AboutInfo{
		Name: "OpenVPN Client", Version: Version, OpenVPN: ver,
		Platform: runtime.GOOS + "/" + runtime.GOARCH, License: "MIT",
		ConfigDir: filepath.Dir(s.core.Cfg.Path()),
		Notices:   thirdParty,
	}
}

var thirdParty = slices.Clone([]string{
	"Wails v3 (MIT) — https://wails.io",
	"Vue 3 (MIT) — https://vuejs.org",
	"Tailwind CSS (MIT) — https://tailwindcss.com",
	"uPlot (MIT) — https://github.com/leeoniya/uPlot",
	"vishvananda/netlink (Apache-2.0)",
	"go.yaml.in/yaml (Apache-2.0 / MIT)",
	"This application controls the system's OpenVPN; OpenVPN is a trademark of OpenVPN Inc.",
})
