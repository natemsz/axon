// The Map window (intel.go): the intel threads on the map, drawn from
// the site's map payload in its improved projection (the game's own 2D
// layout), the way the game's map draws it: stars in their security
// colours, gates between them, names close up.
//
// It reads the threads itself. Every system with intel carries a ring
// and a label ("12 · 4m", "clear · 1h"); hovering a system lists its
// reports, and clicking it opens the newest. A thread opened anywhere
// centres its system with its neighbourhood around it (three jumps). A
// new report's system pings: one plain echo, five softer pulses, then
// a faint slow breath until it is opened or no longer recent. Drag to
// pan, wheel to zoom.
(function () {
  "use strict";
  const I = window.Intel;
  const canvas = document.getElementById("map");
  const ctx = canvas.getContext("2d");
  const status = document.getElementById("status");
  const regionEl = document.getElementById("region");
  const tip = document.getElementById("tip");
  const legend = document.getElementById("legend");

  // --- Layers ---
  // The site's live map layers, as its map draws them: security (the
  // stars' own colours), threat (the decayed kill score on the site's
  // scale), kills and NPC kills in the last hour, jumps in the last
  // hour, and sovereignty (each alliance its own hue). The choice is
  // remembered per browser.
  const LAYER_KEY = "axon.intel.layer";
  let layer = "security", L = null;
  try { layer = localStorage.getItem(LAYER_KEY) || "security"; } catch (_) { /* default */ }
  const THREAT = [{ at: 0, name: "Quiet" }, { at: 0.5, name: "Low" }, { at: 2, name: "Medium" }, { at: 5, name: "High" }, { at: 10, name: "Extreme" }];
  function threatName(v) { let n = THREAT[0].name; THREAT.forEach((t) => { if (v >= t.at) n = t.name; }); return n; }
  function heatT(v, max) { return v <= 0 || max <= 0 ? 0 : Math.log(1 + v) / Math.log(1 + max); }
  // The ramp: threat runs amber through red to white-hot; the counts
  // run dim amber to bright; jumps run cool blue.
  const RAMPS = {
    threat: [[214, 140, 40], [224, 50, 40], [255, 236, 210]],
    kills: [[150, 90, 40], [255, 122, 26], [255, 240, 200]],
    npc: [[110, 90, 60], [214, 170, 80], [255, 245, 215]],
    jumps: [[40, 70, 110], [78, 168, 222], [224, 244, 255]],
  };
  function rampColour(t, a) {
    const ramp = RAMPS[layer] || RAMPS.kills;
    t = Math.min(Math.max(t, 0), 1);
    const [lo, hi, core] = ramp;
    let c;
    if (t < 0.75) { const u = t / 0.75; c = lo.map((v, k) => v + (hi[k] - v) * u); }
    else { const w = (t - 0.75) / 0.25; c = hi.map((v, k) => v + (core[k] - v) * w); }
    return "rgba(" + c.map(Math.round).join(",") + "," + a + ")";
  }
  function joinLayers(Lp) {
    const n = G.n, J = { threat: new Float32Array(n), kills: new Int32Array(n), npc: new Int32Array(n), jumps: new Int32Array(n), sov: new Int16Array(n).fill(-1), alliances: Lp.alliances || [], max: {} };
    const each = (part, fn) => (part && part.id || []).forEach((id, j) => { const i = G.byID.get(id); if (i !== undefined) fn(i, j, part); });
    each(Lp.kills, (i, j, p) => { J.kills[i] = p.ship[j] + p.pod[j]; J.npc[i] = p.npc[j]; });
    each(Lp.jumps, (i, j, p) => { J.jumps[i] = p.n[j]; });
    each(Lp.sov, (i, j, p) => { J.sov[i] = p.a[j]; });
    each(Lp.threat, (i, j, p) => { J.threat[i] = p.s[j] / 100; });
    ["kills", "npc", "jumps"].forEach((k) => { let m = 0; for (let i = 0; i < n; i++) if (J[k][i] > m) m = J[k][i]; J.max[k] = m; });
    J.max.threat = 10;
    return J;
  }
  async function loadLayers() {
    if (!G || layer === "security") return;
    try { L = joinLayers(await I.api("/api/intel/layers")); draw(); showLegend(); }
    catch (e) { legend.textContent = e.message; legend.hidden = false; }
  }
  function heatOf(i) {
    if (!L || layer === "security" || layer === "sov") return 0;
    return heatT(L[layer][i], L.max[layer]);
  }
  function starColour(i, heat) {
    if (!L || layer === "security") return I.secColour(G.tier[i]);
    if (layer === "sov") {
      const a = L.sov[i];
      if (a < 0) return "rgba(120,125,130,0.55)";
      const al = L.alliances[a], hue = ((Math.imul((al && al.id) | 0, 2654435761) >>> 0) % 360);
      return "hsl(" + hue + ",55%,58%)";
    }
    return heat > 0 ? rampColour(heat, 1) : "rgba(120,125,130,0.5)";
  }
  function layerWords(i) {
    if (!L || layer === "security") return "";
    if (layer === "threat") return "Threat: " + threatName(L.threat[i]);
    if (layer === "sov") { const al = L.alliances[L.sov[i]]; return al ? "Held by " + al.name : "No alliance holds it"; }
    const v = L[layer][i];
    return v + " " + { kills: "kills", npc: "NPC kills", jumps: "jumps" }[layer] + " in the last hour";
  }
  function showLegend() {
    const words = { threat: "Threat: quiet to extreme", kills: "Kills, last hour", npc: "NPC kills, last hour", jumps: "Jumps, last hour", sov: "Sovereignty" }[layer];
    legend.textContent = words || "";
    legend.hidden = !words;
  }
  function setLayer(name) {
    layer = name;
    try { localStorage.setItem(LAYER_KEY, name); } catch (_) { /* this visit only */ }
    document.querySelectorAll("[data-layer]").forEach((b) => b.setAttribute("aria-checked", String(b.dataset.layer === name)));
    showLegend();
    if (name !== "security" && !L) loadLayers();
    draw();
  }
  document.querySelectorAll("[data-layer]").forEach((b) => b.addEventListener("click", () => setLayer(b.dataset.layer)));

  let P = null, G = null;
  let cam = { x: 0, y: 0, k: 1 };
  let cw = 0, ch = 0, dpr = 1;
  let sel = -1, hover = -1, pendingFocus = 0;
  let intel = new Map();   // system index -> its threads, newest first
  let recent = 1800;
  const pings = new Map();
  let raf = 0, pingTimer = 0;
  const reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // --- The payload ---
  function prepare(P) {
    const S = P.sys, n = S.id.length;
    const px = S.px && S.px.length === n ? S.px : S.x;
    const py = S.py && S.py.length === n ? S.py : S.y;
    const byID = new Map(), tier = new Uint8Array(n), adj = [];
    for (let i = 0; i < n; i++) { byID.set(S.id[i], i); tier[i] = I.secTier(S.s[i]); adj.push([]); }
    for (let g = 0; g < P.gates.length; g += 2) { adj[P.gates[g]].push(P.gates[g + 1]); adj[P.gates[g + 1]].push(P.gates[g]); }
    let all = [Infinity, Infinity, -Infinity, -Infinity];
    for (let i = 0; i < n; i++) all = [Math.min(all[0], px[i]), Math.min(all[1], py[i]), Math.max(all[2], px[i]), Math.max(all[3], py[i])];
    return { n, px, py, byID, tier, adj, all };
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
  // The neighbourhood of a system: everything within a few jumps.
  function around(i, jumps) {
    const seen = new Set([i]);
    let edge = [i];
    for (let j = 0; j < jumps; j++) {
      const next = [];
      edge.forEach((a) => G.adj[a].forEach((b) => { if (!seen.has(b)) { seen.add(b); next.push(b); } }));
      edge = next;
    }
    return seen;
  }
  function focus(sysID) {
    if (!G || !cw) { pendingFocus = sysID; return; }
    const i = G.byID.get(+sysID);
    if (i === undefined) return;
    sel = i;
    let b = [Infinity, Infinity, -Infinity, -Infinity];
    around(i, 3).forEach((s) => { b = [Math.min(b[0], G.px[s]), Math.min(b[1], G.py[s]), Math.max(b[2], G.px[s]), Math.max(b[3], G.py[s])]; });
    fitBox(b, 1.3);
    cam.x = G.px[i];
    cam.y = G.py[i];
    regionEl.textContent = P.regions[P.sys.r[i]].name;
    draw();
  }

  // --- Drawing ---
  function resize() {
    const r = canvas.getBoundingClientRect();
    // A hidden window measures nothing: keep the view as it was, so
    // it opens again where it was left.
    if (!r.width || !r.height) return;
    dpr = window.devicePixelRatio || 1;
    cw = r.width; ch = r.height;


    canvas.width = Math.round(cw * dpr);
    canvas.height = Math.round(ch * dpr);
    if (pendingFocus && G) { const f = pendingFocus; pendingFocus = 0; focus(f); }
    draw();
  }
  function draw() { if (!raf) raf = requestAnimationFrame(paint); }
  function rgba(hex, a) {
    const v = parseInt(hex.slice(1), 16);
    return "rgba(" + (v >> 16) + "," + ((v >> 8) & 255) + "," + (v & 255) + "," + a + ")";
  }
  // A label with a dark halo, so it reads over the gates and stars.
  function label(text, x, y, colour, font) {
    ctx.font = font;
    ctx.lineWidth = 3;
    ctx.strokeStyle = "rgba(8,10,12,0.9)";
    ctx.strokeText(text, x, y);
    ctx.fillStyle = colour;
    ctx.fillText(text, x, y);
  }
  // A thread's words on the map: "12 · 4m", "5 ships · 4m", "clear · 1h".
  function intelText(t) {
    const what = t.clear ? "clear" : t.n ? String(t.n) : t.count ? t.count + " " + t.noun + (t.count === 1 ? "" : "s") : "intel";
    return what + " · " + I.ago(t.at);
  }
  function paint() {
    raf = 0;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cw, ch);
    if (!G) return;
    const S = P.sys, inView = (x, y) => x > -30 && y > -30 && x < cw + 30 && y < ch + 30;
    // Gates.
    ctx.lineWidth = 1;
    for (let g = 0; g < P.gates.length; g += 2) {
      const a = P.gates[g], b = P.gates[g + 1];
      const ax = sx(a), ay = sy(a), bx = sx(b), by = sy(b);
      if (!inView(ax, ay) && !inView(bx, by)) continue;
      ctx.strokeStyle = S.r[a] !== S.r[b] ? "rgba(255,255,255,0.12)" : "rgba(255,255,255,0.22)";
      ctx.beginPath();
      ctx.moveTo(ax, ay);
      ctx.lineTo(bx, by);
      ctx.stroke();
    }
    // Stars: the security colour, each with a dark edge so neighbours
    // never run together.
    const r = Math.max(2.6, Math.min(5, 2 + cam.k * 0.5));
    let shown = 0;
    for (let i = 0; i < G.n; i++) {
      const x = sx(i), y = sy(i);
      if (!inView(x, y)) continue;
      shown++;
      const heat = heatOf(i);
      if (heat > 0) {
        // A live layer's heat: a soft glow under the star.
        ctx.beginPath();
        ctx.arc(x, y, r + 3 + heat * 11, 0, Math.PI * 2);
        ctx.fillStyle = rampColour(heat, 0.22 + heat * 0.25);
        ctx.fill();
      }
      ctx.beginPath();
      ctx.arc(x, y, r, 0, Math.PI * 2);
      ctx.fillStyle = starColour(i, heat);
      ctx.fill();
      ctx.lineWidth = 1.2;
      ctx.strokeStyle = "rgba(6,8,10,0.95)";
      ctx.stroke();
    }
    ctx.textAlign = "left";
    ctx.textBaseline = "middle";
    // Names, once few enough systems are in view to read them all.
    if (shown <= 110) {
      for (let i = 0; i < G.n; i++) {
        const x = sx(i), y = sy(i);
        if (!inView(x, y)) continue;
        const lit = i === sel || i === hover || intel.has(i);
        label(S.n[i], x + r + (intel.has(i) ? 10 : 5), y, lit ? "#f2f3f4" : "rgba(214,216,218,0.62)", (lit ? "600 " : "") + "12px 'Saira Semi Condensed', 'Segoe UI', sans-serif");
      }
    }
    // Intel: a ring on every system with a thread, and its words.
    intel.forEach((ts, i) => {
      const x = sx(i), y = sy(i);
      if (!inView(x, y)) return;
      const t = ts[0], calm = !!t.clear;
      const colour = calm ? "#4b8fe0" : "#e0473b";
      ctx.lineWidth = i === sel ? 2.2 : 1.6;
      ctx.strokeStyle = i === sel ? "#f2f3f4" : rgba(colour, 0.95);
      ctx.beginPath();
      ctx.arc(x, y, r + 5, 0, Math.PI * 2);
      ctx.stroke();
      label(intelText(t), x + r + 10, y + 15, calm ? "#8fb8ee" : "#f08a80", "600 12.5px 'Saira Semi Condensed', 'Segoe UI', sans-serif");
    });
    if (sel >= 0 && !intel.has(sel)) {
      ctx.lineWidth = 2;
      ctx.strokeStyle = "#f2f3f4";
      ctx.beginPath();
      ctx.arc(sx(sel), sy(sel), r + 5, 0, Math.PI * 2);
      ctx.stroke();
    }
    paintPings(r);
  }

  // --- Pings ---
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
      const g = ring(now - p.t0);
      ctx.strokeStyle = rgba("#e0473b", g.alpha);
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.arc(sx(p.i), sy(p.i), r + 5 + g.size * g.grow, 0, Math.PI * 2);
      ctx.stroke();
    });
    if (live && !reduceMotion) pingTimer = setTimeout(draw, fast ? 16 : 80);
  }
  function ping(sys, id, at, fresh) {
    const i = G && G.byID.get(+sys);
    if (i === undefined) return;
    const now = Date.now();
    pings.set(id, { i, id, at: at || now / 1000, recent, t0: fresh ? now : now - SETTLE, viewed: false });
    draw();
  }

  // --- The threads, read here ---
  const SEEN_KEY = "axon.intel.seen";
  function seen() { try { return JSON.parse(localStorage.getItem(SEEN_KEY) || "{}") || {}; } catch (_) { return {}; } }
  let known = null;
  async function loadThreads() {
    if (!G) return;
    try {
      const out = await I.api("/api/intel/threads");
      recent = out.recent || recent;
      const list = out.threads || [];
      const next = new Map();
      list.forEach((t) => {
        const i = G.byID.get(t.sys);
        if (i === undefined) return;
        if (!next.has(i)) next.set(i, []);
        next.get(i).push(t);
      });
      intel = next;
      const s = seen(), now = Date.now() / 1000;
      list.forEach((t) => {
        const changed = known && known[t.id] !== t.report;
        if (now - t.at < recent && s[t.id] !== t.report) ping(t.sys, t.id, t.at, !!changed);
      });
      known = {};
      list.forEach((t) => { known[t.id] = t.report; });
      if (sel < 0 && list.length) focus(list[0].sys);
      draw();
    } catch (e) {
      status.textContent = e.message;
      status.hidden = false;
    }
  }

  // --- Following the other windows ---
  I.on((m) => {
    if (m.type === "open") {
      focus(m.sys);
      pings.forEach((p) => { if (p.id === m.thread) p.viewed = true; });
    } else if (m.type === "ping" && G) {
      ping(m.sys, m.id, m.at, m.fresh);
    }
  });

  // --- Hover, click, pan, zoom ---
  function nearest(mx, my) {
    let best = -1, bd = 14 * 14;
    for (let i = 0; i < G.n; i++) {
      const dx = sx(i) - mx, dy = sy(i) - my, d = dx * dx + dy * dy;
      if (d < bd) { bd = d; best = i; }
    }
    return best;
  }
  function showTip(i, mx, my) {
    tip.textContent = "";
    if (i < 0) { tip.hidden = true; return; }
    const head = I.el("div", "tip-head");
    head.appendChild(I.secEl(I.secText(P.sys.s[i]), G.tier[i]));
    head.appendChild(I.el("span", "sys", P.sys.n[i]));
    tip.appendChild(head);
    const ts = intel.get(i) || [];
    const lw = layerWords(i);
    if (lw) tip.appendChild(I.el("div", "tip-layer", lw));
    if (!ts.length) tip.appendChild(I.el("div", "tip-none", "No reports"));
    ts.slice(0, 5).forEach((t) => {
      const row = I.el("div", "tip-row");
      row.appendChild(I.el("span", "n" + (t.clear ? "" : " hostile"), intelText(t)));
      row.appendChild(I.el("span", "tip-sub", t.kind + (t.by ? " by " + t.by : "")));
      tip.appendChild(row);
    });
    tip.hidden = false;
    const x = Math.min(mx + 14, cw - tip.offsetWidth - 6), y = Math.min(my + 14, ch - tip.offsetHeight - 6);
    tip.style.left = Math.max(4, x) + "px";
    tip.style.top = Math.max(4, y) + "px";
  }
  let drag = null, moved = false;
  canvas.addEventListener("mousedown", (e) => {
    if (e.button !== 0) return;
    drag = { x: e.clientX, y: e.clientY, cx: cam.x, cy: cam.y };
    moved = false;
    canvas.classList.add("dragging");
  });
  window.addEventListener("mousemove", (e) => {
    const r = canvas.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top;
    if (drag) {
      if (Math.abs(e.clientX - drag.x) + Math.abs(e.clientY - drag.y) > 3) moved = true;
      cam.x = drag.cx - (e.clientX - drag.x) / cam.k;
      cam.y = drag.cy - (e.clientY - drag.y) / cam.k;
      tip.hidden = true;
      draw();
      return;
    }
    if (!G || e.target !== canvas) return;
    const i = nearest(mx, my);
    if (i !== hover) { hover = i; draw(); }
    showTip(i, mx, my);
  });
  window.addEventListener("mouseup", (e) => {
    if (drag && !moved && G) {
      const r = canvas.getBoundingClientRect();
      const i = nearest(e.clientX - r.left, e.clientY - r.top);
      const ts = intel.get(i);
      if (ts && ts.length) I.openReport({ report: ts[0].report, thread: ts[0].id, sys: ts[0].sys });
    }
    drag = null;
    canvas.classList.remove("dragging");
  });
  canvas.addEventListener("mouseleave", () => { hover = -1; tip.hidden = true; draw(); });
  canvas.addEventListener("wheel", (e) => {
    e.preventDefault();
    const r = canvas.getBoundingClientRect();
    const mx = e.clientX - r.left - cw / 2, my = e.clientY - r.top - ch / 2;
    const wx = cam.x + mx / cam.k, wy = cam.y + my / cam.k;
    cam.k *= Math.exp(-e.deltaY * 0.0015);
    cam.x = wx - mx / cam.k;
    cam.y = wy - my / cam.k;
    tip.hidden = true;
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
    if (pendingFocus) { const f = pendingFocus; pendingFocus = 0; focus(f); }
    I.every(10000, loadThreads);
    I.every(180000, loadLayers);
    setLayer(layer);
  }).catch((e) => { status.textContent = "The map could not be loaded: " + e.message; });
})();
