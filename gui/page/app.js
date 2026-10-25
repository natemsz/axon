// Axon's settings page. Every call to the program carries the key the
// browser was opened with (the address's ?k=), which is then dropped
// from the address bar and kept for this tab only.
"use strict";

(function () {
  const $ = (id) => document.getElementById(id);

  let key = new URLSearchParams(location.search).get("k");
  try {
    if (key) sessionStorage.setItem("axon.key", key);
    else key = sessionStorage.getItem("axon.key");
  } catch (_) { /* storage blocked: the key in the address is enough */ }
  if (location.search) history.replaceState(null, "", "/");

  async function api(path, body) {
    const opts = { method: body === undefined ? "GET" : "POST", headers: { "X-Axon-Key": key || "" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(path, opts);
    } catch (_) {
      throw new Error("Axon is not running any more. Start it again to reopen this page.");
    }
    if (res.status === 403) throw new Error("This page has lost its key: open it from Axon again.");
    const out = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(out.error || res.statusText);
    return out;
  }

  // --- state ---------------------------------------------------------

  let siteChannels = null; // [{id, name, post}] once signed in
  let seenNames = [];      // in-game channels from the log folder
  let dirty = false;
  let running = "";
  let traySupported = false;
  let ownWindow = false;

  function banner(text, bad) {
    const b = $("banner");
    b.textContent = text || "";
    b.hidden = !text;
    b.classList.toggle("bad", !!bad);
    if (text) b.scrollIntoView({ block: "nearest" });
  }

  async function act(button, fn) {
    if (button) button.disabled = true;
    try {
      await fn();
    } catch (e) {
      banner(e.message, true);
    } finally {
      if (button) button.disabled = false;
    }
  }

  function setDirty(v) {
    dirty = v;
    $("dirty").hidden = !v;
  }

  // --- the form --------------------------------------------------------

  function fill(loaded) {
    if (loaded.path) $("path").value = loaded.path;
    const c = loaded.config || {};
    const server = c.server && c.server !== "https://" ? c.server : "";
    const token = c.token && c.token.startsWith("esw_") ? c.token : "";
    // The sign-in check holds while the site and token stay the same.
    if (server !== $("server").value.trim() || token !== $("token").value.trim()) {
      siteChannels = null;
      $("test-out").textContent = "";
    }
    $("server").value = server;
    $("token").value = token;
    $("logs").value = c.logs || "";
    $("map-rows").replaceChildren();
    for (const [name, id] of Object.entries(c.channels || {})) addRow(name, id);
    const p = $("problem");
    p.textContent = loaded.problem || "";
    p.hidden = !loaded.problem;
    p.classList.toggle("bad", !!loaded.exists && !!loaded.problem);
    renderAuto();
  }

  function config() {
    const channels = {};
    for (const tr of $("map-rows").children) {
      const name = tr.querySelector("input.name").value.trim();
      const id = parseInt(tr.querySelector(".site").value, 10);
      if (name && id > 0) channels[name] = id;
    }
    return {
      server: $("server").value.trim(),
      token: $("token").value.trim(),
      logs: $("logs").value.trim(),
      channels,
    };
  }

  function siteControl(value) {
    const postable = (siteChannels || []).filter((c) => c.post);
    let el;
    if (postable.length) {
      el = document.createElement("select");
      el.append(new Option("Choose a channel…", ""));
      for (const c of postable) el.append(new Option(`${c.name} (#${c.id})`, String(c.id)));
      if (value && !postable.some((c) => c.id === Number(value))) {
        el.append(new Option(`#${value} (not one you can post to)`, String(value)));
      }
      el.value = value ? String(value) : "";
    } else {
      el = document.createElement("input");
      el.type = "number";
      el.min = "1";
      el.placeholder = "channel number";
      el.value = value || "";
    }
    el.className = "site";
    el.setAttribute("aria-label", "Site channel");
    return el;
  }

  function addRow(name, id) {
    const tr = document.createElement("tr");
    const n = document.createElement("input");
    n.type = "text";
    n.className = "name";
    n.value = name || "";
    n.placeholder = "in-game channel name";
    n.setAttribute("list", "seen");
    n.setAttribute("aria-label", "In-game channel");
    const del = document.createElement("button");
    del.type = "button";
    del.className = "btn ghost";
    del.textContent = "Remove";
    del.addEventListener("click", () => { tr.remove(); setDirty(true); renderAuto(); });
    const cells = [n, "→", siteControl(id), del];
    const classes = ["", "arrow", "", "del"];
    cells.forEach((c, i) => {
      const td = document.createElement("td");
      if (classes[i]) td.className = classes[i];
      td.append(c);
      tr.append(td);
    });
    $("map-rows").append(tr);
    return n;
  }

  // Swap each row's number box for a list once the site's channels are
  // known (and back), keeping the choice.
  function refreshSiteControls() {
    for (const tr of $("map-rows").children) {
      const old = tr.querySelector(".site");
      old.replaceWith(siteControl(old.value));
    }
  }

  // The channels watched without a mapping: the site's postable ones,
  // marked where a recent log has the same name.
  function renderAuto() {
    const box = $("auto");
    box.replaceChildren();
    if (!siteChannels) {
      const s = document.createElement("span");
      s.className = "muted";
      s.textContent = "Check sign-in to see the site's channels.";
      box.append(s);
      return;
    }
    const seen = new Set(seenNames.map((n) => n.toLowerCase()));
    const postable = siteChannels.filter((c) => c.post);
    if (!postable.length) {
      const s = document.createElement("span");
      s.className = "note bad";
      s.textContent = "This account cannot post to any intel channel yet.";
      box.append(s);
      return;
    }
    for (const c of postable) {
      const chip = document.createElement("span");
      chip.className = "chip";
      const b = document.createElement("b");
      b.textContent = c.name;
      chip.append(b, ` #${c.id}`);
      if (seen.has(c.name.toLowerCase())) chip.append(" · in your logs");
      box.append(chip);
    }
  }

  function setSeen(names) {
    seenNames = names || [];
    const dl = $("seen");
    dl.replaceChildren(...seenNames.map((n) => new Option(n)));
    renderAuto();
  }

  // --- running -------------------------------------------------------

  function setRunning(path, failed) {
    running = path || "";
    const s = $("status");
    s.classList.toggle("on", !!running);
    s.classList.toggle("bad", !running && !!failed);
    s.textContent = running ? "Watching" : failed ? "Stopped: error" : "Stopped";
    s.title = running ? running : failed || "";
    const run = $("run");
    run.textContent = running ? "Stop watching" : "Start watching";
    run.classList.toggle("danger", !!running);
    $("replay").disabled = !!running;
  }

  let after = 0;
  async function poll() {
    try {
      const out = await api("/api/log?after=" + after);
      const log = $("log");
      const atEnd = log.scrollTop + log.clientHeight >= log.scrollHeight - 4;
      for (const l of out.lines) {
        after = l.n;
        const li = document.createElement("li");
        const t = document.createElement("time");
        t.textContent = l.at;
        li.append(t, l.text);
        if (/^(stopped:|could not|config:)|dropping/.test(l.text)) li.className = "bad";
        else if (/new reports?$|^signed in/.test(l.text)) li.className = "good";
        log.append(li);
      }
      while (log.children.length > 500) log.firstChild.remove();
      if (atEnd) log.scrollTop = log.scrollHeight;
      if (out.running !== running) setRunning(out.running, out.failed);
    } catch (_) { /* shown when the player next acts */ }
    setTimeout(poll, 1000);
  }

  // --- wiring --------------------------------------------------------

  async function save(path) {
    const out = await api("/api/save", { path, config: config() });
    fill(out);
    setDirty(false);
    banner("Saved " + out.path + ".");
  }

  async function findLogs(dir) {
    const out = await api("/api/logs", { dir });
    setSeen(out.channels);
    const o = $("logs-out");
    if (!out.dir) {
      o.textContent = "Not found in: " + out.candidates.join(", ") + ". Choose the folder.";
      return;
    }
    const n = out.channels.length;
    o.textContent = (out.found ? "Found " : "") + out.dir + " · " +
      (n ? `${n} channel${n === 1 ? "" : "s"} logged this week` : "no channel logs this week");
  }

  async function browse(kind, from) {
    const out = await api("/api/browse", { kind, from });
    return out.path;
  }

  function wire() {
    for (const id of ["server", "token", "logs"]) $(id).addEventListener("input", () => setDirty(true));
    for (const id of ["server", "token"]) {
      $(id).addEventListener("change", () => {
        siteChannels = null;
        $("test-out").textContent = "";
        refreshSiteControls();
        renderAuto();
      });
    }
    $("map-rows").addEventListener("input", () => setDirty(true));
    $("map-rows").addEventListener("change", () => { setDirty(true); renderAuto(); });
    $("overlay").addEventListener("click", (e) => act(e.currentTarget, async () => {
      await api("/api/overlay", {});
    }));

    $("open").addEventListener("click", (e) => act(e.currentTarget, async () => {
      if (dirty && !confirm("Drop the unsaved changes?")) return;
      fill(await api("/api/open", { path: $("path").value }));
      setDirty(false);
      banner("");
      await findLogs($("logs").value).catch(() => {});
    }));
    $("browse-open").addEventListener("click", (e) => act(e.currentTarget, async () => {
      const p = await browse("open", $("path").value);
      if (!p) return;
      if (dirty && !confirm("Drop the unsaved changes?")) return;
      fill(await api("/api/open", { path: p }));
      setDirty(false);
      banner("");
    }));

    async function importText(text) {
      const out = await api("/api/import", { text });
      const path = $("path").value;
      fill(Object.assign(out, { path }));
      setDirty(true);
      banner("Imported. Save to keep it in " + path + ", or Save as… to choose another file.");
    }
    $("import-file").addEventListener("click", () => $("import-input").click());
    $("import-input").addEventListener("change", (e) => act(null, async () => {
      const f = e.target.files[0];
      e.target.value = "";
      if (f) await importText(await f.text());
    }));
    $("paste-go").addEventListener("click", (e) => act(e.currentTarget, async () => {
      await importText($("paste").value);
      $("paste").value = "";
      $("paste").closest("details").open = false;
    }));

    $("token-show").addEventListener("click", (e) => {
      const t = $("token");
      const show = t.type === "password";
      t.type = show ? "text" : "password";
      e.currentTarget.textContent = show ? "Hide" : "Show";
      e.currentTarget.setAttribute("aria-pressed", String(show));
    });

    $("test").addEventListener("click", (e) => act(e.currentTarget, async () => {
      const o = $("test-out");
      o.textContent = "Checking…";
      try {
        const out = await api("/api/test", config());
        siteChannels = out.channels || [];
        const n = siteChannels.filter((c) => c.post).length;
        o.textContent = `Signed in as ${out.account} · ${n} channel${n === 1 ? "" : "s"} to post to`;
        refreshSiteControls();
        renderAuto();
      } catch (err) {
        o.textContent = "";
        throw err;
      }
    }));

    $("pair").addEventListener("click", (e) => act(e.currentTarget, async () => {
      const o = $("pair-out");
      o.textContent = "Pairing…";
      try {
        const out = await api("/api/pair", {
          server: $("server").value.trim(),
          code: $("pair-code").value.trim(),
          device: $("pair-device").value.trim()
        });
        fill(out);
        setDirty(false);
        o.textContent = "Paired as " + out.account + ".";
        banner("Paired with " + $("server").value.trim() + " as " + out.account + ". Your credential is saved; the site channel list can now be checked.");
      } catch (err) {
        o.textContent = "";
        throw err;
      }
    }));

    $("browse-logs").addEventListener("click", (e) => act(e.currentTarget, async () => {
      const p = await browse("folder", $("logs").value);
      if (!p) return;
      $("logs").value = p;
      setDirty(true);
      await findLogs(p);
    }));
    $("find-logs").addEventListener("click", (e) => act(e.currentTarget, async () => {
      const typed = $("logs").value.trim();
      await findLogs(typed);
    }));

    $("map-add").addEventListener("click", () => addRow("", "").focus());

    $("save").addEventListener("click", (e) => act(e.currentTarget, () => save($("path").value)));
    $("save-as").addEventListener("click", (e) => act(e.currentTarget, async () => {
      const p = await browse("save", $("path").value);
      if (p) await save(p);
    }));

    $("run").addEventListener("click", (e) => act(e.currentTarget, async () => {
      if (running) {
        setRunning((await api("/api/stop", {})).running);
        return;
      }
      if (dirty) await save($("path").value);
      const out = await api("/api/start", { replay: $("replay").checked });
      setRunning(out.running, out.failed);
      banner("");
    }));

    $("to-tray").addEventListener("click", (e) => act(e.currentTarget, async () => {
      if (dirty && !confirm("There are unsaved changes. Close anyway?")) return;
      const out = await api("/api/tray", {});
      if (!out.closed) {
        banner(traySupported
          ? "Axon keeps running in the tray, by the clock. You can close this tab; double-click the icon to bring it back."
          : "Axon keeps running. You can close this tab; open Axon again to bring it back.");
      }
    }));

    $("quit").addEventListener("click", (e) => act(e.currentTarget, async () => {
      const what = running ? "Stop watching and quit Axon?" : "Quit Axon?";
      if (!confirm(dirty ? "There are unsaved changes. " + what : what)) return;
      await api("/api/quit", {}).catch(() => {});
      banner("Axon has quit. You can close this tab.");
      document.querySelectorAll("button, input, select, textarea").forEach((el) => { el.disabled = true; });
    }));

    window.addEventListener("beforeunload", (e) => { if (dirty) e.preventDefault(); });
  }

  // frameless turns the top bar into the window's title bar, in Axon's
  // own window: its buttons, and dragging it moves the window.
  function frameless() {
    document.body.classList.add("frameless");
    $("winctl").hidden = false;
    $("to-tray").hidden = true;
    const win = (action) => api("/api/window", { action }).catch((e) => banner(e.message, true));
    $("win-min").addEventListener("click", () => win("minimize"));
    $("win-max").addEventListener("click", () => win("maximize"));
    $("win-close").addEventListener("click", () => {
      if (dirty && !confirm("There are unsaved changes. Close anyway?")) return;
      win("close");
    });
    const bar = document.querySelector("header");
    const control = "button, input, label, a, select, textarea, .pill";
    bar.addEventListener("mousedown", (e) => {
      if (e.button === 0 && e.detail === 1 && !e.target.closest(control)) win("drag");
    });
    bar.addEventListener("dblclick", (e) => {
      if (!e.target.closest(control)) win("maximize");
    });
  }

  async function start() {
    wire();
    try {
      const s = await api("/api/state");
      $("version").textContent = s.version;
      traySupported = s.tray;
      ownWindow = s.window;
      $("overlay").hidden = !s.overlay;
      const close = $("to-tray");
      close.textContent = traySupported ? "Close to tray" : "Close window";
      close.title = "Axon keeps running" + (traySupported ? " in the tray" : "") + "; open it again to come back";
      close.hidden = !traySupported && !ownWindow;
      if (s.frameless) frameless();
      if (s.notice) banner(s.notice);
      $("replay").checked = s.replay;
      if (s.logsArg) {
        const a = $("logs-arg");
        a.textContent = "Axon was started with -logs " + s.logsArg + ", which is used instead of this folder.";
        a.hidden = false;
      }
      fill(s);
      setRunning(s.running, s.failed);
      await findLogs(s.config.logs || "").catch(() => {});
    } catch (e) {
      banner(e.message, true);
    }
    poll();
  }

  start();
})();
