//go:build !linux

package main

// trayAvailable: the menu bar is always there on macOS.
func trayAvailable() bool { return true }
