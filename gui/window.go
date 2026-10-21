package gui

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// The settings window. On Windows it is Axon's own, holding WebView2
// (webview_windows.go). Otherwise, or where that cannot start, it is
// the page in a Chromium-family browser's app mode (Edge, else
// Chrome, Chromium or Brave)
// with a profile of Axon's own. That is a window of its own, with no
// address bar or tabs, run by a browser process Axon started and only
// Axon uses: closing it closes nothing of the player's own browsing,
// and Axon carries on in the tray. With no such browser installed the
// page opens in the default browser instead.

// browserCandidates are the app-mode browsers to look for, best first.
func browserCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			out = append(out,
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
				filepath.Join(base, "Chromium", "Application", "chrome.exe"))
		}
		return out
	case "darwin":
		home, _ := os.UserHomeDir()
		var out []string
		for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
			out = append(out,
				filepath.Join(root, "Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
				filepath.Join(root, "Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge"),
				filepath.Join(root, "Chromium.app", "Contents", "MacOS", "Chromium"),
				filepath.Join(root, "Brave Browser.app", "Contents", "MacOS", "Brave Browser"))
		}
		return out
	default:
		var out []string
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable", "brave-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				out = append(out, p)
			}
		}
		return out
	}
}

// findBrowser is the first app-mode browser installed, or "".
func findBrowser() string {
	for _, p := range browserCandidates() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// windowArgs are the browser's arguments for Axon's window.
func windowArgs(url, profile string) []string {
	return []string{
		"--app=" + url,
		"--user-data-dir=" + profile,
		"--window-size=1120,860",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-extensions",
		"--disable-background-mode",
		"--disable-features=Translate,msEdgeSidebarV2,msHubApps,msUndersideButton,msEdgeStartupBoost",
		"--class=evesynapse-axon",
	}
}

// profileDir is where the window's browser profile lives: Axon's own,
// apart from the player's.
func profileDir() string { return filepath.Join(profileBase(), "window") }

// profileBase is the folder for the window's browser data.
func profileBase() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, AppName)
}

type appWindow struct {
	url     string // the page, which signs the window in
	browser string // "" when there is none: the default browser is used
	profile string
	// fallback opens url in the default browser (OpenBrowser; tests
	// replace it).
	fallback func(string) error
	// closed is called once each time the player closes the window.
	closed func()

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	watching bool
	// wv is Axon's own window (Windows, with the WebView2 runtime),
	// made on first showing; useWV is false once that has failed.
	wv    *webview
	useWV bool
}

func newAppWindow(url string, fallback func(string) error, closed func()) *appWindow {
	if fallback == nil {
		fallback = OpenBrowser
	}
	return &appWindow{url: url, browser: findBrowser(), profile: profileDir(), fallback: fallback, closed: closed, useWV: webviewDLL() != ""}
}

// own reports whether Axon has a window of its own here, rather than
// a tab in the player's browser.
func (a *appWindow) own() bool { return a.useWV || a.browser != "" }

// frameless reports whether the window is Axon's own, with no title
// bar of the system's: the page draws the window's buttons.
func (a *appWindow) frameless() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wv != nil
}

// command does one of the page's window buttons: minimize, maximize
// (or restore), drag (the top bar), or close (to the tray).
func (a *appWindow) command(what string) bool {
	a.mu.Lock()
	wv := a.wv
	a.mu.Unlock()
	if wv == nil {
		return false
	}
	switch what {
	case "minimize":
		wv.do(cmdMinimize)
	case "maximize":
		wv.do(cmdMaximize)
	case "drag":
		wv.do(cmdDrag)
	case "close":
		wv.do(cmdHide)
	default:
		return false
	}
	return true
}

// showWebview shows Axon's own window, making it the first time. It
// reports false when there is none to be had (then the browser's).
func (a *appWindow) showWebview() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.useWV {
		return false
	}
	if a.wv != nil {
		a.wv.do(cmdShow)
		return true
	}
	wv, err := newWebview(a.url, a.closed)
	if err != nil {
		log.Printf("Axon's own window cannot open (%v): using a browser window", err)
		a.useWV = false
		return false
	}
	a.wv = wv
	return true
}

