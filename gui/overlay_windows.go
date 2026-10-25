package gui

import (
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/natemsz/axon/watch"
)

// The overlay window: a small native window that stays on top of
// the game, holding a paste box, the channel it posts to, a Send
// button and the last post's outcome. It is made the tray's way —
// plain Win32 on its own thread — and hidden, not closed, when
// dismissed. Everything it sends goes through the server's desk
// (overlay.go) and the signed client, never around them.

var (
	gdi32 = syscall.NewLazyDLL("gdi32.dll")

	pRegisterHotKey       = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey     = user32.NewProc("UnregisterHotKey")
	pOpenClipboard        = user32.NewProc("OpenClipboard")
	pCloseClipboard       = user32.NewProc("CloseClipboard")
	pGetClipboardData     = user32.NewProc("GetClipboardData")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pSetWindowTextW       = user32.NewProc("SetWindowTextW")
	pEnableWindow         = user32.NewProc("EnableWindow")
	pSetFocus             = user32.NewProc("SetFocus")
	pGetStockObject       = gdi32.NewProc("GetStockObject")
	pGlobalLock           = kernel32.NewProc("GlobalLock")
	pGlobalUnlock         = kernel32.NewProc("GlobalUnlock")
	pGlobalSize           = kernel32.NewProc("GlobalSize")
)

const (
	wmHotkey  = 0x0312
	wmSetFont = 0x0030

	// The overlay's own messages, posted to its window from wherever
	// the news is: show yourself, a post has finished, a channel
	// list has arrived.
	wmOverlayShow = wmApp + 20
	wmOverlayDone = wmApp + 21
	wmOverlayList = wmApp + 22

	ovChannelID = 101
	ovTextID    = 102
	ovSendID    = 103
	ovStatusID  = 104

	ovHotkeyID = 1

	// The grab: Ctrl+Alt+V. Plain V is the game's scanner key and
	// Ctrl+V is paste; the chord is no EVE default. MOD_NOREPEAT
	// keeps a held key from posting twice.
	modAlt      = 0x0001
	modControl  = 0x0002
	modNoRepeat = 0x4000
	vkV         = 0x56

	cfUnicodeText = 13

	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsVScroll      = 0x00200000
	wsBorder       = 0x00800000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsClipChildren = 0x02000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080

	esMultiline   = 0x0004
	esAutoVScroll = 0x0040
	esWantReturn  = 0x1000

	cbsDropdownList = 0x0003

	cbAddString    = 0x0143
	cbResetContent = 0x014B
	cbGetCurSel    = 0x0147
	cbSetCurSel    = 0x014E
	cbErr          = ^uintptr(0)

	bnClicked = 0

	swShowWin   = 5
	hwndTopmost = ^uintptr(0) // HWND_TOPMOST
	swpShowWin  = 0x0040

	defaultGUIFont = 17

	ovUseDefault = 0x80000000 // CW_USEDEFAULT
)

const overlaySupported = true

// ovState is the overlay window's shared state; its window's
// procedure is a callback with no receiver, so it reads this (as
// the tray's does). The window's fields are only touched on its own
// thread; the rest is under mu. selMu guards the pairing of the
// channel list with the combobox's contents, which change together
// (a refresh swaps one and refills the other): a channel read —
// the grab, from another thread — must see the two agree. It is
// taken before mu, never after.
var ovState struct {
	mu       sync.Mutex
	selMu    sync.Mutex
	actions  overlayActions
	wnd      uintptr
	channel  uintptr
	text     uintptr
	send     uintptr
	status   uintptr
	list     []watch.Channel
	doneText string
	doneOK   bool
	listErr  string
	grabOn   bool
}

var overlayProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	if r, ok := overlayHandle(hwnd, uint32(msg), wparam, lparam); ok {
		return r
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
})

