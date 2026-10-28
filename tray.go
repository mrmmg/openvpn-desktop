package main

import (
	_ "embed"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/mrmmg/openvpn-desktop/internal/backend"
)

//go:embed assets/tray-off.png
var trayOff []byte

//go:embed assets/tray-busy.png
var trayBusy []byte

//go:embed assets/tray-on.png
var trayOn []byte

// tray is the notification-area icon: green while connected, orange while
// connecting or disconnecting, grey otherwise, with Show, Disconnect and Quit.
type tray struct {
	app  *application.App
	win  *application.WebviewWindow
	icon *application.SystemTray
	ctrl *backend.Controller

	// quitting lets a real quit through the close-to-tray handler.
	quitting atomic.Bool

	mu   sync.Mutex
	last string
}

func newTray(app *application.App, win *application.WebviewWindow, ctrl *backend.Controller) *tray {
	t := &tray{app: app, win: win, ctrl: ctrl, icon: app.SystemTray.New()}
	t.icon.OnClick(t.show)
	t.update(backend.ConnectionState{State: "disconnected"})
	return t
}

func (t *tray) show() {
	t.win.Show()
	t.win.UnMinimise()
	t.win.Focus()
}

func (t *tray) quit() {
	t.quitting.Store(true)
	t.app.Quit()
}

// update reflects the connection state in the icon, tooltip and menu.
func (t *tray) update(s backend.ConnectionState) {
	icon, text, active := trayOff, "Disconnected", false
	switch s.State {
	case "connected":
		icon, text, active = trayOn, "Connected", true
	case "connecting":
		icon, text, active = trayBusy, "Connecting…", true
	case "disconnecting":
		icon, text = trayBusy, "Disconnecting…"
	}

	t.mu.Lock()
	key := text
	changed := key != t.last
	t.last = key
	t.mu.Unlock()
	if !changed {
		return
	}

	t.icon.SetIcon(icon)
	t.icon.SetTooltip("OpenVPN Desktop — " + text)

	// The menu is rebuilt rather than edited: every desktop's tray protocol
	// then sees a fresh, consistent menu.
	m := t.app.NewMenu()
	m.Add("Show OpenVPN").OnClick(func(*application.Context) { t.show() })
	m.Add("Disconnect").SetEnabled(active).OnClick(func(*application.Context) { go t.ctrl.Disconnect() })
	m.AddSeparator()
	m.Add("Quit").OnClick(func(*application.Context) { t.quit() })
	t.icon.SetMenu(m)
}
