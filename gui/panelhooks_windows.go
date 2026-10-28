package gui

import (
	"syscall"
	"unsafe"
)

// The site panel's hooks into its WebView: a mark on every page it
// loads (window.__axonPanel), which tells the site to draw the
// panel's window controls in its own top bar, and the channel those
// controls talk back on (chrome.webview.postMessage, taken in
// panelMessage, overlay_windows.go). A page with no such top bar (the
// site's front page and sign-in, EVE's login) gets the same controls
// from Axon, in its corner, coloured by the site's theme where the
// page has one, so the panel can always be moved and closed.

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

// panelScript runs first on every page the panel loads. The fallback
// controls live in a closed shadow root with a constructed sheet, so
// the page's styles and its CSP leave them alone; the site's theme
// tokens still reach them.
const panelScript = `window.__axonPanel = true;
(function () {
  function mount() {
    if (document.querySelector("[data-axon-winctl]") || !window.chrome || !window.chrome.webview) return;
    var host = document.createElement("div");
    var root = host.attachShadow({ mode: "closed" });
    var css = new CSSStyleSheet();
    css.replaceSync(
      ":host{position:fixed;top:0;right:0;z-index:2147483647;display:flex;height:32px;" +
      "background:var(--glass,rgba(12,14,17,.86));border:1px solid var(--glass-edge,rgba(255,255,255,.08));" +
      "border-top:0;border-right:0;color:var(--muted,#9aa3ad);user-select:none}" +
      ".grip{width:26px;cursor:grab;opacity:.5;background:radial-gradient(circle,currentColor 1px,transparent 1.4px) 0 0/4px 4px}" +
      "button{width:40px;padding:0;border:0;color:inherit;background:none;cursor:pointer;display:grid;place-items:center}" +
      "button:hover{color:var(--text,#e6e9ec);background:var(--row-hover,rgba(255,255,255,.08))}" +
      ".x:hover{color:#fff;background:var(--danger,#c42b1c)}" +
      "svg{width:10px;height:10px;fill:none;stroke:currentColor;stroke-width:1.2}");
    root.adoptedStyleSheets = [css];
    root.innerHTML =
      '<div class="grip" title="Move"></div>' +
      '<button data-axon="minimize" title="Minimize" aria-label="Minimize"><svg viewBox="0 0 10 10"><path d="M1.5 5.5h7"/></svg></button>' +
      '<button data-axon="maximize" title="Maximize" aria-label="Maximize"><svg viewBox="0 0 10 10"><rect x="1.5" y="1.5" width="7" height="7"/></svg></button>' +
      '<button data-axon="close" class="x" title="Close" aria-label="Close"><svg viewBox="0 0 10 10"><path d="M2 2l6 6M8 2l-6 6"/></svg></button>';
    function tell(what) { window.chrome.webview.postMessage({ axon: what }); }
    root.addEventListener("click", function (e) {
      var b = e.target.closest("button");
      if (b) tell(b.getAttribute("data-axon"));
    });
    var grip = root.querySelector(".grip");
    grip.addEventListener("mousedown", function (e) {
      if (e.button !== 0) return;
      e.preventDefault();
      tell("drag");
    });
    grip.addEventListener("dblclick", function () { tell("maximize"); });
    document.documentElement.appendChild(host);
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", mount);
  else mount();
})();`

func (w *webview) panelHooks(core uintptr) {
	// ICoreWebView2::AddScriptToExecuteOnDocumentCreated (27).
	js, _ := syscall.UTF16PtrFromString(panelScript)
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
