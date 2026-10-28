// The dock (intel.go): opens and puts away each intel window, the
// overlay and the site panel; its quick options set every window's
// transparency, lock them in place, and choose whether they hide while
// EVE is minimised. A button's tooltip is its name and its shortcut.
(function () {
  "use strict";
  const I = window.Intel;
  const BAR_W = 330, BAR_H = 44, OPEN_H = 220;
  const opts = document.getElementById("opts");
  const configure = document.getElementById("configure");

  // Tooltips: the name, and the shortcut as it is bound now.
  async function tips() {
    let keys = {};
    try { keys = await I.api("/api/intel/keys"); } catch (_) { /* names alone */ }
    document.querySelectorAll(".dock-btn").forEach((b) => {
      const k = b.dataset.key ? keys[b.dataset.key] : "";
      b.title = b.dataset.name + (k ? "  " + k : "");
      b.setAttribute("aria-label", b.dataset.name);
    });
  }

  // A window's button is lit while it is open.
  const open = new Set();
  function light() {
    document.querySelectorAll("[data-open]").forEach((b) => b.classList.toggle("on", open.has(b.dataset.open)));
  }
  I.on((m) => {
    if (m.type === "shown") { open.add(m.module); light(); }
    if (m.type === "gone") { open.delete(m.module); light(); }
  });
  I.send({ type: "who" }); // the open windows answer with "shown"

  document.addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (!b) return;
    if (b.dataset.open) I.api("/api/intel/open", { module: b.dataset.open, toggle: true }).catch(() => {});
    else if (b.dataset.do) I.api("/api/intel/action", { do: b.dataset.do }).catch(() => {});
  });

  // The quick options open above the bar: the dock grows upward, and
  // every window shows its extent while they are open (configure mode).
  configure.addEventListener("click", () => {
    const on = opts.hidden;
    opts.hidden = !on;
    configure.setAttribute("aria-expanded", String(on));
    I.api("/api/intel/window", { module: "dock", cmd: "size", w: BAR_W, h: on ? OPEN_H : BAR_H }).catch(() => {});
    I.setPrefs({ configure: on });
  });

  // Transparency: every window's panel, at once.
  const alpha = document.getElementById("alpha"), alphaV = document.getElementById("alpha-v");
  // The slider says how see-through the windows are: 100 less the
  // panel's opacity.
  alpha.value = String(Math.round((1 - I.prefs().alpha) * 100));
  alphaV.textContent = alpha.value + "%";
  alpha.addEventListener("input", () => {
    alphaV.textContent = alpha.value + "%";
    I.setPrefs({ alpha: 1 - +alpha.value / 100 });
  });

  // Lock and follow-the-game are Axon's own: it acts on them.
  const locked = document.getElementById("locked"), follow = document.getElementById("follow");
  I.api("/api/intel/prefs").then((p) => { locked.checked = !!p.locked; follow.checked = !!p.follow_eve; }).catch(() => {});
  locked.addEventListener("change", () => {
    I.api("/api/intel/prefs", { locked: locked.checked }).catch(() => {});
    I.setPrefs({ locked: locked.checked });
  });
  follow.addEventListener("change", () => I.api("/api/intel/prefs", { follow_eve: follow.checked }).catch(() => {}));

  // The grip moves the dock (the bar has no title strip).
  document.querySelector(".dock-grip").addEventListener("mousedown", (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    I.api("/api/intel/window", { module: "dock", cmd: "drag" }).catch(() => {});
  });

  tips();
  window.addEventListener("focus", tips);
})();
