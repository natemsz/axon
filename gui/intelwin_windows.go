package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// The intel windows on Windows (intel.go): one WebView2 window per
// module, made the first time it is opened, each on its own thread.
// They are frameless and see-through like the overlay, with square
// corners like the game's own windows, resizable from their edges and
// moved by their own title strip; each remembers where the player
// left it (intel-windows.json beside the WebView profile). Closing one
// only hides it.

const (
	wmExitSizeMove = 0x0232
	wsThickFrame   = 0x00040000
	dwmDoNotRound  = 1 // DWMWCP_DONOTROUND
	intelEdge      = 6 // the resizing edge, px at 96 DPI
	intelMinW      = 280
	intelMinH      = 180
)

// intelDefaults are each window's first place, as fractions of the
// main screen's work area, and its size at 96 DPI.
var intelDefaults = map[string]struct {
	fx, fy float64
	w, h   int
}{
	"threads": {0.02, 0.12, 460, 520},
	"map":     {0.70, 0.12, 520, 520},
	"feed":    {0.02, 0.64, 460, 300},
	"report":  {0.30, 0.10, 780, 680},
}

var intelWins struct {
	mu       sync.Mutex
	byModule map[string]*webview
	byHwnd   map[uintptr]*webview
}

var intelViewProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	intelWins.mu.Lock()
	w := intelWins.byHwnd[hwnd]
	intelWins.mu.Unlock()
	if w != nil {
		if r, ok := w.handleIntel(hwnd, uint32(msg), wparam, lparam); ok {
			return r
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
})

// showIntelWindow opens a module's window at url, making it the first
// time. toggle puts it away instead when it is already showing that
// page. false when the WebView2 runtime is missing.
func showIntelWindow(module, url string, toggle bool) bool {
	dll := webviewDLL()
	if dll == "" {
		return false
	}
	intelWins.mu.Lock()
	if intelWins.byModule == nil {
		intelWins.byModule = map[string]*webview{}
		intelWins.byHwnd = map[uintptr]*webview{}
	}
	w := intelWins.byModule[module]
	if w == nil {
		w = &webview{url: url, intel: module, ready: make(chan error, 1)}
		intelWins.byModule[module] = w
		intelWins.mu.Unlock()
		go w.loop(dll)
		go func() {
			if err := <-w.ready; err != nil {
				intelWins.mu.Lock()
				delete(intelWins.byModule, module)
				intelWins.mu.Unlock()
			}
		}()
		return true
	}
	intelWins.mu.Unlock()
	if w.hwnd == 0 {
		return true // still being made: it shows itself
	}
	w.mu.Lock()
	open := w.visible && w.url == url
	if !(toggle && open) {
		w.next = url
	}
	w.mu.Unlock()
	if toggle && open {
		pPostMessageW.Call(w.hwnd, wmWindowCmd, cmdHide, 0)
	} else {
		pPostMessageW.Call(w.hwnd, wmWindowCmd, cmdShow, 0)
	}
	return true
}

// intelWindowDo is a page's request of its own frame.
func intelWindowDo(module, cmd string) {
	intelWins.mu.Lock()
	w := intelWins.byModule[module]
	intelWins.mu.Unlock()
	if w == nil || w.hwnd == 0 {
		return
	}
	switch cmd {
	case "drag":
		pPostMessageW.Call(w.hwnd, wmWindowCmd, cmdDrag, 0)
	case "hide":
		pPostMessageW.Call(w.hwnd, wmWindowCmd, cmdHide, 0)
	}
}

