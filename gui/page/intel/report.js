// The Report window (intel.go): one report broken down, the site's
// workbench drawn as the game draws an info window. The verdict comes
// first (how many, what, where, how fresh), then the hulls on scan,
// the pilots in local, what changed since the report before, and the
// system's gates. A section the report has no data for is left out.
(function () {
  "use strict";
  const I = window.Intel;
  const body = document.getElementById("body");
  const id = +new URLSearchParams(location.search).get("id");

  function cap(text) { return I.el("p", "cap", text); }
  function fig(n, label, hostile) {
    const f = I.el("span", "r-fig");
    f.appendChild(I.el("b", hostile ? "hostile" : "", String(n)));
    f.appendChild(I.el("span", "", label));
    return f;
  }
  function table(head, rows) {
    const t = I.el("table", "r-table");
    const tr = I.el("tr");
    head.forEach((h) => { const th = I.el("th", h.num ? "num" : "", h.text); tr.appendChild(th); });
    const thead = I.el("thead");
    thead.appendChild(tr);
    t.appendChild(thead);
    const tb = I.el("tbody");
    rows.forEach((cells) => {
      const r = I.el("tr");
      cells.forEach((c, i) => {
        const td = I.el("td", (head[i].num ? "num" : "") + (c && c.dim ? " dim" : ""));
        if (c && c.node) td.appendChild(c.node); else td.textContent = c && c.text !== undefined ? c.text : c;
        r.appendChild(td);
      });
      tb.appendChild(r);
    });
    t.appendChild(tb);
    return t;
  }

  function render(v) {
    body.textContent = "";
    document.title = v.SysName ? v.SysName + " report" : "Report";
    document.getElementById("title").textContent = v.Snapshot || "Report";
    document.getElementById("when").textContent = v.Ago || "";
    if (v.Error) { body.appendChild(I.el("p", "problem", v.Error)); return; }

    // The verdict.
    const vd = I.el("div", "r-verdict");
    if (v.HasCounts) vd.appendChild(fig(v.Hostile, v.Hostile === 1 ? "hostile" : "hostiles", v.Hostile > 0));
    if (v.Hulls) vd.appendChild(fig(v.Hulls, (v.Hulls === 1 ? "ship" : "ships") + (v.HasDScan ? ", " + v.OnGrid + " on grid" : "")));
    if (v.PilotsN) vd.appendChild(fig(v.PilotsN, "in local"));
    const where = I.el("span", "r-where");
    if (v.KindLabel) where.appendChild(I.el("span", "tag" + (v.KindTone === "loss" ? " hostile" : v.KindTone === "kill" ? " calm" : ""), v.KindLabel));
    if (v.SysName) {
      const s = I.el("span");
      s.appendChild(I.secEl(v.SecText, v.SecTier));
      s.appendChild(I.el("span", "sys", v.SysName));
      where.appendChild(s);
    }
    vd.appendChild(where);
    body.appendChild(vd);
    if (v.Headline) body.appendChild(I.el("div", "r-line", v.Headline));
    const meta = [v.Reporter ? "Reported by " + v.Reporter : "", v.ChannelName, v.SysLine].filter(Boolean).join("  ·  ");
    if (meta) body.appendChild(I.el("div", "r-meta", meta));
    if (v.GangTracked) {
      const g = I.el("div", "r-gang");
      g.appendChild(I.el("div", "", (v.GangStale ? "Gang gone quiet" : "Tracked gang") + (v.GangComp ? "  ·  " + v.GangComp : "")));
      g.appendChild(I.el("div", "r-psub", [v.GangSeen, v.GangConf ? "confidence " + v.GangConf + "%" : "", v.GangTrail ? "sighted in " + v.GangTrail : ""].filter(Boolean).join("  ·  ")));
      body.appendChild(g);
    }

    // The hulls on scan.
    if (v.HasDScan && v.Types && v.Types.length) {
      body.appendChild(cap("On scan" + (v.Doctrine ? "  ·  " + v.Doctrine : "")));
      body.appendChild(table(
        [{ text: "Hull" }, { text: "Class" }, { text: "Count", num: true }, { text: "On grid", num: true }, { text: "Range" }],
        v.Types.map((h) => [h.Name, { text: h.Role || h.Class, dim: true }, h.N, h.Grid, { text: h.Range, dim: true }])));
    } else if (v.Roles && v.Roles.length) {
      body.appendChild(cap("Hull roles"));
      body.appendChild(table([{ text: "Role" }, { text: "Count", num: true }], v.Roles.map((r) => [r.Label, r.N])));
    }

    // The pilots.
    if (v.AllPilots && v.AllPilots.length) {
      const counts = v.HasCounts ? "  ·  " + v.Hostile + " hostile, " + v.Neutral + " neutral, " + v.Friendly + " friendly" : "";
      body.appendChild(cap("Pilots" + counts));
      v.AllPilots.forEach((p) => {
        const row = I.el("div", "r-pilot");
        row.appendChild(I.el("span", "r-dot " + (p.Bucket || "")));
        const who = I.el("span");
        who.appendChild(I.el("div", "r-pname", p.Name));
        if (p.CorpLine) who.appendChild(I.el("div", "r-psub", p.CorpLine));
        if (p.Facts) who.appendChild(I.el("div", "r-psub", p.Facts));
        row.appendChild(who);
        const flags = I.el("span", "r-flags");
        if (p.Young) flags.appendChild(I.el("span", "tag", "Young alt"));
        if (p.Recent) flags.appendChild(I.el("span", "tag", "Joined <7d"));
        if (p.KilledHere) flags.appendChild(I.el("span", "tag hostile", "Killed here"));
        row.appendChild(flags);
        body.appendChild(row);
      });
    }

    // What changed since the report before.
    if (v.HasPrev && ((v.HullDeltas && v.HullDeltas.length) || (v.PilotDeltas && v.PilotDeltas.length))) {
      body.appendChild(cap("Since " + (v.PrevAgo || "the report before") + (v.PrevBy ? " by " + v.PrevBy : "")));
      (v.HullDeltas || []).forEach((d) => {
        const row = I.el("div", "r-pilot");
        row.appendChild(I.el("span", "r-pname", d.Name));
        row.appendChild(I.el("span", "r-delta " + d.Dir, d.Text));
        body.appendChild(row);
      });
      (v.PilotDeltas || []).forEach((d) => {
        const row = I.el("div", "r-pilot");
        row.appendChild(I.el("span", "r-dot " + (d.Bucket || "")));
        row.appendChild(I.el("span", "r-pname", d.Name));
        row.appendChild(I.el("span", "r-delta " + d.Dir, d.Dir));
        body.appendChild(row);
      });
    }

    // The gates.
    if (v.Gates && v.Gates.length) {
      body.appendChild(cap("Gates"));
      body.appendChild(table(
        [{ text: "System" }, { text: "Region" }, { text: "Jumps 1h", num: true }, { text: "Ship / pod", num: true }],
        v.Gates.map((g) => {
          const s = I.el("span");
          s.appendChild(I.secEl(g.SecText, g.SecTier));
          s.appendChild(I.el("span", "sys", g.Name));
          return [{ node: s }, { text: g.Region, dim: true }, g.HasStats ? g.Jumps : "", g.HasStats ? g.Ship + " / " + g.Pod : ""];
        })));
    }
  }

  async function load() {
    if (!id) return;
    try {
      const out = await I.api("/api/intel/report?id=" + id);
      render(out.report || {});
      if (out.report && out.report.SystemID) I.send({ type: "open", report: id, thread: out.thread, sys: out.report.SystemID });
    } catch (e) {
      body.textContent = "";
      body.appendChild(I.el("p", "problem", e.message));
    }
  }
  load();
})();
