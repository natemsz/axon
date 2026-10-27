package gui

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The overlay's window on Windows: a WebView2 window of its own
// (webview_windows.go, with overlay set), frameless, always on top
// and out of the taskbar, its page drawn see-through over the
// system's acrylic backdrop (Windows 11; a dark panel elsewhere).
// It stays on screen once called up, so a second send is one
// shortcut and one Enter: the shortcut takes the keyboard, a send
// gives it back to whatever had it (the game), and the bar stays
// where the player put it.
//
// The shortcuts are registered with this window (RegisterHotKey), on
// its thread, from the config as it stands; a save re-registers
// them. A hotkey only tells Axon a chord was pressed: nothing else
// about the keyboard is ever seen, and the clipboard is read only in
// clipboardText, when the player sends with the field empty.

var (
	pRegisterHotKey      = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	pGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	pIsWindow            = user32.NewProc("IsWindow")
	pSystemParamsInfo    = user32.NewProc("SystemParametersInfoW")
	pOpenClipboard       = user32.NewProc("OpenClipboard")
	pCloseClipboard      = user32.NewProc("CloseClipboard")
	pGetClipboardData    = user32.NewProc("GetClipboardData")
	pDwmSetWindowAttr    = dwmapi.NewProc("DwmSetWindowAttribute")
	pDwmBlurBehind       = dwmapi.NewProc("DwmEnableBlurBehindWindow")
	pCreateRectRgn       = syscall.NewLazyDLL("gdi32.dll").NewProc("CreateRectRgn")
	pGlobalLock          = kernel32.NewProc("GlobalLock")
	pGlobalUnlock        = kernel32.NewProc("GlobalUnlock")
	pGlobalSize          = kernel32.NewProc("GlobalSize")
)

const overlaySupported = true

const (
	wmHotkey = 0x0312

	// The overlay window's own commands, beside webview's.
	cmdRelease  = 100
	cmdSyncKeys = 101

	modNoRepeat   = 0x4000
	cfUnicodeText = 13

	wsPopup        = 0x80000000
	wsClipChildren = 0x02000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080

	swShow       = 5
	hwndTopmost  = ^uintptr(0) // HWND_TOPMOST
	swpShowWin   = 0x0040
	spiWorkArea  = 0x0030
	dwmDarkMode     = 20 // DWMWA_USE_IMMERSIVE_DARK_MODE
	dwmCorners      = 33 // DWMWA_WINDOW_CORNER_PREFERENCE
	dwmRound        = 2  // DWMWCP_ROUND
	dwmBBEnable     = 0x1
	dwmBBBlurRegion = 0x2

	// The bar's size at 96 DPI: a three-line field, its chips and
	// a status line (page/overlay.css is laid out for it).
	ovW, ovH = 560, 176
	// The site panel's size at 96 DPI: room for the workbench.
	panelW, panelH = 1180, 780
)

// theOverlay is the overlay's window; its procedure is a callback
// with no receiver, so it reads this.
var theOverlay *webview

// ovState is what the overlay's thread keeps: the server's desk, the
// hotkeys registered by id, and the window that had the keyboard
// before a shortcut took it.
var ovState struct {
	mu    sync.Mutex
	desk  overlayDesk
	bound map[uintptr]shortcutAction
	prev  uintptr
}

var overlayViewProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	if w := theOverlay; w != nil {
		if r, ok := w.handleOverlay(hwnd, uint32(msg), wparam, lparam); ok {
			return r
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
})

// runOverlay makes the overlay's window (hidden) and registers the
// shortcuts with it. ready is closed once it stands, or could not be
// made (no WebView2 runtime): Axon runs on without it.
func runOverlay(d overlayDesk, ready chan<- struct{}) {
	defer close(ready)
	ovState.mu.Lock()
	ovState.desk = d
	ovState.mu.Unlock()
	dll := webviewDLL()
	if dll == "" {
		d.note("overlay: the WebView2 runtime is not installed, so the overlay and its shortcuts are off")
		return
	}
	w := &webview{url: d.url, overlay: true, ready: make(chan error, 1)}
	theOverlay = w
	go w.loop(dll)
	select {
	case err := <-w.ready:
		if err != nil {
			theOverlay = nil
			d.note("overlay: could not be made: %v", err)
		}
	case <-time.After(20 * time.Second):
		d.note("overlay: WebView2 did not start; the overlay is off")
	}
	// The site panel: the workbench, map and feed shortcuts open the
	// site's own pages in it, on top of the game.
	p := &webview{url: "about:blank", panel: true, ready: make(chan error, 1)}
	thePanel = p
	go p.loop(dll)
	select {
	case err := <-p.ready:
		if err != nil {
			thePanel = nil
			d.note("overlay: the site panel could not be made: %v", err)
		}
	case <-time.After(20 * time.Second):
		thePanel = nil
		d.note("overlay: the site panel did not start")
	}
}

