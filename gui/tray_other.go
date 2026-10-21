//go:build !windows

package gui

import "context"

// Only Windows has a tray icon: a Mac's menu bar and Linux's trays
// need Cocoa or D-Bus, which the standard library does not reach.
const traySupported = false

func runTray(_ context.Context, _ trayActions, ready chan<- struct{}) { close(ready) }

func trayNote(string) {}

func showConsole(bool) bool { return false }
