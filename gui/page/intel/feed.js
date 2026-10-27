// The Feed window (intel.go): every report across the player's
// channels as it lands, newest first, the way the site's activity log
// shows them. A row opens its report.
(function () {
  "use strict";
  const I = window.Intel;
  const rows = document.getElementById("rows");
  const KEEP = 120;
  let after = 0, first = true;

  function rowEl(r) {
    const li = I.el("li", "row");
    li.dataset.id = r.id;
    const top = I.el("div", "row-top");
    const main = I.el("span", "row-main");
    main.appendChild(I.secEl(r.sec, r.tier));
    main.appendChild(I.el("span", "sys", r.sysName));
    if (r.kindLabel) {
      main.appendChild(document.createTextNode("  "));
      main.appendChild(I.el("span", "tag" + (r.tone === "loss" ? " hostile" : r.tone === "kill" ? " calm" : ""), r.kindLabel));
    }
    top.appendChild(main);
    top.appendChild(I.el("span", "row-time", I.eveTime(r.at)));
    li.appendChild(top);
    li.appendChild(I.el("div", "row-sub", r.text));
    li.appendChild(I.el("div", "row-sub", (r.by || "") + (r.chn ? "  ·  " + r.chn : "")));
    li.addEventListener("click", () => I.openReport({ report: r.id, sys: r.sys }));
    return li;
  }

  async function load() {
    try {
      const out = await I.api("/api/intel/feed?after=" + after);
      const list = out.reports || [];
      document.getElementById("problem").hidden = true;
      // Newest first from the site: put the oldest on top first.
      for (let i = list.length - 1; i >= 0; i--) {
        const r = list[i];
        after = Math.max(after, r.id);
        if (rows.querySelector('[data-id="' + r.id + '"]')) continue;
        const li = rowEl(r);
        if (!first) {
          li.classList.add("new");
          I.send({ type: "ping", sys: r.sys, id: "r" + r.id, at: r.at, fresh: true });
        }
        rows.insertBefore(li, rows.firstChild);
      }
      while (rows.children.length > KEEP) rows.lastChild.remove();
      document.getElementById("empty").hidden = rows.children.length > 0;
      first = false;
    } catch (e) {
      const p = document.getElementById("problem");
      p.textContent = e.message;
      p.hidden = false;
    }
  }

  I.every(10000, load);
})();