// thePanel is the site panel: the intel workbench, the map or the
// feed, in a window of Axon's own that stays on top of the game.
// The site is signed into once there, like any browser.
var thePanel *webview

var panelViewProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	if w := thePanel; w != nil {
		if r, ok := w.handlePanel(hwnd, uint32(msg), wparam, lparam); ok {
			return r
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
})

// makePanelWindow makes the panel: a framed, resizable window, on
// top and out of the taskbar, centred on the main screen.
func (w *webview) makePanelWindow(inst uintptr) error {
	name, _ := syscall.UTF16PtrFromString("AxonPanel")
	title, _ := syscall.UTF16PtrFromString(AppName)
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	brush, _, _ := pCreateSolidBrush.Call(0x00060b14)
	wc := wndClassEx{WndProc: panelViewProc, Instance: inst, ClassName: name, Cursor: cursor, Background: brush}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	var work rect
	pSystemParamsInfo.Call(spiWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0)
	width, height := min(w.scale(panelW), work.R-work.L), min(w.scale(panelH), work.B-work.T)
	x, y := (work.L+work.R-width)/2, (work.T+work.B-height)/2
	w.hwnd, _, _ = pCreateWindowExW.Call(wsExTopmost|wsExToolWindow, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsClipChildren, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, inst, 0)
	if w.hwnd == 0 {
		return syscall.GetLastError()
	}
	v := uint32(1)
	pDwmSetWindowAttr.Call(w.hwnd, dwmDarkMode, uintptr(unsafe.Pointer(&v)), 4)
	return nil
}

// handlePanel is the panel's procedure: the system draws its frame.
func (w *webview) handlePanel(hwnd uintptr, msg uint32, wparam, lparam uintptr) (uintptr, bool) {
	switch msg {
	case wmSize:
		w.resize()
		if w.ctrl != 0 {
			comCall(w.ctrl, 23) // NotifyParentWindowPositionChanged
		}
		return 0, true
	case wmDPIChanged:
		r := (*rect)(sysPtr(&lparam))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
		return 0, true
	case wmActivate:
		if wparam&0xffff != 0 && w.ctrl != 0 {
			comCall(w.ctrl, 12, 0) // MoveFocus(PROGRAMMATIC)
		}
		return 0, false
	case wmClose:
		w.panelHide()
		return 0, true
	case wmWindowCmd:
		switch wparam {
		case cmdShow:
			w.panelShow()
		case cmdHide:
			w.panelHide()
		}
		return 0, true
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0, true
	}
	return 0, false
}

// panelShow loads the page asked for, when it is another, and brings
// the panel up on top with the keyboard.
func (w *webview) panelShow() {
	w.mu.Lock()
	next := w.next
	w.next = ""
	w.mu.Unlock()
	if next != "" && next != w.url && w.core != 0 {
		u, _ := syscall.UTF16PtrFromString(next)
		comCall(w.core, 5, uintptr(unsafe.Pointer(u))) // Navigate
		w.url = next
	}
	pShowWindow.Call(w.hwnd, swShow)
	pSetWindowPos.Call(w.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWin)
	pSetForegroundWindow.Call(w.hwnd)
	if w.ctrl != 0 {
		comCall(w.ctrl, 4, 1)  // put_IsVisible
		comCall(w.ctrl, 12, 0) // MoveFocus(PROGRAMMATIC)
	}
	w.mu.Lock()
	w.visible = true
	w.mu.Unlock()
}

func (w *webview) panelHide() {
	if w.ctrl != 0 {
		comCall(w.ctrl, 4, 0)
	}
	pShowWindow.Call(w.hwnd, swHideWin)
	w.mu.Lock()
	w.visible = false
	w.mu.Unlock()
}

// showSitePanel opens url in the panel; the same shortcut again,
// with that page showing, puts the panel away. false when there is
// no panel here.
func showSitePanel(url string) bool {
	w := thePanel
	if w == nil || w.hwnd == 0 {
		return false
	}
	w.mu.Lock()
	open := w.visible && w.url == url
	if !open {
		w.next = url
	}
	w.mu.Unlock()
	if open {
		pPostMessageW.Call(w.hwnd, wmWindowCmd, cmdHide, 0)
	} else {
		pPostMessageW.Call(w.hwnd, wmWindowCmd, cmdShow, 0)
	}
	return true
}

