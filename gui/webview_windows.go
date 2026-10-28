package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/natemsz/axon/internal/icon"
)

// Axon's window on Windows: a window of Axon's own, with no title bar
// of the system's (the page draws its own, in Axon's colours), holding
// the WebView2 control, the Edge engine Windows ships for exactly
// this. Closing it hides it: Axon stays in the tray, and showing it
// again is instant.
//
// WebView2 is reached as Microsoft's loader DLL reaches it: by asking
// the installed runtime (its location is in the registry) to make an
// environment, then a controller for the window. No DLL ships with
// Axon. Where the runtime is missing, Axon falls back to an app-mode
// browser window (window.go).

var (
	dwmapi = syscall.NewLazyDLL("dwmapi.dll")

	pLoadLibraryExW          = kernel32.NewProc("LoadLibraryExW")
	pGetProcAddress          = kernel32.NewProc("GetProcAddress")
	pCoInitializeEx          = syscall.NewLazyDLL("ole32.dll").NewProc("CoInitializeEx")
	pRegGetValueW            = syscall.NewLazyDLL("advapi32.dll").NewProc("RegGetValueW")
	pDwmExtendFrame          = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	pSetWindowPos            = user32.NewProc("SetWindowPos")
	pGetClientRect           = user32.NewProc("GetClientRect")
	pIsZoomed                = user32.NewProc("IsZoomed")
	pReleaseCapture          = user32.NewProc("ReleaseCapture")
	pSendMessageW            = user32.NewProc("SendMessageW")
	pGetDpiForWindow         = user32.NewProc("GetDpiForWindow")
	pGetDpiForSystem         = user32.NewProc("GetDpiForSystem")
	pGetSystemMetricsForDpi  = user32.NewProc("GetSystemMetricsForDpi")
	pSetProcessDpiAwarenessC = user32.NewProc("SetProcessDpiAwarenessContext")
	pCreateSolidBrush        = syscall.NewLazyDLL("gdi32.dll").NewProc("CreateSolidBrush")
	pLoadCursorW             = user32.NewProc("LoadCursorW")
	pGetWindowRect           = user32.NewProc("GetWindowRect")
	pMonitorFromWindow       = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW         = user32.NewProc("GetMonitorInfoW")
)

const (
	wmSize          = 0x0005
	wmActivate      = 0x0006
	wmShowWindowMsg = 0x0018
	wmGetMinMaxInfo = 0x0024
	wmSetIcon       = 0x0080
	wmNCCalcSize    = 0x0083
	wmNCHitTest     = 0x0084
	wmNCLButtonDown = 0x00A1
	wmDPIChanged    = 0x02E0
	wmWindowCmd     = wmApp + 10 // wParam: one of the cmd values

	cmdShow = iota
	cmdHide
	cmdMinimize
	cmdMaximize
	cmdDrag
	cmdDestroy

	wsOverlappedWindow = 0x00CF0000
	swHideWin          = 0
	swShowNormal       = 1
	swMinimize         = 6
	swMaximize         = 3
	swRestoreWin       = 9
	htClient           = 1
	htCaption          = 2
	htLeft             = 10
	htRight            = 11
	htTop              = 12
	htTopLeft          = 13
	htTopRight         = 14
	htBottom           = 15
	htBottomLeft       = 16
	htBottomRight      = 17
	smCxFrame          = 32
	smCyFrame          = 33
	smCxPaddedBorder   = 92
	swpNoZOrder        = 0x0004
	swpNoActivate      = 0x0010
	swpFrameChanged    = 0x0020
	swpNoMove          = 0x0002
	swpNoSize          = 0x0001
	idcArrow           = 32512
	monitorNearest     = 2
	rrfRtRegSZ         = 0x00000002
	rrfSubkeyWow6432   = 0x00020000 // the 32-bit view, where EdgeUpdate registers
	hklm               = 0x80000002
	hkcu               = 0x80000001
	loadWithAlteredDir = 0x00000008

	// winW, winH: the window's size at 96 DPI; the page's layout is
	// made for it (app.css). minW, minH: the smallest it goes.
	winW, winH = 1120, 860
	minW, minH = 860, 600
	// edge is the resizing border left around the page, in pixels at
	// 96 DPI, painted in the top bar's colour.
	edge = 4
)

