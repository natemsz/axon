package gui

import (
	"net/url"
	"syscall"
	"unsafe"
)

// The site panel's hooks into its WebView: a mark on every page it
// loads (window.__axonPanel), which tells the site to draw the
// panel's window controls in its own top bar, and the channel those
// controls talk back on (chrome.webview.postMessage, taken in
// panelMessage, overlay_windows.go).

var pCoTaskMemFree = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")

// takeString reads and frees a string WebView2 handed over.
func takeString(p uintptr) string {
	if p == 0 {
		return ""
	}
	defer pCoTaskMemFree.Call(p)
	var n int
	for *(*uint16)(unsafe.Add(sysPtr(&p), n*2)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(sysPtr(&p)), n))
}

func (w *webview) panelHooks(core uintptr) {
	// ICoreWebView2::AddScriptToExecuteOnDocumentCreated (27).
	js, _ := syscall.UTF16PtrFromString("window.__axonPanel = true;")
	added := newHandler(func(uintptr, uintptr) uintptr { return 0 })
	keep = append(keep, added)
	comCall(core, 27, uintptr(unsafe.Pointer(js)), uintptr(unsafe.Pointer(added)))
	// ICoreWebView2::add_WebMessageReceived (34); the handler's
	// Invoke is (sender, args): args' get_Source (3) and
	// get_WebMessageAsJson (4).
	got := newHandler(func(_, args uintptr) uintptr {
		var src, msg uintptr
		comCall(args, 3, uintptr(unsafe.Pointer(&src)))
		comCall(args, 4, uintptr(unsafe.Pointer(&msg)))
		w.panelMessage(takeString(src), takeString(msg))
		return 0
	})
	keep = append(keep, got)
	var token int64
	comCall(core, 34, uintptr(unsafe.Pointer(got)), uintptr(unsafe.Pointer(&token)))
}

// originOf is a page's site, "https://host/", for panelMessage's check.
func originOf(page string) string {
	u, err := url.Parse(page)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/"
}
