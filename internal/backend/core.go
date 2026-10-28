package backend

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/appconfig"
	"github.com/mrmmg/openvpn-desktop/internal/applog"
	"github.com/mrmmg/openvpn-desktop/internal/ipcheck"
	"github.com/mrmmg/openvpn-desktop/internal/network"
	"github.com/mrmmg/openvpn-desktop/internal/paths"
	"github.com/mrmmg/openvpn-desktop/internal/privilege"
	"github.com/mrmmg/openvpn-desktop/internal/process"
	"github.com/mrmmg/openvpn-desktop/internal/state"
	"github.com/mrmmg/openvpn-desktop/internal/vpn"
)

// Version is set at build time (-ldflags "-X .../backend.Version=1.2.3").
var Version = "1.0.0"

// Emit delivers an event to the frontend.
type Emit func(name string, data any)

// Core holds the shared services. It does not start or stop OpenVPN.
type Core struct {
	Dirs     paths.Dirs
	Cfg      *appconfig.Store
	Lib      *Library
	Log      *slog.Logger
	Launcher *privilege.Launcher
	Hist     *state.History
	Net      network.Provider
	VPN      *vpn.Manager
	IP       ipcheck.Checker
	LogFile  string
	// Warnings are problems found at startup (shown once in the UI).
	Warnings []string

	// Notify shows a desktop notification (set by main; may be nil).
	Notify func(title, subtitle, body string)

	emit    Emit
	closers []io.Closer
}

// NewCore builds all services from the per-user directories.
func NewCore(dirs paths.Dirs, emit Emit) (*Core, error) {
	if emit == nil {
		emit = func(string, any) {}
	}
	c := &Core{Dirs: dirs, emit: emit}
	cfg, err := appconfig.Open(filepath.Join(dirs.Config, "config.yaml"), dirs)
	if err != nil {
		c.Warnings = append(c.Warnings, "Settings: "+err.Error())
	}
	c.Cfg = cfg
	c.Lib = NewLibrary(cfg)

	c.LogFile = filepath.Join(dirs.State, "app.log")
	logger, closer, lerr := applog.Open(c.LogFile, "info", 5)
	if lerr != nil {
		c.Warnings = append(c.Warnings, "Application log disabled: "+lerr.Error())
	}
	c.Log, c.closers = logger, append(c.closers, closer)

	if err := paths.EnsurePrivateDir(dirs.Runtime); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dirs.State, "logs"), 0o700); err != nil {
		return nil, err
	}
	c.Hist = state.NewHistory(filepath.Join(dirs.State, "history.jsonl"))
	c.Launcher = privilege.New()
	c.Net = network.NewDefault()
	c.IP = ipcheck.NewHTTPChecker(8*time.Second, Version)

	conf := cfg.Get()
	bin, _ := process.LocateBinary(conf.OpenVPNBinary)
	c.VPN = vpn.New(vpn.Options{
		Binary:         bin,
		Verbosity:      conf.LogVerbosity,
		LogDir:         filepath.Join(dirs.State, "logs"),
		RuntimeDir:     dirs.Runtime,
		CredExt:        credExt,
		ConnectTimeout: 60 * time.Second,
		StopTimeout:    15 * time.Second,
		AutoReconnect:  conf.AutoReconnect,
		RetryInterval:  time.Duration(conf.ReconnectInterval) * time.Second,
		MaxRetries:     conf.ReconnectAttempts,
	}, c.Launcher, process.DefaultTable(), c.Net, nil, c.Hist, c.Log)
	c.Log.Info("starting", "version", Version, "privilege", c.Launcher.Describe())
	return c, nil
}

// Close stops the managed connection (through the management socket, no
// privileges needed) and releases resources.
func (c *Core) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c.VPN.Close(ctx, true)
	for _, cl := range c.closers {
		if cl != nil {
			_ = cl.Close()
		}
	}
}
