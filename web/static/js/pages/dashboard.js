import { html, $, api, ago, pct, deviceName, modelLabel, plural, complianceChip } from "../lib.js";
import { barList, statusStack, orchardGrid } from "../charts.js";

export async function render({ root, navigate }) {
  const [d, devs] = await Promise.all([api.get("/api/dashboard"), api.get("/api/devices?status=enrolled&limit=1000")]);
  const notReady = d.readiness.filter((r) => !r.ok && r.id !== "signing");
  const c = d.devices;
  const issues = (d.compliance.noncompliant || 0) + (d.compliance.grace || 0);
  root.innerHTML = html`
    <div class="page-head"><div><h1>Overview</h1><p class="sub">Health of every iPhone and iPad you manage.</p></div>
      <div class="actions"><a class="btn" href="#/enrollment">Enroll devices</a></div></div>
    ${notReady.length ? html`<div class="callout warn"><p><strong>Finish setting up before enrolling devices.</strong></p>
      <ul class="checklist">${d.readiness.map((r) => html`<li class="${r.ok ? "ok" : "no"}"><span class="mark" aria-hidden="true">${r.ok ? "✓" : "!"}</span><div><strong>${r.label}</strong><div class="hint">${r.ok ? r.detail || "Done" : r.help}</div></div></li>`)}</ul>
      <p><a href="#/settings">Open settings</a></p></div>` : ""}
    ${d.apns.configured && d.apns.days_left <= 30 ? html`<div class="callout bad"><p><strong>Your Apple push certificate expires in ${d.apns.days_left} days.</strong> Renew it with the same Apple ID, or every device stops receiving commands. <a href="#/settings/push">Renew now</a></p></div>` : ""}
    <section class="overview">
      <div class="panel hero">
        <div><div class="value">${c.enrolled.toLocaleString()}</div><div class="label">${c.enrolled === 1 ? "device enrolled" : "devices enrolled"}</div></div>
        <div class="detail">${c.pending ? html`${plural(c.pending, "device")} enrolling now. ` : ""}${d.ade.devices ? html`${plural(d.ade.devices, "device")} in Apple Business Manager${d.ade.unassigned ? html`, ${d.ade.unassigned} without an enrollment profile` : ""}.` : ""}</div>
      </div>
      <div class="panel tiles">
        <a class="tile" href="#/devices?compliance=noncompliant"><div class="t-label">Need attention</div><div class="t-value">${issues}</div><div class="t-note">not compliant or in grace</div></a>
        <a class="tile" href="#/commands?status=pending"><div class="t-label">Commands waiting</div><div class="t-value">${d.pending_commands}</div><div class="t-note">${(d.commands_24h.Error || 0) + (d.commands_24h.CommandFormatError || 0)} failed in 24 h</div></a>
        <a class="tile" href="#/devices?sort=battery"><div class="t-label">Low battery</div><div class="t-value">${c.low_battery}</div><div class="t-note">below 20%</div></a>
        <a class="tile" href="#/devices?sort=last_seen&dir=asc"><div class="t-label">Not seen in 7 days</div><div class="t-value">${c.stale}</div><div class="t-note">may be off or unreachable</div></a>
        <a class="tile" href="#/devices"><div class="t-label">In Lost Mode</div><div class="t-value">${c.lost_mode}</div><div class="t-note">being located</div></a>
      </div>
    </section>
    <section class="panel" id="orchard"></section>
    <div class="grid-3" style="margin-top:16px">
      <section class="panel"><div class="panel-head"><h2>Compliance</h2><a class="hint" href="#/compliance">Policies</a></div><div class="panel-pad">${statusStack(d.compliance)}</div></section>
      <section class="panel"><div class="panel-head"><h2>iOS versions</h2></div><div class="panel-pad">${barList(d.os_versions, { href: (i) => `#/devices?os=${encodeURIComponent(i.key)}` })}</div></section>
      <section class="panel"><div class="panel-head"><h2>Models</h2></div><div class="panel-pad">${barList(d.models.map((m) => ({ ...m, label: modelLabel(m.key) })), { href: (i) => `#/devices?model=${encodeURIComponent(i.key)}` })}</div></section>
    </div>
    <div class="grid-2" style="margin-top:16px">
      <section class="panel"><div class="panel-head"><h2>Needs a look</h2></div>
        ${attention(d.attention)}
      </section>
      <section class="panel"><div class="panel-head"><h2>Recent activity</h2><a class="hint" href="#/activity">All activity</a></div>
        <ul class="timeline panel-pad" style="padding-top:4px">${d.recent_events.length ? d.recent_events.map((e) => html`<li><time>${ago(e.ts)}</time><div class="${e.level === "warn" ? "lvl-warn" : ""}">${e.device_id ? html`<a href="#/devices/${encodeURIComponent(e.device_id)}">${e.device_name || "Device"}</a>: ` : ""}${e.message}</div></li>`) : html`<li><span></span><span class="hint">Nothing has happened yet.</span></li>`}</ul>
      </section>
    </div>`.s;
  orchardGrid($("#orchard", root), devs.items, { onPick: (u) => navigate("/devices/" + encodeURIComponent(u)) });
}

function attention(a) {
  const rows = [];
  for (const d of a.lost_mode) rows.push([d, "In Lost Mode"]);
  for (const d of a.low_battery) rows.push([d, `Battery ${pct(d.battery_level)}`]);
  for (const d of a.low_storage) rows.push([d, `${d.available_gb.toFixed(1)} GB free`]);
  for (const d of a.stale) rows.push([d, `Last seen ${ago(d.last_seen)}`]);
  if (!rows.length) return html`<p class="hint panel-pad" style="margin:0">No devices are low on battery or storage, lost, or out of touch.</p>`;
  return html`<div class="table-wrap"><table class="table"><tbody>${rows.slice(0, 12).map(([d, why]) => html`<tr><td><a class="primary" href="#/devices/${encodeURIComponent(d.udid)}">${deviceName(d)}</a><span class="cell-sub">${d.product_name}</span></td><td>${why}</td><td class="right">${complianceChip(d.compliance)}</td></tr>`)}</tbody></table></div>`;
}