// webviewRuntimeKey is the WebView2 runtime's registration.
const webviewRuntimeKey = `SOFTWARE\Microsoft\EdgeUpdate\ClientState\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// webviewDLL is the installed runtime's EmbeddedBrowserWebView.dll,
// or "" when there is none.
func webviewDLL() string {
	read := func(root uintptr, key string) string {
		buf := make([]uint16, 1024)
		size := uint32(len(buf) * 2)
		k, _ := syscall.UTF16PtrFromString(key)
		v, _ := syscall.UTF16PtrFromString("EBWebView")
		r, _, _ := pRegGetValueW.Call(root, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v)), rrfRtRegSZ|rrfSubkeyWow6432, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		if r != 0 {
			return ""
		}
		return syscall.UTF16ToString(buf)
	}
	for _, root := range []uintptr{hklm, hkcu} {
		dir := read(root, webviewRuntimeKey)
		if dir == "" {
			continue
		}
		dll := filepath.Join(dir, "EBWebView", "x64", "EmbeddedBrowserWebView.dll")
		if _, err := os.Stat(dll); err == nil {
			return dll
		}
	}
	return ""
}

// comHandler is a COM object made in Go: a completion handler with
// one Invoke. Its methods are callbacks shared by every handler of a
// kind; handlers live as long as the program, so counting is moot.
type comHandler struct {
	vtbl *[4]uintptr
}

var (
	comQI = syscall.NewCallback(func(this, _, out uintptr) uintptr {
		*(*uintptr)(sysPtr(&out)) = this
		return 0
	})
	comAddRef  = syscall.NewCallback(func(uintptr) uintptr { return 1 })
	comRelease = syscall.NewCallback(func(uintptr) uintptr { return 1 })
)

func newHandler(invoke func(hr, obj uintptr) uintptr) *comHandler {
	return &comHandler{vtbl: &[4]uintptr{comQI, comAddRef, comRelease, syscall.NewCallback(func(_, hr, obj uintptr) uintptr { return invoke(hr, obj) })}}
}

// comCall calls method i of the COM object at obj.
func comCall(obj uintptr, i int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(unsafe.Pointer(&obj))
	vtbl = *(*unsafe.Pointer)(vtbl)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

type rect struct{ L, T, R, B int32 }

// webview is the window and its WebView2.
type webview struct {
	url     string
	closed  func()
	hwnd    uintptr
	ctrl    uintptr
	ready   chan error
	visible bool
	mu      sync.Mutex
	// overlay makes this the overlay's window (overlay_windows.go)
	// instead of Axon's own: frameless, always on top, out of the
	// taskbar, see-through, and hidden until its shortcut brings it
	// up.
	overlay bool
	// panel makes this the site panel (overlay_windows.go): the
	// site's own pages in a framed window on top, hidden until a
	// shortcut opens one. next is the page the next show loads.
	panel bool
	next  string
	core  uintptr
	// siteOrigin is the panel's site ("https://host/"): the only page
	// whose window-control messages the panel takes.
	siteOrigin string
	// intel makes this one of the intel windows (intelwin_windows.go):
	// the module it draws. Frameless, see-through, on top.
	intel string
}

// the one window's procedure is a callback with no receiver: it reads
// this.
var theWebview *webview

var webviewProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	if w := theWebview; w != nil {
		if r, ok := w.handle(hwnd, uint32(msg), wparam, lparam); ok {
			return r
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
})

// newWebview opens the window at url, waiting until the page is
// there; an error means WebView2 cannot be used here.
func newWebview(url string, closed func()) (*webview, error) {
	dll := webviewDLL()
	if dll == "" {
		return nil, errors.New("the WebView2 runtime is not installed")
	}
	w := &webview{url: url, closed: closed, ready: make(chan error, 1)}
	theWebview = w
	go w.loop(dll)
	select {
	case err := <-w.ready:
		if err != nil {
			theWebview = nil
			return nil, err
		}
		return w, nil
	case <-time.After(20 * time.Second):
		return nil, errors.New("WebView2 did not start")
	}
}

func (w *webview) fail(err error) {
	select {
	case w.ready <- err:
	default:
	}
}

func (w *webview) dpi() uint32 {
	if w.hwnd != 0 {
		if d, _, _ := pGetDpiForWindow.Call(w.hwnd); d != 0 {
			return uint32(d)
		}
	}
	if d, _, _ := pGetDpiForSystem.Call(); d != 0 {
		return uint32(d)
	}
	return 96
}

func (w *webview) scale(v int) int32 { return int32(v * int(w.dpi()) / 96) }

// loop makes the window and runs its messages, on one OS thread.
func (w *webview) loop(dll string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetProcessDpiAwarenessC.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4); fails harmlessly when already set
	pCoInitializeEx.Call(0, 0x2)

	inst, _, _ := pGetModuleHandleW.Call(0)
	if w.overlay || w.panel || w.intel != "" {
		makeWin := w.makeOverlayWindow
		if w.intel != "" {
			makeWin = w.makeIntelWindow
		} else if w.panel {
			makeWin = w.makePanelWindow
		}
		if err := makeWin(inst); err != nil {
			w.fail(err)
			return
		}
		if err := w.start(dll); err != nil {
			w.fail(err)
			pDestroyWindow.Call(w.hwnd)
			return
		}
		w.pump()
		return
	}
	name, _ := syscall.UTF16PtrFromString("AxonWindow")
	title, _ := syscall.UTF16PtrFromString(AppName)
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	brush, _, _ := pCreateSolidBrush.Call(0x00060b14) // #140b06, as COLORREF 0x00BBGGRR
	wc := wndClassEx{WndProc: webviewProc, Instance: inst, ClassName: name, Cursor: cursor, Background: brush}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	width, height := w.scale(winW), w.scale(winH)
	const cwUseDefault = 0x80000000
	w.hwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)), wsOverlappedWindow,
		cwUseDefault, cwUseDefault, uintptr(width), uintptr(height), 0, 0, inst, 0)
	if w.hwnd == 0 {
		w.fail(errors.New("could not make the window"))
		return
	}
	// The window's own icon, for the taskbar and Alt+Tab.
	for _, s := range []struct{ kind, px uintptr }{{0, 16}, {1, 32}} {
		png := icon.PNG(int(s.px))
		h, _, _ := pCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&png[0])), uintptr(len(png)), 1, 0x00030000, s.px, s.px, 0)
		pSendMessageW.Call(w.hwnd, wmSetIcon, s.kind, h)
	}
	// A hairline of the frame kept under the page gives the window
	// the system's shadow and rounded corners.
	margins := [4]int32{0, 0, 1, 0}
	pDwmExtendFrame.Call(w.hwnd, uintptr(unsafe.Pointer(&margins)))
	pSetWindowPos.Call(w.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged)

	if err := w.start(dll); err != nil {
		w.fail(err)
		pDestroyWindow.Call(w.hwnd)
		return
	}
	w.pump()
}

// pump runs the window thread's messages until the window is gone.
func (w *webview) pump() {
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// start asks the runtime for an environment, then a controller for
// the window; the page loads once both have come (on this thread, by
// the message loop).
func (w *webview) start(dll string) error {
	path, _ := syscall.UTF16PtrFromString(dll)
	mod, _, _ := pLoadLibraryExW.Call(uintptr(unsafe.Pointer(path)), 0, loadWithAlteredDir)
	if mod == 0 {
		return fmt.Errorf("could not load %s", dll)
	}
	fname := []byte("CreateWebViewEnvironmentWithOptionsInternal\x00")
	create, _, _ := pGetProcAddress.Call(mod, uintptr(unsafe.Pointer(&fname[0])))
	if create == 0 {
		return errors.New("the WebView2 runtime has no entry point Axon knows")
	}
	data := filepath.Join(profileBase(), "webview")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	dataPtr, _ := syscall.UTF16PtrFromString(data)

	ctrlDone := newHandler(func(hr, ctrl uintptr) uintptr {
		if int32(hr) < 0 || ctrl == 0 {
			w.fail(fmt.Errorf("WebView2 controller: HRESULT 0x%08X", uint32(hr)))
			return 0
		}
		comCall(ctrl, 1) // AddRef: kept for the program's life
		w.ctrl = ctrl
		w.setup()
		return 0
	})
	envDone := newHandler(func(hr, env uintptr) uintptr {
		if int32(hr) < 0 || env == 0 {
			w.fail(fmt.Errorf("WebView2 environment: HRESULT 0x%08X", uint32(hr)))
			return 0
		}
		// ICoreWebView2Environment::CreateCoreWebView2Controller
		if r := comCall(env, 3, w.hwnd, uintptr(unsafe.Pointer(ctrlDone))); int32(r) < 0 {
			w.fail(fmt.Errorf("WebView2 controller: HRESULT 0x%08X", uint32(r)))
		}
		return 0
	})
	keep = append(keep, ctrlDone, envDone)
	// (check for a running instance, installed runtime, data folder,
	// options, handler): what the loader passes.
	r, _, _ := syscall.SyscallN(create, 1, 0, uintptr(unsafe.Pointer(dataPtr)), 0, uintptr(unsafe.Pointer(envDone)))
	if int32(r) < 0 {
		return fmt.Errorf("WebView2 environment: HRESULT 0x%08X", uint32(r))
	}
	return nil
}

// keep holds the handlers the runtime calls back, for good.
var keep []*comHandler

// setup settles the WebView's settings and loads the page, then shows
// the window.
func (w *webview) setup() {
	// Controller2: the page's background before it paints, so the
	// window never flashes white. COREWEBVIEW2_COLOR {A,R,G,B}.
	iid := guidOf(0xc979903e, 0xd4ca, 0x4228, [8]byte{0x92, 0xeb, 0x47, 0xee, 0x3f, 0xa9, 0x6e, 0xab})
	var c2 uintptr
	if comCall(w.ctrl, 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&c2))) == 0 && c2 != 0 {
		bg := uintptr(uint32(0xff) | 0x0d<<8 | 0x05<<16 | 0x03<<24)
		if w.overlay || w.intel != "" {
			// Fully transparent: the page paints its own see-through
			// panel over the system backdrop.
			bg = 0
		}
		comCall(c2, 27, bg)
	}
	var core uintptr
	comCall(w.ctrl, 25, uintptr(unsafe.Pointer(&core))) // get_CoreWebView2
	w.core = core
	if core == 0 {
		w.fail(errors.New("WebView2 gave no view"))
		return
	}
	var settings uintptr
	comCall(core, 3, uintptr(unsafe.Pointer(&settings))) // get_Settings
	if settings != 0 {
		comCall(settings, 10, 0) // put_IsStatusBarEnabled
		comCall(settings, 12, 0) // put_AreDevToolsEnabled
		comCall(settings, 14, 0) // put_AreDefaultContextMenusEnabled
		comCall(settings, 18, 0) // put_IsZoomControlEnabled
		comCall(settings, 24, 0) // put_AreBrowserAcceleratorKeysEnabled (reload, print, find)
	}
	if w.panel {
		w.panelHooks(core)
	}
	w.resize()
	u, _ := syscall.UTF16PtrFromString(w.url)
	comCall(core, 5, uintptr(unsafe.Pointer(u))) // Navigate
	if !w.overlay && !w.panel {
		w.showNow()
	}
	w.fail(nil)
}

func guidOf(a uint32, b, c uint16, d [8]byte) [16]byte {
	var g [16]byte
	*(*uint32)(unsafe.Pointer(&g[0])) = a
	*(*uint16)(unsafe.Pointer(&g[4])) = b
	*(*uint16)(unsafe.Pointer(&g[6])) = c
	copy(g[8:], d[:])
	return g
}

// resize fits the page to the window, inside the resizing border.
func (w *webview) resize() {
	if w.ctrl == 0 {
		return
	}
	var r rect
	pGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	if z, _, _ := pIsZoomed.Call(w.hwnd); z == 0 && !w.overlay && !w.panel && w.intel == "" {
		e := w.scale(edge)
		r.L += e
		r.R -= e
		r.B -= e
		r.T += 1
	}
	comCall(w.ctrl, 6, uintptr(unsafe.Pointer(&r))) // put_Bounds
}

func (w *webview) showNow() {
	pShowWindow.Call(w.hwnd, swRestoreWin)
	pSetForegroundWindow.Call(w.hwnd)
	comCall(w.ctrl, 4, 1) // put_IsVisible
	w.mu.Lock()
	w.visible = true
	w.mu.Unlock()
}

func (w *webview) hideNow() {
	comCall(w.ctrl, 4, 0)
	pShowWindow.Call(w.hwnd, swHideWin)
	w.mu.Lock()
	was := w.visible
	w.visible = false
	w.mu.Unlock()
	if was && w.closed != nil {
		go w.closed()
	}
}

// handle is the window's procedure; ok false leaves a message to the
// system.
func (w *webview) handle(hwnd uintptr, msg uint32, wparam, lparam uintptr) (uintptr, bool) {
	switch msg {
	case wmNCCalcSize:
		if wparam == 0 {
			return 0, false
		}
		// The whole window is the page's: no title bar. Maximized, a
		// window overhangs the screen by its frame; keep it on it.
		if z, _, _ := pIsZoomed.Call(hwnd); z != 0 {
			r := (*rect)(sysPtr(&lparam))
			dpi := uintptr(w.dpi())
			fx, _, _ := pGetSystemMetricsForDpi.Call(smCxFrame, dpi)
			fy, _, _ := pGetSystemMetricsForDpi.Call(smCyFrame, dpi)
			pad, _, _ := pGetSystemMetricsForDpi.Call(smCxPaddedBorder, dpi)
			r.L += int32(fx + pad)
			r.R -= int32(fx + pad)
			r.T += int32(fy + pad)
			r.B -= int32(fy + pad)
		}
		return 0, true
	case wmNCHitTest:
		// The border around the page resizes; the page itself says
		// where it can be dragged (cmdDrag).
		x, y := int32(int16(lparam)), int32(int16(lparam>>16))
		var r rect
		pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		e := w.scale(edge + 2)
		left, right, top, bottom := x < r.L+e, x >= r.R-e, y < r.T+e, y >= r.B-e
		if z, _, _ := pIsZoomed.Call(hwnd); z != 0 {
			return htClient, true
		}
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
		// MINMAXINFO.ptMinTrackSize is at offset 24.
		p := (*[10]int32)(sysPtr(&lparam))
		p[6], p[7] = w.scale(minW), w.scale(minH)
		return 0, true
	case wmDPIChanged:
		r := (*rect)(sysPtr(&lparam))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), swpNoZOrder|swpNoActivate)
		return 0, true
	case wmClose:
		// The close box, Alt+F4: to the tray.
		w.hideNow()
		return 0, true
	case wmWindowCmd:
		switch wparam {
		case cmdShow:
			w.showNow()
		case cmdHide:
			w.hideNow()
		case cmdMinimize:
			pShowWindow.Call(hwnd, swMinimize)
		case cmdMaximize:
			if z, _, _ := pIsZoomed.Call(hwnd); z != 0 {
				pShowWindow.Call(hwnd, swRestoreWin)
			} else {
				pShowWindow.Call(hwnd, swMaximize)
			}
		case cmdDrag:
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
		case cmdDestroy:
			if w.ctrl != 0 {
				comCall(w.ctrl, 24) // Close
			}
			pDestroyWindow.Call(hwnd)
		}
		return 0, true
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0, true
	}
	return 0, false
}

// do asks the window's thread to do cmd.
func (w *webview) do(cmd uintptr) {
	pPostMessageW.Call(w.hwnd, wmWindowCmd, cmd, 0)
}

func (w *webview) isVisible() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.visible
}

// isMaximized reports whether the window fills the screen.
func (w *webview) isMaximized() bool {
	z, _, _ := pIsZoomed.Call(w.hwnd)
	return z != 0
}

// sysPtr reads an address Windows passed as a number (a message's
// lparam, a callback's out-parameter) as the pointer it is. The memory
// is Windows', not Go's, so the garbage collector has no stake in it.
func sysPtr(p *uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(p)) }
