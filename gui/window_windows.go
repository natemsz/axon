package gui

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	pEnumWindows      = user32.NewProc("EnumWindows")
	pIsWindowVisible  = user32.NewProc("IsWindowVisible")
	pIsIconic         = user32.NewProc("IsIconic")
	pGetWindowTextW   = user32.NewProc("GetWindowTextW")
	pGetClassNameW    = user32.NewProc("GetClassNameW")
	pBringWindowToTop = user32.NewProc("BringWindowToTop")
)

// chromeWindowClass is the class of a Chromium browser's windows.
const chromeWindowClass = "Chrome_WidgetWin_1"

// trackByTitle: on Windows the window is found by its title (see
// window.go).
const trackByTitle = true

const swRestore = 9

// enum is EnumWindows' one callback (Windows allows a program only so
// many) and what it gathers; mu keeps one search at a time.
var enum struct {
	mu    sync.Mutex
	found []uintptr
	cb    uintptr
}

func windowString(proc *syscall.LazyProc, hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := proc.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func init() {
	enum.cb = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if windowString(pGetWindowTextW, hwnd) == AppName && windowString(pGetClassNameW, hwnd) == chromeWindowClass {
			enum.found = append(enum.found, hwnd)
		}
		return 1
	})
}

// appWindows are Axon's open windows: visible browser app windows
// titled exactly with Axon's name.
func appWindows() []uintptr {
	enum.mu.Lock()
	defer enum.mu.Unlock()
	enum.found = nil
	pEnumWindows.Call(enum.cb, 0)
	return enum.found
}

// raiseWindows brings the windows to the front, restored.
func raiseWindows(wins []uintptr) {
	for _, w := range wins {
		if min, _, _ := pIsIconic.Call(w); min != 0 {
			pShowWindow.Call(w, swRestore)
		}
		pBringWindowToTop.Call(w)
		pSetForegroundWindow.Call(w)
	}
}

// closeWindows asks the windows to close, as their close buttons
// would.
func closeWindows(wins []uintptr) {
	for _, w := range wins {
		pPostMessageW.Call(w, wmClose, 0, 0)
	}
}

// terminate is not needed here: the window is closed by its title.
func terminate(int) {}
