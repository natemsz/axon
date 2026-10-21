//go:build !windows

package gui

import (
	"os"
	"syscall"
)

// trackByTitle: here the window's browser process is followed (see
// window.go).
const trackByTitle = false

func appWindows() []uintptr  { return nil }
func raiseWindows([]uintptr) {}
func closeWindows([]uintptr) {}

// terminate asks the window's browser to quit.
func terminate(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
}
