//go:build !windows

package gui

// The overlay's window is Windows' for now (overlay_windows.go); the
// Mac and Linux windows follow. Here there is none, and a paste goes
// on the site's own channel page instead.
const overlaySupported = false

func runOverlay(_ overlayDesk, ready chan<- struct{}) { close(ready) }

func showOverlayWindow()   {}
func hideOverlayWindow()   {}
func releaseOverlayFocus() {}
func dragOverlayWindow()   {}
func syncShortcuts()       {}

func clipboardText() (string, bool) { return "", false }

func showSitePanel(string) bool { return false }

func showIntelWindow(module, url string, toggle bool) bool { return false }
func intelWindowDo(module, cmd string)                     {}
func intelWindowSize(module string, width, height int)     {}
func intelOpenModules() []string                           { return nil }
func hideIntelWindow(module string)                        {}
func startEveWatch()                                       {}