func overlayDPI(wnd uintptr) uint32 {
	if wnd != 0 {
		if d, _, _ := pGetDpiForWindow.Call(wnd); d != 0 {
			return uint32(d)
		}
	}
	if d, _, _ := pGetDpiForSystem.Call(); d != 0 {
		return uint32(d)
	}
	return 96
}

func overlayScale(dpi uint32, v int32) int32 { return v * int32(dpi) / 96 }

// runOverlay makes the overlay window (hidden) and runs its
// messages, on one OS thread, for the life of the process. ready is
// closed once the window stands (or could not be made).
func runOverlay(a overlayActions, ready chan<- struct{}) {
	ovState.mu.Lock()
	ovState.actions = a
	ovState.mu.Unlock()
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done := sync1(ready)
		inst, _, _ := pGetModuleHandleW.Call(0)
		name, _ := syscall.UTF16PtrFromString("AxonOverlay")
		title, _ := syscall.UTF16PtrFromString(AppName + ": paste intel")
		wc := wndClassEx{WndProc: overlayProc, Instance: inst, ClassName: name}
		wc.Size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		dpi := overlayDPI(0)
		wnd, _, _ := pCreateWindowExW.Call(wsExTopmost|wsExToolWindow,
			uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
			wsCaption|wsSysMenu|wsMinimizeBox|wsClipChildren,
			ovUseDefault, ovUseDefault,
			uintptr(overlayScale(dpi, 452)), uintptr(overlayScale(dpi, 372)),
			0, 0, inst, 0)
		if wnd == 0 {
			done()
			return
		}
		ovState.mu.Lock()
		ovState.wnd = wnd
		ovState.mu.Unlock()
		font, _, _ := pGetStockObject.Call(defaultGUIFont)
		child := func(class, text string, style uintptr, x, y, w, h int32, id uintptr) uintptr {
			c, _ := syscall.UTF16PtrFromString(class)
			t, _ := syscall.UTF16PtrFromString(text)
			hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)),
				style, uintptr(overlayScale(dpi, x)), uintptr(overlayScale(dpi, y)),
				uintptr(overlayScale(dpi, w)), uintptr(overlayScale(dpi, h)),
				wnd, id, inst, 0)
			if hwnd != 0 {
				pSendMessageW.Call(hwnd, wmSetFont, font, 1)
			}
			return hwnd
		}
		plain := uintptr(wsChild | wsVisible)
		child("STATIC", "Post to", plain, 10, 13, 52, 20, 0)
		ovState.channel = child("COMBOBOX", "", plain|wsTabStop|wsVScroll|cbsDropdownList, 66, 10, 348, 200, ovChannelID)
		ovState.text = child("EDIT", "", plain|wsTabStop|wsBorder|esMultiline|esAutoVScroll|esWantReturn|wsVScroll, 10, 42, 404, 178, ovTextID)
		ovState.send = child("BUTTON", "Send", plain|wsTabStop, 10, 232, 96, 30, ovSendID)
		ovState.status = child("STATIC", "Paste an intel line or a local list, and Send. It posts as you, the way a line from your channels does.", plain, 116, 232, 298, 46, ovStatusID)
		overlaySyncGrab()
		done()
		var m msg
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
}

// showOverlayWindow brings the overlay up, from wherever the ask
// came (the page, the tray menu).
func showOverlayWindow() {
	ovState.mu.Lock()
	wnd := ovState.wnd
	ovState.mu.Unlock()
	if wnd != 0 {
		pPostMessageW.Call(wnd, wmOverlayShow, 0, 0)
	}
}

