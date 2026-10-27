// The Threads window (intel.go): the recent threads across the
// player's channels, one row each, newest first. A row opens its
// report in the Report window and moves the map; the map marks every
// thread's system, and pings the ones with news this browser has not
// opened.
(function () {
  "use strict";
  const I = window.Intel;
  const rows = document.getElementById("rows");
  const SEEN_KEY = "axon.intel.seen";
  let threads = [], recent = 1800, openThread = 0, known = {};

  function seen() { try { return JSON.parse(localStorage.getItem(SEEN_KEY) || "{}") || {}; } catch (_) { return {}; } }
  function markSeen(t) {
    const s = seen();
    s[t.id] = t.report;
    const keys = Object.keys(s);
    if (keys.length > 300) keys.sort((a, b) => a - b).slice(0, keys.length - 300).forEach((k) => delete s[k]);
    try { localStorage.setItem(SEEN_KEY, JSON.stringify(s)); } catch (_) { /* fine */ }
  }
  function fresh(t, s, now) { return now - t.at < recent && s[t.id] !== t.report; }

  // "5 ships", "12 hostiles", "Clear", in the thread's newest words.
  function what(t) {
    if (t.clear) return { text: "Clear", hostile: false };
    if (t.n) return { text: t.n + (t.n === 1 ? " hostile" : " hostiles"), hostile: true };
    if (t.count && t.noun) return { text: t.count + " " + t.noun + (t.count === 1 ? "" : "s"), hostile: false };
    return { text: "Hostiles", hostile: true };
  }

  function rowEl(t) {
    const li = I.el("li", "row" + (t.id === openThread ? " on" : ""));
    li.dataset.id = t.id;
    const top = I.el("div", "row-top");
    const main = I.el("span", "row-main");
    const w = what(t);
    main.appendChild(I.el("span", "n" + (w.hostile ? " hostile" : ""), w.text));
    main.appendChild(document.createTextNode("  "));
    main.appendChild(I.secEl(t.sec, t.tier));
    main.appendChild(I.el("span", "sys", t.sysName));
    if (t.systems > 1) main.appendChild(document.createTextNode("  +" + (t.systems - 1)));
    top.appendChild(main);
    top.appendChild(I.el("span", "row-time", I.ago(t.at)));
    li.appendChild(top);
    li.appendChild(I.el("div", "row-sub", t.kind + (t.by ? " by " + t.by : "") + " at " + I.eveTime(t.at) + (t.chn ? "  ·  " + t.chn : "")));
    li.addEventListener("click", () => open(t));
    return li;
  }

  function open(t) {
    openThread = t.id;
    rows.querySelectorAll(".row.on").forEach((r) => r.classList.remove("on"));
    const li = rows.querySelector('[data-id="' + t.id + '"]');
    if (li) li.classList.add("on");
    markSeen(t);
    I.openReport({ report: t.report, thread: t.id, sys: t.sys });
  }

  function render() {
    rows.textContent = "";
    threads.forEach((t) => {
      const li = rowEl(t);
      if (known[t.id] !== undefined && known[t.id] !== t.report) li.classList.add("new");
      rows.appendChild(li);
    });
    document.getElementById("empty").hidden = threads.length > 0;
    document.getElementById("count").textContent = threads.length ? String(threads.length) : "";
    document.getElementById("updated").textContent = "Updated " + I.eveTime(Date.now() / 1000) + " EVE";
  }

  function tellMap(first) {
    I.send({ type: "threads", rows: threads.map((t) => ({ id: t.id, report: t.report, sys: t.sys, at: t.at })), open: openThread });
    const s = seen(), now = Date.now() / 1000;
    threads.forEach((t) => {
      const changed = known[t.id] === undefined ? !first : known[t.id] !== t.report;
      if (fresh(t, s, now)) I.send({ type: "ping", sys: t.sys, id: t.id, at: t.at, recent, fresh: changed });
    });
  }

  let firstLoad = true;
  async function load() {
    try {
      const out = await I.api("/api/intel/threads");
      threads = out.threads || [];
      recent = out.recent || recent;
      document.getElementById("problem").hidden = true;
      render();
      tellMap(firstLoad);
      if (firstLoad && threads.length && !openThread) {
        // The map opens on the newest thread.
        I.send({ type: "focus", sys: threads[0].sys });
      }
      firstLoad = false;
      known = {};
      threads.forEach((t) => { known[t.id] = t.report; });
    } catch (e) {
      const p = document.getElementById("problem");
      p.textContent = e.message;
      p.hidden = false;
    }
  }

  // Another window opened a report: mark its thread here.
  I.on((m) => {
    if (m.type === "open" && m.thread) {
      openThread = m.thread;
      rows.querySelectorAll(".row").forEach((r) => r.classList.toggle("on", +r.dataset.id === openThread));
    }
    // The map, newly opened, asks for what to draw.
    if (m.type === "hello") tellMap(true);
  });

  I.every(10000, load);
})();