// The window is kept track of in one of two ways. On Windows, Edge
// hands its start over to a fresh process of its own, so the process
// Axon starts is gone at once: there the window itself is found, as
// the app window titled with Axon's name (a browser's own windows add
// the browser's name to the title). Elsewhere the browser Axon starts
// is the window's process, and is followed as such.

// alive is the window's browser process, while it runs (not used on
// Windows).
func (a *appWindow) alive() (*exec.Cmd, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == nil {
		return nil, false
	}
	select {
	case <-a.done:
		return nil, false
	default:
		return a.cmd, true
	}
}

// isOpen reports whether Axon's window is open.
func (a *appWindow) isOpen() bool {
	a.mu.Lock()
	wv := a.wv
	a.mu.Unlock()
	if wv != nil {
		return wv.isVisible()
	}
	if trackByTitle {
		return len(appWindows()) > 0
	}
	_, ok := a.alive()
	return ok
}

// show opens the window, or brings it to the front when it is open.
func (a *appWindow) show() error {
	if !a.own() {
		return a.fallback(a.url)
	}
	if a.showWebview() {
		return nil
	}
	if a.browser == "" {
		return a.fallback(a.url)
	}
	if trackByTitle {
		if wins := appWindows(); len(wins) > 0 {
			raiseWindows(wins)
			return nil
		}
	} else if cmd, ok := a.alive(); ok {
		// Its window cannot be raised from here: start afresh
		// rather than leave a second window beside the first.
		a.stop(cmd)
	}
	if err := os.MkdirAll(a.profile, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(a.browser, windowArgs(a.url, a.profile)...)
	if err := cmd.Start(); err != nil {
		a.browser = ""
		return a.fallback(a.url)
	}
	done := make(chan struct{})
	a.mu.Lock()
	a.cmd, a.done = cmd, done
	a.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		close(done)
		if !trackByTitle && a.closed != nil {
			a.closed()
		}
	}()
	if trackByTitle {
		go a.watchTitle()
	}
	return nil
}

// watchTitle waits for the window to appear, then for it to close,
// and says so; one watcher at a time.
func (a *appWindow) watchTitle() {
	a.mu.Lock()
	if a.watching {
		a.mu.Unlock()
		return
	}
	a.watching = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.watching = false
		a.mu.Unlock()
	}()
	for i := 0; len(appWindows()) == 0; i++ {
		if i == 80 { // 20 seconds: it never came
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	for len(appWindows()) > 0 {
		time.Sleep(time.Second)
	}
	if a.closed != nil {
		a.closed()
	}
}

// hide closes the window, if it is open, reporting whether it was;
// Axon itself keeps running.
func (a *appWindow) hide() bool {
	a.mu.Lock()
	wv := a.wv
	a.mu.Unlock()
	if wv != nil {
		open := wv.isVisible()
		wv.do(cmdHide)
		return open
	}
	if trackByTitle {
		wins := appWindows()
		if len(wins) == 0 {
			return false
		}
		closeWindows(wins)
		for i := 0; i < 20 && len(appWindows()) > 0; i++ {
			time.Sleep(150 * time.Millisecond)
		}
		return true
	}
	cmd, ok := a.alive()
	if !ok {
		return false
	}
	a.stop(cmd)
	return true
}

// shutdown closes the window for good, as Axon quits.
func (a *appWindow) shutdown() {
	a.mu.Lock()
	wv := a.wv
	a.mu.Unlock()
	if wv != nil {
		wv.do(cmdDestroy)
		return
	}
	a.hide()
}

// stop closes the window's browser, gently first.
func (a *appWindow) stop(cmd *exec.Cmd) {
	a.mu.Lock()
	done := a.done
	a.mu.Unlock()
	terminate(cmd.Process.Pid)
	select {
	case <-done:
		return
	case <-time.After(3 * time.Second):
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