// overlayHandle is the overlay window's procedure.
func overlayHandle(hwnd uintptr, message uint32, wparam, lparam uintptr) (uintptr, bool) {
	switch message {
	case wmOverlayShow:
		// The grab's setting may have been saved since the window
		// was made; the hotkey follows the config as it stands.
		overlaySyncGrab()
		overlayRefresh()
		pShowWindow.Call(hwnd, swShowWin)
		pSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpShowWin)
		pSetForegroundWindow.Call(hwnd)
		pSetFocus.Call(ovState.text)
		return 0, true
	case wmOverlayList:
		overlayFillChannels()
		return 0, true
	case wmOverlayDone:
		ovState.mu.Lock()
		text, ok := ovState.doneText, ovState.doneOK
		ovState.mu.Unlock()
		setOverlayText(ovState.status, text)
		overlayEnable(true)
		if ok {
			setOverlayText(ovState.text, "")
			pSetFocus.Call(ovState.text)
		}
		return 0, true
	case wmHotkey:
		if wparam == ovHotkeyID {
			overlayGrab()
		}
		return 0, true
	case wmCommand:
		if wparam&0xffff == ovSendID && wparam>>16 == bnClicked {
			overlaySend()
		}
		return 0, true
	case wmClose:
		pShowWindow.Call(hwnd, swHideWin)
		return 0, true
	case wmDestroy:
		if ovState.grabOn {
			pUnregisterHotKey.Call(hwnd, ovHotkeyID)
			ovState.grabOn = false
		}
		pPostQuitMessage.Call(0)
		return 0, true
	}
	return 0, false
}

// overlaySyncGrab registers or drops the grab hotkey to match the
// config as it stands. It runs on the overlay's thread: the hotkey
// belongs to its window.
func overlaySyncGrab() {
	ovState.mu.Lock()
	a, wnd, on := ovState.actions, ovState.wnd, ovState.grabOn
	ovState.mu.Unlock()
	if wnd == 0 || a.grabEnabled == nil {
		return
	}
	switch want := a.grabEnabled(); {
	case want && !on:
		r, _, _ := pRegisterHotKey.Call(wnd, ovHotkeyID, modAlt|modControl|modNoRepeat, vkV)
		if r == 0 {
			a.note("overlay: could not register the %s grab hotkey — another program may hold it; the grab is off", GrabChord)
			return
		}
		ovState.mu.Lock()
		ovState.grabOn = true
		ovState.mu.Unlock()
		a.note("overlay: the clipboard grab is on: %s posts the clipboard to the overlay's channel", GrabChord)
	case !want && on:
		pUnregisterHotKey.Call(wnd, ovHotkeyID)
		ovState.mu.Lock()
		ovState.grabOn = false
		ovState.mu.Unlock()
		a.note("overlay: the clipboard grab is off")
	}
}

// overlayRefresh fetches the channels a paste can go to, off the
// window's thread, and posts the answer back to it.
func overlayRefresh() {
	ovState.mu.Lock()
	a, wnd := ovState.actions, ovState.wnd
	ovState.mu.Unlock()
	go func() {
		_, list, err := a.channels()
		ovState.selMu.Lock()
		ovState.mu.Lock()
		ovState.list = list
		ovState.listErr = ""
		if err != nil {
			ovState.listErr = err.Error()
		}
		ovState.mu.Unlock()
		ovState.selMu.Unlock()
		pPostMessageW.Call(wnd, wmOverlayList, 0, 0)
	}()
}

// overlayFillChannels settles the channel list in the window: the
// names, the choice kept where it can be, and the outcome said in
// the status line when there is nothing to post to.
func overlayFillChannels() {
	ovState.selMu.Lock()
	defer ovState.selMu.Unlock()
	ovState.mu.Lock()
	list, listErr, doneText := ovState.list, ovState.listErr, ovState.doneText
	ovState.mu.Unlock()
	keep := overlayChannelLocked()
	pSendMessageW.Call(ovState.channel, cbResetContent, 0, 0)
	pick := uintptr(0)
	for i, c := range list {
		t, _ := syscall.UTF16PtrFromString(c.Name + " (#" + strconv.FormatInt(c.ID, 10) + ")")
		pSendMessageW.Call(ovState.channel, cbAddString, 0, uintptr(unsafe.Pointer(t)))
		if c.ID == keep {
			pick = uintptr(i)
		}
	}
	if len(list) > 0 {
		pSendMessageW.Call(ovState.channel, cbSetCurSel, pick, 0)
		overlayEnable(true)
		if listErr == "" && doneText == "" {
			setOverlayText(ovState.status, "Ready.")
		}
	} else {
		overlayEnable(false)
		if listErr != "" {
			setOverlayText(ovState.status, "Not posted anywhere yet: "+listErr)
		}
	}
}

