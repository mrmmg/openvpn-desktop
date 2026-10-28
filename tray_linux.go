package main

import (
	"github.com/godbus/dbus/v5"
)

// trayAvailable reports whether a notification-area host is running. Without
// one (GNOME without the AppIndicator extension, for example) the tray icon is
// invisible, so closing the window must quit instead of hiding it.
func trayAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	obj := conn.Object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher")
	v, err := obj.GetProperty("org.kde.StatusNotifierWatcher.IsStatusNotifierHostRegistered")
	if err != nil {
		return false
	}
	registered, _ := v.Value().(bool)
	return registered
}