// makeIntelWindow makes a module's window where the player last left
// it, or at its first place.
func (w *webview) makeIntelWindow(inst uintptr) error {
	name, _ := syscall.UTF16PtrFromString("AxonIntel")
	title, _ := syscall.UTF16PtrFromString(AppName + ": " + w.intel)
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	wc := wndClassEx{WndProc: intelViewProc, Instance: inst, ClassName: name, Cursor: cursor}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))) // fails harmlessly after the first
	var work rect
	pSystemParamsInfo.Call(spiWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0)
	r, ok := loadIntelRect(w.intel)
	if !ok || !rectOnScreen(r, work) {
		d := intelDefaults[w.intel]
		r.L = work.L + int32(float64(work.R-work.L)*d.fx)
		r.T = work.T + int32(float64(work.B-work.T)*d.fy)
		r.R = r.L + w.scale(d.w)
		r.B = r.T + w.scale(d.h)
	}
	w.hwnd, _, _ = pCreateWindowExW.Call(wsExTopmost|wsExToolWindow, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
		wsPopup|wsThickFrame|wsClipChildren, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), 0, 0, inst, 0)
	if w.hwnd == 0 {
		return syscall.GetLastError()
	}
	intelWins.mu.Lock()
	intelWins.byHwnd[w.hwnd] = w
	intelWins.mu.Unlock()
	// Now that the procedure knows the window, let it drop the frame.
	pSetWindowPos.Call(w.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged)
	rgn, _, _ := pCreateRectRgn.Call(0, 0, ^uintptr(0), ^uintptr(0))
	bb := dwmBlurBehind{Flags: dwmBBEnable | dwmBBBlurRegion, Enable: 1, Region: rgn}
	pDwmBlurBehind.Call(w.hwnd, uintptr(unsafe.Pointer(&bb)))
	for _, a := range [][2]uint32{{dwmDarkMode, 1}, {dwmCorners, dwmDoNotRound}} {
		v := a[1]
		pDwmSetWindowAttr.Call(w.hwnd, uintptr(a[0]), uintptr(unsafe.Pointer(&v)), 4)
	}
	return nil
}

// handleIntel is an intel window's procedure.
func (w *webview) handleIntel(hwnd uintptr, msg uint32, wparam, lparam uintptr) (uintptr, bool) {
	switch msg {
	case wmNCCalcSize:
		if wparam == 0 {
			return 0, false
		}
		return 0, true // no frame: the page draws its own
	case wmNCHitTest:
		x, y := int32(int16(lparam)), int32(int16(lparam>>16))
		var r rect
		pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		e := w.scale(intelEdge)
		left, right, top, bottom := x < r.L+e, x >= r.R-e, y < r.T+e, y >= r.B-e
		switch {
		case top && left:
			return htTopLeft, true
		case top && right:
			return htTopRight, true
		case bottom && left:
			return htBottomLeft, true
		case bottom && right:
			return htBottomRight, true
		case left:
			return htLeft, true
		case right:
			return htRight, true
		case top:
			return htTop, true
		case bottom:
			return htBottom, true
		}
		return htClient, true
	case wmSize:
		w.resize()
		if w.ctrl != 0 {
			comCall(w.ctrl, 23) // NotifyParentWindowPositionChanged
		}
		return 0, true
	case wmGetMinMaxInfo:
		p := (*[10]int32)(sysPtr(&lparam))
		p[6], p[7] = w.scale(intelMinW), w.scale(intelMinH)
		return 0, true
	case wmDPIChanged:
		r := (*rect)(sysPtr(&lparam))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
		return 0, true
	case wmExitSizeMove:
		var r rect
		pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		saveIntelRect(w.intel, r)
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
		case cmdDrag:
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
		}
		return 0, true
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0, true
	}
	return 0, false
}

// --- Where each window was left ---

var intelRects struct {
	mu sync.Mutex
	m  map[string]rect
}

func intelRectsPath() string { return filepath.Join(profileBase(), "intel-windows.json") }

func loadIntelRect(module string) (rect, bool) {
	intelRects.mu.Lock()
	defer intelRects.mu.Unlock()
	if intelRects.m == nil {
		intelRects.m = map[string]rect{}
		if b, err := os.ReadFile(intelRectsPath()); err == nil {
			_ = json.Unmarshal(b, &intelRects.m)
		}
	}
	r, ok := intelRects.m[module]
	return r, ok
}

func saveIntelRect(module string, r rect) {
	intelRects.mu.Lock()
	defer intelRects.mu.Unlock()
	if intelRects.m == nil {
		intelRects.m = map[string]rect{}
	}
	intelRects.m[module] = r
	if b, err := json.Marshal(intelRects.m); err == nil {
		_ = os.MkdirAll(filepath.Dir(intelRectsPath()), 0o700)
		_ = os.WriteFile(intelRectsPath(), b, 0o600)
	}
}

// rectOnScreen is whether a saved place still has its title strip on
// the work area (a screen unplugged since leaves it off).
func rectOnScreen(r, work rect) bool {
	return r.R-r.L > 0 && r.B-r.T > 0 && r.L < work.R-40 && r.R > work.L+40 && r.T >= work.T-10 && r.T < work.B-30
}
