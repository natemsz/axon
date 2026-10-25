//go:build !windows

package gui

// The overlay window is Windows' own (overlay_windows.go), as the
// tray is: an always-on-top native window is Win32 work. Here there
// is none; a paste goes on the site's own channel page instead.
const overlaySupported = false

func runOverlay(_ overlayActions, ready chan<- struct{}) { close(ready) }

func showOverlayWindow() {}
