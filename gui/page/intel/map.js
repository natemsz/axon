// The Map window (intel.go): the systems the intel threads are in,
// drawn from the site's map payload in its improved projection (the
// game's own 2D layout), the way the game's map draws them: stars in
// their security colours, the gates between them, names close up.
//
// It follows the other windows: a thread or report opened anywhere
// centres its system with its region around it; every thread's system
// carries a ring; a new report's system pings, one plain echo, five
// softer pulses, then a faint slow breath until it is opened or no
// longer recent. Drag to pan, wheel to zoom.
(function () {
  "use strict";
  const I = window.Intel;
  const canvas = document.getElementById("map");
  const ctx = canvas.getContext("2d");
  const status = document.getElementById("status");
  const regionEl = document.getElementById("region");

  let P = null, G = null;               // the payload, and what is derived from it
  let cam = { x: 0, y: 0, k: 1 };       // world point at the centre, pixels per unit
  let cw = 0, ch = 0, dpr = 1;
  let sel = -1, marks = new Set(), pings = new Map();
  let raf = 0, pingTimer = 0;
  const reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // --- The payload ---
  function prepare(P) {
    const S = P.sys, n = S.id.length;
    const px = S.px && S.px.length === n ? S.px : S.x;
    const py = S.py && S.py.length === n ? S.py : S.y;
    const byID = new Map(), tier = new Uint8Array(n);
    for (let i = 0; i < n; i++) { byID.set(S.id[i], i); tier[i] = I.secTier(S.s[i]); }
    const R = P.regions.length, bounds = [];
    for (let r = 0; r < R; r++) bounds.push([Infinity, Infinity, -Infinity, -Infinity]);
    for (let i = 0; i < n; i++) {
      const b = bounds[S.r[i]];
      if (px[i] < b[0]) b[0] = px[i];
      if (py[i] < b[1]) b[1] = py[i];
      if (px[i] > b[2]) b[2] = px[i];
      if (py[i] > b[3]) b[3] = py[i];
    }
    let all = [Infinity, Infinity, -Infinity, -Infinity];
    bounds.forEach((b) => { all = [Math.min(all[0], b[0]), Math.min(all[1], b[1]), Math.max(all[2], b[2]), Math.max(all[3], b[3])]; });
    return { n, px, py, byID, tier, bounds, all };
  }

  // --- The camera ---
  function sx(i) { return (G.px[i] - cam.x) * cam.k + cw / 2; }
  function sy(i) { return (G.py[i] - cam.y) * cam.k + ch / 2; }
  function fitBox(b, pad) {
    const w = Math.max(b[2] - b[0], 1), h = Math.max(b[3] - b[1], 1);
    cam.x = (b[0] + b[2]) / 2;
    cam.y = (b[1] + b[3]) / 2;
    cam.k = Math.min(cw / (w * pad), ch / (h * pad));
  }
  function focus(sysID) {
    if (!G) { pendingFocus = sysID; return; }
    const i = G.byID.get(+sysID);
    if (i === undefined) return;
    sel = i;
    fitBox(G.bounds[P.sys.r[i]], 1.25);
    // The system itself stays well inside the view.
    cam.x = cam.x * 0.4 + G.px[i] * 0.6;
    cam.y = cam.y * 0.4 + G.py[i] * 0.6;
    regionEl.textContent = P.regions[P.sys.r[i]].name;
    draw();
  }
  let pendingFocus = 0;

  // --- Drawing ---
  function resize() {
    const r = canvas.getBoundingClientRect();
    dpr = window.devicePixelRatio || 1;
    cw = r.width; ch = r.height;
    canvas.width = Math.round(cw * dpr);
    canvas.height = Math.round(ch * dpr);
    draw();
  }
  function draw() {
    if (raf) return;
    raf = requestAnimationFrame(paint);
  }
  function rgba(hex, a) {
    const v = parseInt(hex.slice(1), 16);
    return "rgba(" + (v >> 16) + "," + ((v >> 8) & 255) + "," + (v & 255) + "," + a + ")";
  }
  function paint() {
    raf = 0;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cw, ch);
    if (!G) return;
    const S = P.sys, inView = (x, y) => x > -20 && y > -20 && x < cw + 20 && y < ch + 20;
    // Gates: hairlines, a touch of the lower end's colour.
    ctx.lineWidth = 1;
    for (let g = 0; g < P.gates.length; g += 2) {
      const a = P.gates[g], b = P.gates[g + 1];
      const ax = sx(a), ay = sy(a), bx = sx(b), by = sy(b);
      if (!inView(ax, ay) && !inView(bx, by)) continue;
      ctx.strokeStyle = S.r[a] !== S.r[b] ? "rgba(255,255,255,0.10)" : "rgba(255,255,255,0.16)";
      ctx.beginPath();
      ctx.moveTo(ax, ay);
      ctx.lineTo(bx, by);
      ctx.stroke();
    }
    // Stars: their security colour, a little larger as the view
    // closes in.
    const r = Math.max(2.2, Math.min(4.5, 1.5 + cam.k * 0.6));
    let shown = 0;
    for (let i = 0; i < G.n; i++) {
      const x = sx(i), y = sy(i);
      if (!inView(x, y)) continue;
      shown++;
      ctx.fillStyle = I.secColour(G.tier[i]);
      ctx.beginPath();
      ctx.arc(x, y, r, 0, Math.PI * 2);
      ctx.fill();
    }
    // Names, once few enough systems are in view to read them all.
    if (shown <= 90) {
      ctx.font = "11px 'Segoe UI', Tahoma, sans-serif";
      ctx.textAlign = "left";
      ctx.textBaseline = "middle";
      for (let i = 0; i < G.n; i++) {
        const x = sx(i), y = sy(i);
        if (!inView(x, y)) continue;
        const important = i === sel || marks.has(i);
        ctx.fillStyle = important ? "rgba(242,243,244,0.95)" : "rgba(214,216,218,0.55)";
        ctx.fillText(S.n[i], x + r + (important ? 9 : 4), y);
      }
    }
    // Every thread's system: a steady ring; the open one brighter.
    marks.forEach((i) => {
      const x = sx(i), y = sy(i);
      if (!inView(x, y)) return;
      ctx.strokeStyle = i === sel ? "rgba(242,243,244,0.95)" : "rgba(224,71,59,0.75)";
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(x, y, r + 5, 0, Math.PI * 2);
      ctx.stroke();
    });
    if (sel >= 0 && !marks.has(sel)) {
      ctx.strokeStyle = "rgba(242,243,244,0.95)";
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(sx(sel), sy(sel), r + 5, 0, Math.PI * 2);
      ctx.stroke();
    }
    paintPings(r);
  }

  // --- Pings ---
  // One plain echo, five softer pulses, then a faint slow breath.
  const ECHO = 1600, PULSE = 1800, PULSES = 5, BREATH = 6000, SETTLE = ECHO + PULSES * PULSE;
  function ring(t) {
    if (reduceMotion) return { size: 6, grow: 1, alpha: 0.35 };
    if (t < ECHO) { const g = t / ECHO; return { size: 26, grow: g, alpha: 0.9 * (1 - g) }; }
    if (t < SETTLE) { const g = ((t - ECHO) % PULSE) / PULSE; return { size: 14, grow: g, alpha: 0.45 * (1 - g) }; }
    const g = ((t - SETTLE) % BREATH) / BREATH;
    return { size: 10, grow: g, alpha: 0.16 * (1 - g) };
  }
  function paintPings(r) {
    clearTimeout(pingTimer);
    const now = Date.now();
    let live = false, fast = false;
    pings.forEach((p, key) => {
      if (p.viewed || now / 1000 - p.at > p.recent) { pings.delete(key); return; }
      live = true;
      if (now - p.t0 < SETTLE) fast = true;
      const g = ring(now - p.t0), x = sx(p.i), y = sy(p.i);
      ctx.strokeStyle = rgba("#e0473b", g.alpha);
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.arc(x, y, r + 3 + g.size * g.grow, 0, Math.PI * 2);
      ctx.stroke();
    });
    if (live && !reduceMotion) pingTimer = setTimeout(draw, fast ? 16 : 80);
  }

  // --- Following the other windows ---
  I.on((m) => {
    if (!G) { if (m.type === "open" || m.type === "focus") pendingFocus = m.sys; return; }
    if (m.type === "open") {
      focus(m.sys);
      pings.forEach((p) => { if (p.id === m.thread) p.viewed = true; });
    } else if (m.type === "focus") {
      if (sel < 0) focus(m.sys);
    } else if (m.type === "threads") {
      marks = new Set();
      (m.rows || []).forEach((t) => { const i = G.byID.get(t.sys); if (i !== undefined) marks.add(i); });
      // Opened after the others: centre the open thread, or the newest.
      if (sel < 0 && m.rows && m.rows.length) {
        const t = m.rows.find((r) => r.id === m.open) || m.rows[0];
        focus(t.sys);
      }
      draw();
    } else if (m.type === "ping") {
      const i = G.byID.get(m.sys);
      if (i === undefined) return;
      const now = Date.now();
      pings.set(m.id, { i, id: m.id, at: m.at || now / 1000, recent: m.recent || 1800, t0: m.fresh ? now : now - SETTLE, viewed: false });
      draw();
    }
  });

  // --- Pan and zoom ---
  let drag = null;
  canvas.addEventListener("mousedown", (e) => {
    if (e.button !== 0) return;
    drag = { x: e.clientX, y: e.clientY, cx: cam.x, cy: cam.y };
    canvas.classList.add("dragging");
  });
  window.addEventListener("mousemove", (e) => {
    if (!drag) return;
    cam.x = drag.cx - (e.clientX - drag.x) / cam.k;
    cam.y = drag.cy - (e.clientY - drag.y) / cam.k;
    draw();
  });
  window.addEventListener("mouseup", () => { drag = null; canvas.classList.remove("dragging"); });
  canvas.addEventListener("wheel", (e) => {
    e.preventDefault();
    const r = canvas.getBoundingClientRect();
    const mx = e.clientX - r.left - cw / 2, my = e.clientY - r.top - ch / 2;
    const wx = cam.x + mx / cam.k, wy = cam.y + my / cam.k;
    cam.k *= Math.exp(-e.deltaY * 0.0015);
    cam.x = wx - mx / cam.k;
    cam.y = wy - my / cam.k;
    draw();
  }, { passive: false });

  // --- Start ---
  new ResizeObserver(resize).observe(canvas);
  I.api("/api/intel/universe").then((payload) => {
    P = payload;
    G = prepare(P);
    status.hidden = true;
    resize();
    fitBox(G.all, 1.05);
    if (pendingFocus) focus(pendingFocus);
    draw();
    // Ask the Threads window for what to mark.
    I.send({ type: "hello" });
  }).catch((e) => { status.textContent = "The map could not be loaded: " + e.message; });
})();