// makeOverlayWindow makes the bar, at the bottom middle of the main
// screen's work area, and gives it the system's see-through look.
func (w *webview) makeOverlayWindow(inst uintptr) error {
	name, _ := syscall.UTF16PtrFromString("AxonOverlay")
	title, _ := syscall.UTF16PtrFromString(AppName + ": send intel")
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	wc := wndClassEx{WndProc: overlayViewProc, Instance: inst, ClassName: name, Cursor: cursor}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	width, height := w.scale(ovW), w.scale(ovH)
	var work rect
	pSystemParamsInfo.Call(spiWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0)
	x := (work.L + work.R - width) / 2
	y := work.B - height - w.scale(120)
	w.hwnd, _, _ = pCreateWindowExW.Call(wsExTopmost|wsExToolWindow, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
		wsPopup|wsClipChildren, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, inst, 0)
	if w.hwnd == 0 {
		return syscall.GetLastError()
	}
	// See-through: blur-behind over an empty region makes DWM
	// composite the window by its pixels' own alpha, with no blur,
	// so the page's half-clear panel shows the game through it (the
	// way Tauri makes a transparent window). The system backdrops
	// (acrylic, mica) are drawn only for framed windows, and this
	// one has no frame: over them it read as solid.
	rgn, _, _ := pCreateRectRgn.Call(0, 0, ^uintptr(0), ^uintptr(0))
	bb := dwmBlurBehind{Flags: dwmBBEnable | dwmBBBlurRegion, Enable: 1, Region: rgn}
	pDwmBlurBehind.Call(w.hwnd, uintptr(unsafe.Pointer(&bb)))
	for _, a := range [][2]uint32{{dwmDarkMode, 1}, {dwmCorners, dwmRound}} {
		v := a[1]
		pDwmSetWindowAttr.Call(w.hwnd, uintptr(a[0]), uintptr(unsafe.Pointer(&v)), 4)
	}
	syncHotkeys(w.hwnd)
	return nil
}

// dwmBlurBehind is DWM_BLURBEHIND.
type dwmBlurBehind struct {
	Flags      uint32
	Enable     int32
	Region     uintptr
	Transition int32
}

// handleOverlay is the overlay window's procedure.
func (w *webview) handleOverlay(hwnd uintptr, msg uint32, wparam, lparam uintptr) (uintptr, bool) {
	switch msg {
	case wmNCCalcSize:
		if wparam == 0 {
			return 0, false
		}
		return 0, true // no frame: the page is the whole window
	case wmNCHitTest:
		return htClient, true // the page says where it drags from
	case wmSize:
		w.resize()
		if w.ctrl != 0 {
			comCall(w.ctrl, 23) // NotifyParentWindowPositionChanged
		}
		return 0, true
	case wmDPIChanged:
		r := (*rect)(sysPtr(&lparam))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
		return 0, true
	case wmClose:
		w.overlayHide()
		return 0, true
	case wmActivate:
		// Activated however (the shortcut, a click on the bar's
		// edge): the keyboard goes into the page, not the host.
		if wparam&0xffff != 0 && w.ctrl != 0 {
			comCall(w.ctrl, 12, 0) // MoveFocus(PROGRAMMATIC)
		}
		return 0, false
	case wmHotkey:
		ovState.mu.Lock()
		a, d := ovState.bound[wparam], ovState.desk
		ovState.mu.Unlock()
		if a == actSubmit {
			// Shown here, on the hotkey's own message: Windows lets
			// the program a hotkey went to take the foreground.
			w.overlayShow()
		} else if a != 0 && d.onShortcut != nil {
			go d.onShortcut(a)
		}
		return 0, true
	case wmWindowCmd:
		switch wparam {
		case cmdShow:
			w.overlayShow()
		case cmdHide:
			w.overlayHide()
		case cmdRelease:
			releaseFocus(hwnd)
		case cmdDrag:
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
		case cmdSyncKeys:
			syncHotkeys(hwnd)
		}
		return 0, true
	case wmDestroy:
		unregisterHotkeys(hwnd)
		pPostQuitMessage.Call(0)
		return 0, true
	}
	return 0, false
}

// overlayShow brings the bar up on top with the keyboard in its
// field, remembering what had the keyboard so a send can give it
// back.
func (w *webview) overlayShow() {
	if fg, _, _ := pGetForegroundWindow.Call(); fg != 0 && fg != w.hwnd {
		ovState.mu.Lock()
		ovState.prev = fg
		ovState.mu.Unlock()
	}
	pShowWindow.Call(w.hwnd, swShow)
	pSetWindowPos.Call(w.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWin)
	pSetForegroundWindow.Call(w.hwnd)
	if w.ctrl != 0 {
		comCall(w.ctrl, 4, 1)  // put_IsVisible
		comCall(w.ctrl, 12, 0) // MoveFocus(PROGRAMMATIC): the page's field takes it
	}
	w.mu.Lock()
	w.visible = true
	w.mu.Unlock()
}

