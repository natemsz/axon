// The intel windows' shared part (intel.go): the key every call
// carries, the window's own frame (drag, hide), the bus the windows
// talk on, and the small helpers every page uses. Loaded first by
// each page; the page's own script reads window.Intel.
(function () {
  "use strict";
  let key = new URLSearchParams(location.search).get("k");
  try {
    if (key) sessionStorage.setItem("axon.key", key);
    else key = sessionStorage.getItem("axon.key");
  } catch (_) { /* the key in the address is enough */ }
  const module = document.body.dataset.module;

  async function api(path, body) {
    const opts = { method: body === undefined ? "GET" : "POST", headers: { "X-Axon-Key": key || "" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(path, opts);
    const out = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(out.error || "Axon did not answer (" + res.status + ")");
    return out;
  }

  // The frame: the title strip moves the window, x or Esc puts it away.
  document.addEventListener("mousedown", (e) => {
    if (e.button !== 0 || !e.target.closest(".win-head") || e.target.closest("button")) return;
    e.preventDefault();
    api("/api/intel/window", { module, cmd: "drag" }).catch(() => {});
  });
  document.addEventListener("click", (e) => {
    if (e.target.closest("[data-close]")) api("/api/intel/window", { module, cmd: "hide" }).catch(() => {});
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") api("/api/intel/window", { module, cmd: "hide" }).catch(() => {});
  });

  // The bus: what one window does, the others follow.
  //   {type:"open", report, thread, sys}   a thread or report opened
  //   {type:"threads", rows}               the threads as they stand
  //   {type:"ping", sys, id, at}           a new report arrived
  const bus = "BroadcastChannel" in window ? new BroadcastChannel("axon-intel") : null;
  function send(msg) { if (bus) bus.postMessage(msg); }
  function on(fn) { if (bus) bus.addEventListener("message", (e) => fn(e.data || {})); }

  // Opening a report: the Report window shows it, and the map moves.
  function openReport(r) {
    send({ type: "open", report: r.report, thread: r.thread, sys: r.sys });
    api("/api/intel/open", { module: "report", report: r.report }).catch(() => {});
  }

  // Security as the game shows it, in its own colours.
  const SEC_COLOURS = ["#f00000", "#d73000", "#f04800", "#f06000", "#d77700", "#efef00", "#8fef2f", "#00f000", "#00ef47", "#48f0c0", "#2fefef"];
  function secRounded(s) { let r = Math.round(s * 10) / 10; if (s > 0 && s < 0.05) r = 0.1; return r; }
  function secTier(s) { const r = secRounded(s); return r <= 0 ? 0 : Math.round(r * 10); }
  function secText(s) { const r = secRounded(s); return (r === 0 ? 0 : r).toFixed(1); }
  function secColour(tier) { return SEC_COLOURS[Math.max(0, Math.min(10, tier | 0))]; }
  function secEl(text, tier) {
    const e = document.createElement("span");
    e.className = "sec";
    e.textContent = text;
    e.style.color = secColour(tier);
    return e;
  }

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = text;
    return e;
  }
  function eveTime(sec) {
    const d = new Date(sec * 1000);
    return String(d.getUTCHours()).padStart(2, "0") + ":" + String(d.getUTCMinutes()).padStart(2, "0");
  }
  function ago(sec) {
    const m = Math.max(0, Math.round((Date.now() / 1000 - sec) / 60));
    if (m < 1) return "now";
    if (m < 60) return m + "m";
    const h = Math.floor(m / 60);
    return h < 24 ? h + "h" : Math.floor(h / 24) + "d";
  }
  // every runs fn now, and on a period while the window is showing.
  function every(ms, fn) {
    fn();
    setInterval(() => { if (document.visibilityState === "visible") fn(); }, ms);
    document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") fn(); });
  }

  // The quick options every window wears (the dock sets them):
  // transparency, locked in place, configure mode. They live in the
  // windows' shared storage and travel on the bus.
  const PREFS_KEY = "axon.intel.prefs";
  function prefs() {
    let p = {};
    try { p = JSON.parse(localStorage.getItem(PREFS_KEY) || "{}") || {}; } catch (_) { /* defaults */ }
    return { alpha: typeof p.alpha === "number" ? p.alpha : 0.72, locked: !!p.locked, configure: !!p.configure };
  }
  function applyPrefs(p) {
    const root = document.documentElement;
    root.style.setProperty("--alpha", String(Math.max(0.2, Math.min(0.98, p.alpha))));
    root.classList.toggle("locked", !!p.locked);
    root.classList.toggle("configure", !!p.configure);
  }
  function setPrefs(patch) {
    const p = Object.assign(prefs(), patch);
    try { localStorage.setItem(PREFS_KEY, JSON.stringify(p)); } catch (_) { /* still applied */ }
    applyPrefs(p);
    send({ type: "prefs", prefs: p });
  }
  applyPrefs(prefs());
  on((m) => { if (m.type === "prefs" && m.prefs) applyPrefs(m.prefs); });

  // The dock lights a window's button while it is open.
  function presence() {
    if (module !== "dock") send({ type: document.visibilityState === "visible" ? "shown" : "gone", module });
  }
  document.addEventListener("visibilitychange", presence);
  on((m) => { if (m.type === "who" && document.visibilityState === "visible") presence(); });
  presence();

  window.Intel = { module, api, send, on, openReport, secTier, secText, secColour, secEl, el, eveTime, ago, every, prefs, setPrefs };
})();
