// The overlay's page (overlay.html, overlay.go). Enter sends; with the
// field empty, Axon reads the clipboard at that moment and sends it.
// Text in the field is read before Send (the kind here, the system by
// the site), and the player may override either. Every call carries
// the key the window was opened with, as the settings page's do.
(function () {
  "use strict";
  let key = new URLSearchParams(location.search).get("k");
  try {
    if (key) sessionStorage.setItem("axon.key", key);
    else key = sessionStorage.getItem("axon.key");
  } catch (_) { /* storage blocked: the key in the address is enough */ }

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

  const $ = (id) => document.getElementById(id);
  const paste = $("paste"), send = $("send"), status = $("status");
  const chips = $("chips"), kind = $("kind"), kindAuto = $("kind-auto"), system = $("system"), from = $("from");
  const KIND_NAMES = { dscan: "D-Scan", local: "Local", fleet: "Fleet", probe: "Probe scan", chat: "Intel line" };
  const FROM_WORDS = { scan: "from its celestials", line: "named in the line", speaker: "where the speaker is", poster: "where you are" };

  // --- The status line ---
  let idle = "";
  function say(text, tone) {
    status.textContent = text;
    status.className = "ov-status" + (tone ? " " + tone : "");
  }
  function sayIdle() { say(idle); }
  async function loadState() {
    try {
      const st = await api("/api/overlay/state");
      if (st.problem) { idle = st.problem; say(idle, "bad"); return; }
      idle = "To " + (st.channel || "your channel") + (st.submit ? " · " + st.submit + " brings this back" : "") + " · Esc returns to the game";
      sayIdle();
    } catch (e) { say(e.message, "bad"); }
  }

  // --- What the text is, before Send ---
  let previewSeq = 0, previewTimer = 0;
  function resetChips() {
    chips.hidden = true;
    kind.value = "";
    kindAuto.textContent = "Auto";
    system.value = "";
    system.placeholder = "Auto";
    from.textContent = "";
  }
  function schedulePreview() {
    clearTimeout(previewTimer);
    if (!paste.value.trim()) { resetChips(); return; }
    chips.hidden = false;
    previewTimer = setTimeout(preview, 350);
  }
  async function preview() {
    const n = ++previewSeq;
    try {
      const pv = await api("/api/overlay/preview", { text: paste.value });
      if (n !== previewSeq) return;
      kindAuto.textContent = pv.kind ? "Auto: " + (KIND_NAMES[pv.kind] || pv.kind) : "Auto";
      system.placeholder = pv.system_name ? pv.system_name : "Not found";
      from.textContent = pv.system_name ? FROM_WORDS[pv.system_from] || "" : (pv.note || "type the system to file it");
    } catch (_) { /* the chips keep Auto; Send still works */ }
  }

  // --- Send ---
  let sending = false;
  async function doSend() {
    if (sending) return;
    sending = true;
    send.disabled = true;
    const empty = !paste.value.trim();
    say(empty ? "Sending what you copied…" : "Sending…");
    try {
      const body = { text: paste.value, kind: kind.value };
      const sys = system.value.trim();
      if (sys) body.system_name = sys;
      const out = await api("/api/overlay/send", body);
      paste.value = "";
      resetChips();
      say(out.said || "Sent.", "ok");
      // Sent: the game gets the keyboard back, the bar stays put.
      api("/api/overlay/release", {}).catch(() => {});
      setTimeout(() => { if (status.classList.contains("ok")) sayIdle(); }, 6000);
    } catch (e) {
      say("Not sent: " + e.message, "bad");
    } finally {
      sending = false;
      send.disabled = false;
    }
  }

  send.addEventListener("click", doSend);
  paste.addEventListener("input", schedulePreview);
  document.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.isComposing &&
        (e.target === paste || e.target === document.body)) {
      e.preventDefault();
      doSend();
    } else if (e.key === "Escape") {
      e.preventDefault();
      api("/api/overlay/release", {}).catch(() => {});
    }
  });
  $("hide").addEventListener("click", () => api("/api/overlay/hide", {}).catch(() => {}));

  // Moving the bar: from the grip, or any bare edge of the panel.
  $("ov").addEventListener("mousedown", (e) => {
    if (e.button !== 0 || (e.target !== e.currentTarget && e.target.id !== "grip" && !e.target.classList.contains("ov-row"))) return;
    e.preventDefault();
    api("/api/overlay/drag", {}).catch(() => {});
  });

  // Called up by the shortcut: the field has the keyboard, and the
  // status line says where a send goes, read fresh.
  window.addEventListener("focus", () => { paste.focus(); loadState(); });
  loadState();
  paste.focus();
})();
