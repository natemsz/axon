//go:build !windows

package gui

import "errors"

// webview is Windows' own window for Axon (webview_windows.go); here
// there is none, and the window is an app-mode browser's.
type webview struct{}

const (
	cmdShow = iota
	cmdHide
	cmdMinimize
	cmdMaximize
	cmdDrag
	cmdDestroy
)

func newWebview(string, func()) (*webview, error) {
	return nil, errors.New("no WebView2 here")
}

func (*webview) do(uintptr)        {}
func (*webview) isVisible() bool   { return false }
func (*webview) isMaximized() bool { return false }
func webviewDLL() string           { return "" }
