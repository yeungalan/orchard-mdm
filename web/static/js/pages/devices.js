import { html, $, $$, api, can, ago, pct, gb, deviceName, modelLabel, complianceChip, enrollChip, qsParams, debounce, icons, modal, toast, toastError, plural, emptyState } from "../lib.js";
import { commandDialog, groups } from "../components.js";

const PAGE = 50;

export async function render({ root, query, navigate }) {
  const f = {
    q: query.get("q") || "", status: query.get("status") === "any" ? "" : query.get("status") || "enrolled", compliance: query.get("compliance") || "", model: query.get("model") || "",
    os: query.get("os") || "", ownership: query.get("ownership") || "", supervised: query.get("supervised") || "", group: query.get("group") || "",
    sort: query.get("sort") || "", dir: query.get("dir") || "", offset: Number(query.get("offset") || 0),
  };
  const [facets, gs] = await Promise.all([api.get("/api/devices/facets"), groups()]);
  const selected = new Set();
  root.innerHTML = html`
    <div class="page-head"><div><h1>Devices</h1><p class="sub">Search, filter and act on enrolled iPhones and iPads.</p></div>
      <div class="actions"><button class="btn" id="export">Export CSV</button><a class="btn btn-primary" href="#/enrollment">Enroll devices</a></div></div>
    <div class="toolbar">
      <label class="search">${icons.search}<input type="search" id="q" placeholder="Name, serial, user, IMEI, tag…" value="${f.q}" aria-label="Search devices"></label>
      <select id="f-status" aria-label="Enrollment status"><option value="">Any status</option>${[["enrolled", "Enrolled"], ["pending", "Enrolling"], ["unenrolled", "Unenrolled"]].map(([v, l]) => html`<option value="${v}" ${f.status === v ? "selected" : ""}>${l}</option>`)}</select>
      <select id="f-compliance" aria-label="Compliance"><option value="">Any compliance</option>${[["compliant", "Compliant"], ["grace", "In grace period"], ["noncompliant", "Not compliant"], ["unknown", "Not evaluated"]].map(([v, l]) => html`<option value="${v}" ${f.compliance === v ? "selected" : ""}>${l}</option>`)}</select>
      <select id="f-model" aria-label="Model"><option value="">Any model</option>${(facets.models || []).map((m) => html`<option ${f.model === m ? "selected" : ""}>${m}</option>`)}</select>
      <select id="f-os" aria-label="iOS version"><option value="">Any iOS</option>${(facets.os_versions || []).map((m) => html`<option ${f.os === m ? "selected" : ""}>${m}</option>`)}</select>
      <select id="f-ownership" aria-label="Ownership"><option value="">Any ownership</option>${[["corporate", "Corporate"], ["personal", "Personal"], ["unknown", "Unknown"]].map(([v, l]) => html`<option value="${v}" ${f.ownership === v ? "selected" : ""}>${l}</option>`)}</select>
      <select id="f-supervised" aria-label="Supervision"><option value="">Supervised or not</option><option value="1" ${f.supervised === "1" ? "selected" : ""}>Supervised</option><option value="0" ${f.supervised === "0" ? "selected" : ""}>Not supervised</option></select>
      <select id="f-group" aria-label="Group"><option value="">Any group</option>${gs.map((g) => html`<option value="${g.id}" ${String(g.id) === f.group ? "selected" : ""}>${g.name}</option>`)}</select>
    </div>
    <div id="bulk"></div>
    <section class="panel" id="list"><p class="hint panel-pad">Loading…</p></section>`.s;

  const sync = () => {
    const p = { ...f, status: f.status === "enrolled" ? "" : f.status || "any" };
    if (!p.offset) delete p.offset;
    history.replaceState(null, "", "#/devices" + qsParams(p));
  };

  const load = async () => {
    const params = { ...f, limit: PAGE };
    const res = await api.get("/api/devices" + qsParams(params));
    const list = $("#list", root);
    if (!res.items.length) {
      list.innerHTML = emptyState(f.q || f.compliance || f.model || f.os ? "No devices match these filters" : "No devices yet",
        f.q || f.compliance ? "Clear a filter or search for something else." : "Share an enrollment link to add the first iPhone or iPad.",
        f.q || f.compliance ? "" : html`<a class="btn btn-primary" href="#/enrollment">Create an enrollment link</a>`).s;
      return;
    }
    const th = (key, label, cls = "") => html`<th class="${cls}"><button data-sort="${key}">${label}${f.sort === key ? (f.dir === "desc" ? " ↓" : " ↑") : ""}</button></th>`;
    list.innerHTML = html`<div class="table-wrap"><table class="table">
      <thead><tr>${can("act") ? html`<th class="sel"><input type="checkbox" id="sel-all" aria-label="Select all on this page"></th>` : ""}${th("name", "Device")}${th("user", "User")}${th("os", "iOS")}<th>Status</th>${th("compliance", "Compliance")}${th("battery", "Battery", "num")}<th class="num">Free</th>${th("last_seen", "Last seen")}</tr></thead>
      <tbody>${res.items.map((d) => html`<tr data-udid="${d.udid}">
        ${can("act") ? html`<td class="sel"><input type="checkbox" value="${d.udid}" aria-label="Select ${deviceName(d)}" ${selected.has(d.udid) ? "checked" : ""}></td>` : ""}
        <td><a class="primary" href="#/devices/${encodeURIComponent(d.udid)}">${deviceName(d)}</a><span class="cell-sub">${modelLabel(d.product_name) || "Unknown model"}<span class="ident">${d.serial_number}</span></span></td>
        <td>${d.assigned_user || html`<span class="muted">—</span>`}${d.tags.length ? html`<span class="cell-sub">${d.tags.map((t) => html`<span class="tag">${t}</span>`)}</span>` : ""}</td>
        <td class="nowrap">${d.os_version || "—"}${d.supervised ? html`<span class="cell-sub">Supervised</span>` : ""}</td>
        <td>${d.lost_mode ? html`<span class="chip chip-warn">Lost Mode</span>` : enrollChip(d.enrollment_status)}${d.pending_commands ? html`<span class="cell-sub">${plural(d.pending_commands, "command")} waiting</span>` : ""}</td>
        <td>${complianceChip(d.compliance)}</td>
        <td class="num">${pct(d.battery_level)}</td>
        <td class="num">${d.capacity_gb ? gb(d.available_gb) : "—"}</td>
        <td class="nowrap">${ago(d.last_seen)}</td></tr>`)}</tbody></table></div>
      <div class="pager"><span>${f.offset + 1}–${f.offset + res.items.length} of ${res.total.toLocaleString()}</span>
        <span class="row"><button class="btn btn-sm" id="prev" ${f.offset === 0 ? "disabled" : ""}>Previous</button><button class="btn btn-sm" id="next" ${f.offset + PAGE >= res.total ? "disabled" : ""}>Next</button></span></div>`.s;
    $("#prev", list)?.addEventListener("click", () => { f.offset = Math.max(0, f.offset - PAGE); sync(); load(); });
    $("#next", list)?.addEventListener("click", () => { f.offset += PAGE; sync(); load(); });
    $$("[data-sort]", list).forEach((b) => b.addEventListener("click", () => {
      if (f.sort === b.dataset.sort) f.dir = f.dir === "desc" ? "" : "desc"; else { f.sort = b.dataset.sort; f.dir = ""; }
      f.offset = 0; sync(); load();
    }));
    $("#sel-all", list)?.addEventListener("change", (e) => {
      $$("tbody .sel input", list).forEach((i) => { i.checked = e.target.checked; e.target.checked ? selected.add(i.value) : selected.delete(i.value); });
      renderBulk();
    });
    $$("tbody .sel input", list).forEach((i) => i.addEventListener("change", () => { i.checked ? selected.add(i.value) : selected.delete(i.value); renderBulk(); }));
  };

  const renderBulk = () => {
    const bar = $("#bulk", root);
    if (!selected.size) { bar.innerHTML = ""; return; }
    bar.innerHTML = html`<div class="bulkbar"><strong>${plural(selected.size, "device")} selected</strong>
      <button class="btn btn-sm btn-primary" id="b-cmd">Send command</button>
      <button class="btn btn-sm" id="b-sync">Refresh inventory</button>
      ${can("manage") ? html`<button class="btn btn-sm" id="b-group">Add to group</button>` : ""}
      <button class="btn btn-sm" id="b-tag">Add tags</button>
      <span class="spacer"></span><button class="btn btn-sm btn-ghost" id="b-clear">Clear selection</button></div>`.s;
    const ids = () => [...selected];
    $("#b-clear", bar).onclick = () => { selected.clear(); renderBulk(); load(); };
    $("#b-cmd", bar).onclick = () => commandDialog(ids(), { onDone: load });
    $("#b-sync", bar).onclick = async () => {
      try { await api.post("/api/devices/bulk", { udids: ids(), action: "sync" }); toast(`Inventory refresh queued for ${plural(selected.size, "device")}`); } catch (e) { toastError(e); }
    };
    $("#b-group", bar) && ($("#b-group", bar).onclick = async () => {
      const statics = (await groups(true)).filter((g) => g.kind === "static");
      if (!statics.length) return toast("Create a static group first (Groups → New group)", "err");
      modal({ title: "Add to group", body: html`<label class="field"><span>Static group</span><select name="g">${statics.map((g) => html`<option value="${g.id}">${g.name}</option>`)}</select></label>`,
        actions: [{ id: "c", label: "Cancel" }, { id: "ok", label: "Add", kind: "primary", submit: true, onClick: async (c) => {
          await api.post("/api/devices/bulk", { udids: ids(), action: "add_to_group", group_id: Number($("form", c.el).g.value) });
          toast("Added to group");
        } }] });
    });
    $("#b-tag", bar).onclick = () => modal({ title: "Add tags", body: html`<label class="field"><span>Tags</span><input type="text" name="t" placeholder="e.g. Tokyo, Loaner"><small class="hint">Comma separated.</small></label>`,
      actions: [{ id: "c", label: "Cancel" }, { id: "ok", label: "Add tags", kind: "primary", submit: true, onClick: async (c) => {
        const tags = $("form", c.el).t.value.split(",").map((s) => s.trim()).filter(Boolean);
        await api.post("/api/devices/bulk", { udids: ids(), action: "add_tags", tags });
        toast("Tags added"); load();
      } }] });
  };

  $("#q", root).addEventListener("input", debounce((e) => { f.q = e.target.value; f.offset = 0; sync(); load(); }, 300));
  for (const k of ["status", "compliance", "model", "os", "ownership", "supervised", "group"]) {
    $("#f-" + k, root).addEventListener("change", (e) => { f[k] = e.target.value; f.offset = 0; sync(); load(); });
  }
  $("#export", root).addEventListener("click", () => api.download("GET", "/api/devices/export.csv" + qsParams({ ...f, offset: "" }), undefined, "devices.csv").catch(toastError));
  await load();
}
