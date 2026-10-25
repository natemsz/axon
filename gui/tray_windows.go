package gui

import (
	"context"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/natemsz/axon/internal/icon"
)

// The tray icon, through the Win32 calls themselves (the standard
// library's syscall reaches them; no cgo). A hidden window owns the
// icon and receives its clicks: double-click opens the settings page,
// right-click shows the menu.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenuW              = user32.NewProc("AppendMenuW")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pShowWindow               = user32.NewProc("ShowWindow")
	pRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	pCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	pShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy    = 0x0002
	wmClose      = 0x0010
	wmNull       = 0x0000
	wmCommand    = 0x0111
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205
	wmApp        = 0x8000
	wmTray       = wmApp + 1
	wmBalloon    = wmApp + 2

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifMsg    = 0x01
	nifIcon   = 0x02
	nifTip    = 0x04
	nifInfo   = 0x10

	mfString    = 0x0000
	mfSeparator = 0x0800
	mfDefault   = 0x1000
	tpmReturn   = 0x0100
	tpmNoNotify = 0x0080
	tpmRight    = 0x0008
	tpmBottom   = 0x0020
)

const (
	menuOpen = 1 + iota
	menuToggle
	menuOverlay
	menuLog
	menuQuit
)

type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            [16]byte
	BalloonIcon     uintptr
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msg struct {
	Wnd     uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	X, Y    int32
	Private uint32
}

// trayWindow is the one tray icon's window; its procedure is a
// callback with no room for a receiver, so it reads this.
var tray struct {
	wnd     uintptr
	icon    uintptr
	created uint32 // "TaskbarCreated": Explorer restarted, add the icon again
	actions trayActions
	balloon string
}

const traySupported = true

func setTip(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	copy(dst[:len(dst)-1], u)
}

func notify(op uintptr, flags uint32, info string) {
	d := notifyIconData{Wnd: tray.wnd, ID: 1, Flags: flags, CallbackMessage: wmTray, Icon: tray.icon}
	d.Size = uint32(unsafe.Sizeof(d))
	setTip(d.Tip[:], "Axon")
	if info != "" {
		setTip(d.Info[:], info)
		setTip(d.InfoTitle[:], "Axon")
	}
	pShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&d)))
}

func wndProc(hwnd, message, wparam, lparam uintptr) uintptr {
	switch uint32(message) {
	case wmTray:
		switch uint32(lparam) & 0xffff {
		case wmLButtonDbl:
			go tray.actions.open()
		case wmRButtonUp:
			showMenu(hwnd)
		}
		return 0
	case wmBalloon:
		notify(nimModify, nifInfo, tray.balloon)
		return 0
	case wmClose:
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		notify(nimDelete, 0, "")
		pPostQuitMessage.Call(0)
		return 0
	}
	if tray.created != 0 && uint32(message) == tray.created {
		notify(nimAdd, nifMsg|nifIcon|nifTip, "")
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, message, wparam, lparam)
	return r
}

func appendMenu(m uintptr, flags, id uintptr, text string) {
	t, _ := syscall.UTF16PtrFromString(text)
	pAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(t)))
}

func showMenu(hwnd uintptr) {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	appendMenu(m, mfString|mfDefault, menuOpen, "Open settings")
	if tray.actions.running() {
		appendMenu(m, mfString, menuToggle, "Stop watching")
	} else {
		appendMenu(m, mfString, menuToggle, "Start watching")
	}
	if tray.actions.overlay != nil {
		appendMenu(m, mfString, menuOverlay, "Paste intel…")
	}
	if tray.actions.logFile != "" {
		appendMenu(m, mfString, menuLog, "Open the log file")
	}
	appendMenu(m, mfSeparator, 0, "")
	appendMenu(m, mfString, menuQuit, "Quit Axon")
	var pt struct{ X, Y int32 }
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(hwnd)
	id, _, _ := pTrackPopupMenu.Call(m, tpmReturn|tpmNoNotify|tpmRight|tpmBottom, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	pPostMessageW.Call(hwnd, wmNull, 0, 0)
	switch id {
	case menuOpen:
		go tray.actions.open()
	case menuToggle:
		go tray.actions.toggle()
	case menuOverlay:
		go tray.actions.overlay()
	case menuLog:
		_ = OpenBrowser(tray.actions.logFile)
	case menuQuit:
		go tray.actions.quit()
	}
}

// runTray shows the icon until ctx ends. ready is closed once the
// icon is up (or failed to come up).
func runTray(ctx context.Context, a trayActions, ready chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tray.actions = a
	done := sync1(ready)
	if png := icon.PNG(32); len(png) > 0 {
		tray.icon, _, _ = pCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&png[0])), uintptr(len(png)), 1, 0x00030000, 32, 32, 0)
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	name, _ := syscall.UTF16PtrFromString("AxonTray")
	wc := wndClassEx{WndProc: syscall.NewCallback(wndProc), Instance: inst, ClassName: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	tray.wnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	if tray.wnd == 0 {
		done()
		return
	}
	tc, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(tc)))
	tray.created = uint32(r)
	notify(nimAdd, nifMsg|nifIcon|nifTip, "")
	done()
	go func() {
		<-ctx.Done()
		pPostMessageW.Call(tray.wnd, wmClose, 0, 0)
	}()
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

func sync1(c chan<- struct{}) func() {
	closed := false
	return func() {
		if !closed {
			closed = true
			close(c)
		}
	}
}

// trayNote shows a notification from the icon.
func trayNote(text string) {
	if tray.wnd == 0 {
		return
	}
	tray.balloon = text
	pPostMessageW.Call(tray.wnd, wmBalloon, 0, 0)
}
