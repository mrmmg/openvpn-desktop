package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/mrmmg/openvpn-desktop/internal/backend"
	"github.com/mrmmg/openvpn-desktop/internal/paths"
)

// The built frontend is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

// appIcon is shown in desktop notifications and as the window icon.
//
//go:embed assets/icon.png
var appIcon []byte

func init() {
	// Registering the events gives the binding generator typed listeners.
	application.RegisterEvent[backend.ConnectionState](backend.EventState)
	application.RegisterEvent[backend.TrafficSample](backend.EventTraffic)
	application.RegisterEvent[backend.Notice](backend.EventNotice)
	application.RegisterEvent[[]backend.LogEntry](backend.EventLogs)
	application.RegisterEvent[application.Void](backend.EventLogsClr)
	application.RegisterEvent[application.Void](backend.EventProfiles)
	application.RegisterEvent[[]backend.ImportOutcome](backend.EventImport)
}

func main() {
	dirs, err := paths.Resolve(nil)
	if err != nil {
		log.Fatal(err)
	}

	var app *application.App
	var tr *tray
	emit := func(name string, data any) {
		if app == nil {
			return
		}
		if st, ok := data.(backend.ConnectionState); ok && name == backend.EventState && tr != nil {
			tr.update(st)
		}
		if data == nil {
			app.Event.Emit(name) // void events must carry no data
			return
		}
		app.Event.Emit(name, data)
	}
	core, err := backend.NewCore(dirs, emit)
	if err != nil {
		log.Fatal(err)
	}
	logBuf := backend.NewLogBuffer(emit)
	ctrl := backend.NewController(core)
	follower := backend.NewLogFollower(core, logBuf)
	profiles := backend.NewProfilesService(core, ctrl, logBuf)

	// Native desktop notifications (D-Bus on Linux, Notification Center on macOS).
	notifier := notifications.New()
	iconPath := filepath.Join(dirs.State, "icon.png")
	if err := os.WriteFile(iconPath, appIcon, 0o600); err != nil {
		iconPath = ""
	}
	// Notifications are sent one at a time, in order, from a single worker.
	// Each gets its own ID: re-using one makes the desktop replace the
	// previous notification silently instead of showing a new popup.
	type note struct{ title, subtitle, body string }
	notes := make(chan note, 16)
	var noteSeq atomic.Uint64
	go func() {
		for n := range notes {
			opts := notifications.NotificationOptions{
				ID:       fmt.Sprintf("vpn-state-%d", noteSeq.Add(1)),
				Title:    "OpenVPN · " + n.title,
				Subtitle: n.subtitle,
				Body:     n.body,
				ThreadID: "vpn-state",
			}
			if iconPath != "" {
				opts.Attachments = []notifications.NotificationAttachment{{ID: "icon", Path: iconPath, Type: "appLogoOverride"}}
			}
			if err := notifier.SendNotification(opts); err != nil {
				core.Log.Warn("notification failed", "title", n.title, "err", err)
			} else {
				core.Log.Info("notification sent", "title", n.title)
			}
		}
	}()
	core.Notify = func(title, subtitle, body string) {
		select {
		case notes <- note{title, subtitle, body}:
		default:
			core.Log.Warn("notification dropped: queue full", "title", title)
		}
	}

	app = application.New(application.Options{
		Name:        "OpenVPN Client",
		Icon:        appIcon,
		Description: "A desktop client for OpenVPN",
		Services: []application.Service{
			application.NewService(notifier),
			application.NewService(profiles),
			application.NewService(backend.NewConnectionService(ctrl)),
			application.NewService(backend.NewLogsService(core, logBuf)),
			application.NewService(backend.NewSettingsService(core, ctrl, logBuf)),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		// The window's app id, matched with openvpn-desktop.desktop for its icon.
		Linux: application.LinuxOptions{ProgramName: "openvpn-desktop"},
		Mac:   application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		// Stop the managed connection and background loops on exit, so no
		// OpenVPN process or goroutine outlives the app.
		OnShutdown: func() {
			follower.Shutdown()
			ctrl.Shutdown()
			core.Close()
		},
	})

	conf := core.Cfg.Get()
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "OpenVPN Client",
		Width:            conf.WindowWidth,
		Height:           conf.WindowHeight,
		MinWidth:         360,
		MinHeight:        480,
		EnableFileDrop:   true,
		BackgroundColour: application.NewRGB(255, 255, 255),
		URL:              "/",
	})
	win.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		if files := e.Context().DroppedFiles(); len(files) > 0 {
			go profiles.HandleDrop(files)
		}
	})

	tr = newTray(app, win, ctrl)
	// Closing the window keeps the app (and the VPN) running in the tray, when
	// that is switched on and the desktop has a tray to bring it back from.
	backend.TrayAvailable = trayAvailable
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if tr.quitting.Load() || !core.Cfg.Get().CloseToTray || !trayAvailable() {
			return
		}
		e.Cancel()
		win.Hide()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