// overlayHide puts the bar away, the keyboard going back first.
func (w *webview) overlayHide() {
	releaseFocus(w.hwnd)
	if w.ctrl != 0 {
		comCall(w.ctrl, 4, 0)
	}
	pShowWindow.Call(w.hwnd, swHideWin)
	w.mu.Lock()
	w.visible = false
	w.mu.Unlock()
}

// releaseFocus hands the keyboard back to the window that had it
// before the overlay took it, when that window is still there.
func releaseFocus(self uintptr) {
	ovState.mu.Lock()
	prev := ovState.prev
	ovState.mu.Unlock()
	if prev == 0 || prev == self {
		return
	}
	if ok, _, _ := pIsWindow.Call(prev); ok != 0 {
		pSetForegroundWindow.Call(prev)
	}
}

// syncHotkeys registers the config's shortcuts with the overlay's
// window, dropping the ones registered before. A chord another
// program holds is named in the log; the others still bind. It runs
// on the window's thread: hotkeys belong to the window.
func syncHotkeys(hwnd uintptr) {
	unregisterHotkeys(hwnd)
	ovState.mu.Lock()
	d := ovState.desk
	ovState.mu.Unlock()
	if d.keys == nil {
		return
	}
	binds, problems := bindings(d.keys())
	for _, p := range problems {
		d.note("shortcut not bound: %s", p)
	}
	bound := map[uintptr]shortcutAction{}
	for i, b := range binds {
		id := uintptr(i + 1)
		vk := vkOf(b.Chord.Key)
		if vk == 0 {
			continue
		}
		if r, _, _ := pRegisterHotKey.Call(hwnd, id, uintptr(b.Chord.Mods|modNoRepeat), uintptr(vk)); r == 0 {
			d.note("shortcut not bound: %s: %s is held by another program; choose another in Axon's settings", actionName(b.Action), b.Chord)
			continue
		}
		bound[id] = b.Action
	}
	ovState.mu.Lock()
	ovState.bound = bound
	ovState.mu.Unlock()
}

func unregisterHotkeys(hwnd uintptr) {
	ovState.mu.Lock()
	ids := ovState.bound
	ovState.bound = nil
	ovState.mu.Unlock()
	for id := range ids {
		pUnregisterHotKey.Call(hwnd, id)
	}
}

// vkOf is a chord key's Windows virtual-key code, 0 for none.
func vkOf(key string) uint32 {
	if len(key) == 1 && (key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9') {
		return uint32(key[0])
	}
	if len(key) >= 2 && key[0] == 'F' {
		n := 0
		for _, r := range key[1:] {
			if r < '0' || r > '9' {
				return 0
			}
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= 24 {
			return 0x70 + uint32(n-1)
		}
		return 0
	}
	return map[string]uint32{
		"Space": 0x20, "Insert": 0x2D, "Delete": 0x2E, "Home": 0x24, "End": 0x23,
		"PageUp": 0x21, "PageDown": 0x22, "Pause": 0x13, "ScrollLock": 0x91,
		"Up": 0x26, "Down": 0x28, "Left": 0x25, "Right": 0x27, "Tab": 0x09, "`": 0xC0,
	}[key]
}

// overlayDo asks the overlay's thread to do cmd.
func overlayDo(cmd uintptr) {
	if w := theOverlay; w != nil && w.hwnd != 0 {
		pPostMessageW.Call(w.hwnd, wmWindowCmd, cmd, 0)
	}
}

func showOverlayWindow()   { overlayDo(cmdShow) }
func hideOverlayWindow()   { overlayDo(cmdHide) }
func releaseOverlayFocus() { overlayDo(cmdRelease) }
func dragOverlayWindow()   { overlayDo(cmdDrag) }

// syncShortcuts re-registers the shortcuts after a save.
func syncShortcuts() { overlayDo(cmdSyncKeys) }

// clipboardText reads the clipboard's text once, under the one open.
// It is called only by a send with the field empty. A busy clipboard
// (the game in the moment after Ctrl+C) gets a few short tries.
func clipboardText() (string, bool) {
	opened := uintptr(0)
	for i := 0; i < 5 && opened == 0; i++ {
		opened, _, _ = pOpenClipboard.Call(0)
		if opened == 0 {
			time.Sleep(30 * time.Millisecond)
		}
	}
	if opened == 0 {
		return "", false
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(h)
	size, _, _ := pGlobalSize.Call(h)
	if size < 2 {
		return "", false
	}
	// The clipboard's memory is Windows', not Go's (see sysPtr).
	u := unsafe.Slice((*uint16)(sysPtr(&p)), int(size)/2)
	return syscall.UTF16ToString(u), true
}