// overlayChannel is the chosen channel's ID, or 0 when none is.
func overlayChannel() int64 {
	ovState.selMu.Lock()
	defer ovState.selMu.Unlock()
	return overlayChannelLocked()
}

// overlayChannelLocked is overlayChannel with selMu held: the list
// it reads by the combobox's index is the list the box was filled
// from.
func overlayChannelLocked() int64 {
	ovState.mu.Lock()
	list := ovState.list
	ovState.mu.Unlock()
	i, _, _ := pSendMessageW.Call(ovState.channel, cbGetCurSel, 0, 0)
	if i == cbErr || int(i) >= len(list) {
		return 0
	}
	return list[i].ID
}

func overlayEnable(on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	pEnableWindow.Call(ovState.send, v)
	pEnableWindow.Call(ovState.channel, v)
}

// overlaySend posts the box's text to the chosen channel, off the
// window's thread, and posts the outcome back: the status line says
// it either way, and the box clears only on success, so a failed
// paste is still there to try again.
func overlaySend() {
	text := overlayText(ovState.text)
	channel := overlayChannel()
	ovState.mu.Lock()
	a, wnd := ovState.actions, ovState.wnd
	ovState.mu.Unlock()
	if channel == 0 {
		setOverlayText(ovState.status, "Choose a channel to post to first.")
		return
	}
	setOverlayText(ovState.status, "Posting…")
	overlayEnable(false)
	go func() {
		say, err := a.post(channel, text)
		ovState.mu.Lock()
		if err != nil {
			ovState.doneText, ovState.doneOK = "Not posted: "+err.Error(), false
		} else {
			ovState.doneText, ovState.doneOK = say, true
		}
		ovState.mu.Unlock()
		pPostMessageW.Call(wnd, wmOverlayDone, 0, 0)
	}()
}

// overlayGrab is the hotkey: the clipboard is read once, here, and
// posted to the overlay's channel (the first there is, when the
// overlay has not been opened to choose one). Nothing else about
// the keyboard or the clipboard is ever looked at. The outcome is
// announced by the tray and the log, since the window may be shut.
func overlayGrab() {
	ovState.mu.Lock()
	a, wnd := ovState.actions, ovState.wnd
	ovState.mu.Unlock()
	text, ok := overlayClipboardText(wnd)
	if !ok || len(text) == 0 {
		a.note("overlay: the grab found no text on the clipboard")
		trayNote("The clipboard held no text to post.")
		return
	}
	go func() {
		channel := overlayChannel()
		if channel == 0 {
			_, list, err := a.channels()
			if err != nil || len(list) == 0 {
				a.note("overlay: the grab could not post: no channel to post to")
				trayNote("The grab could not post: no channel to post to.")
				return
			}
			channel = list[0].ID
		}
		say, err := a.post(channel, text)
		if err != nil {
			trayNote("Not posted: " + err.Error())
			return
		}
		trayNote(say + ".")
	}()
}

// overlayClipboardText reads the clipboard's text once, under the
// one open. A busy clipboard — the game in the moment after Ctrl+C —
// gets a few short tries.
func overlayClipboardText(wnd uintptr) (string, bool) {
	opened := uintptr(0)
	for i := 0; i < 5 && opened == 0; i++ {
		opened, _, _ = pOpenClipboard.Call(wnd)
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

func overlayText(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	got, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:got])
}

func setOverlayText(hwnd uintptr, s string) {
	t, _ := syscall.UTF16PtrFromString(s)
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(t)))
}
