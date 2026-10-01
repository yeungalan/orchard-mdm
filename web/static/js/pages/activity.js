import { html, $, api, can, ago, fmtTime, emptyState, debounce } from "../lib.js";

export async function render({ root, query }) {
  const tab = query.get("tab") || "events";
  root.innerHTML = html`<div class="page-head"><div><h1>Activity</h1><p class="sub">What devices did, and what administrators changed.</p></div></div>
    <nav class="tabs"><a href="#/activity?tab=events" aria-selected="${tab === "events"}">Device activity</a>${can("admin") ? html`<a href="#/activity?tab=audit" aria-selected="${tab === "audit"}">Audit log</a>` : ""}</nav>
    <div id="body"></div>`.s;
  const body = $("#body", root);
  if (tab === "audit" && can("admin")) {
    body.innerHTML = html`<div class="toolbar"><input type="search" id="q" placeholder="Filter by person, action or target" style="max-width:360px"></div><section class="panel" id="list"></section>`.s;
    const load = async () => {
      const res = await api.get(`/api/audit?limit=300&q=${encodeURIComponent($("#q", body).value)}`);
      $("#list", body).innerHTML = (res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>When</th><th>Who</th><th>Action</th><th>Target</th><th>Details</th><th>IP</th></tr></thead><tbody>
        ${res.items.map((a) => html`<tr><td class="nowrap" title="${fmtTime(a.ts)}">${ago(a.ts)}</td><td>${a.actor}</td><td class="ident">${a.action}</td><td class="ident">${a.target}</td>
          <td style="max-width:360px;overflow-wrap:anywhere" class="hint">${a.details}</td><td class="ident">${a.ip}</td></tr>`)}</tbody></table></div>`
        : emptyState("No entries", "Nothing matches.")).s;
    };
    $("#q", body).addEventListener("input", debounce(load, 300));
    return load();
  }
  const res = await api.get("/api/events?limit=300");
  body.innerHTML = html`<section class="panel panel-pad">${res.items.length ? html`<ul class="timeline">${res.items.map((e) => html`<li><time title="${fmtTime(e.ts)}">${ago(e.ts)}</time>
    <div class="${e.level === "warn" ? "lvl-warn" : ""}">${e.device_id ? html`<a href="#/devices/${encodeURIComponent(e.device_id)}">${e.device_name || "Device"}</a>: ` : ""}${e.message}</div></li>`)}</ul>`
    : html`<p class="hint" style="margin:0">Nothing has happened yet.</p>`}</section>`.s;
}
