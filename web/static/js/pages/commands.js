import { html, $, $$, api, ago, fmtTime, commandChip, qsParams, toastError, emptyState } from "../lib.js";
import { showCommand } from "./device.js";

export async function render({ root, query }) {
  const f = { status: query.get("status") || "", type: query.get("type") || "", offset: 0 };
  root.innerHTML = html`<div class="page-head"><div><h1>Command queue</h1><p class="sub">Every command sent to devices, by people, automation and assignments. Devices fetch commands when they check in.</p></div></div>
    <div class="toolbar"><select id="st" aria-label="Status"><option value="">All commands</option><option value="pending" ${f.status === "pending" ? "selected" : ""}>Waiting for the device</option>
      <option value="failed" ${f.status === "failed" ? "selected" : ""}>Failed</option><option value="Acknowledged" ${f.status === "Acknowledged" ? "selected" : ""}>Done</option><option value="NotNow">Deferred by device</option><option value="Canceled">Canceled</option><option value="Expired">Expired</option></select>
      <input type="search" id="type" placeholder="Request type, e.g. InstallProfile" value="${f.type}" style="max-width:280px"></div>
    <div id="stats"></div><section class="panel" id="list"></section>`.s;
  const load = async () => {
    const res = await api.get("/api/commands" + qsParams({ status: f.status, type: f.type, limit: 100, offset: f.offset }));
    const s = res.stats || {};
    $("#stats", root).innerHTML = html`<p class="hint">Last 7 days: ${s.Acknowledged || 0} done, ${(s.Error || 0) + (s.CommandFormatError || 0)} failed, ${(s.Queued || 0) + (s.Sent || 0) + (s.NotNow || 0)} waiting.</p>`.s;
    $("#list", root).innerHTML = (res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Command</th><th>Device</th><th>Status</th><th>Source</th><th>Queued</th><th>Completed</th></tr></thead><tbody>
      ${res.items.map((c) => html`<tr class="clickable" tabindex="0" data-uuid="${c.uuid}"><td><strong>${c.request_type}</strong>${c.error_text ? html`<span class="cell-sub">${c.error_text}</span>` : ""}</td>
        <td><a href="#/devices/${encodeURIComponent(c.device_id)}">${c.device_name || c.device_id.slice(0, 13)}</a></td><td>${commandChip(c.status)}</td><td>${c.created_by || c.source}</td>
        <td class="nowrap" title="${fmtTime(c.created_at)}">${ago(c.created_at)}</td><td class="nowrap">${c.completed_at ? ago(c.completed_at) : "—"}</td></tr>`)}
      </tbody></table></div><div class="pager"><span>${f.offset + 1}–${f.offset + res.items.length} of ${res.total.toLocaleString()}</span>
      <span class="row"><button class="btn btn-sm" id="prev" ${f.offset ? "" : "disabled"}>Previous</button><button class="btn btn-sm" id="next" ${f.offset + 100 >= res.total ? "disabled" : ""}>Next</button></span></div>`
      : emptyState("No commands", "Nothing matches these filters.")).s;
    $$("[data-uuid]", root).forEach((tr) => {
      tr.addEventListener("click", (e) => { if (!e.target.closest("a")) showCommand(tr.dataset.uuid).then(() => {}).catch(toastError); });
      tr.addEventListener("keydown", (e) => { if (e.key === "Enter") showCommand(tr.dataset.uuid).catch(toastError); });
    });
    $("#prev", root)?.addEventListener("click", () => { f.offset = Math.max(0, f.offset - 100); load(); });
    $("#next", root)?.addEventListener("click", () => { f.offset += 100; load(); });
  };
  $("#st", root).addEventListener("change", (e) => { f.status = e.target.value; f.offset = 0; load(); });
  let t;
  $("#type", root).addEventListener("input", (e) => { clearTimeout(t); t = setTimeout(() => { f.type = e.target.value.trim(); f.offset = 0; load(); }, 300); });
  await load();
}
