import { html, raw, $, $$, api, can, ago, fmtTime, fmtDate, pct, gb, bytes, deviceName, complianceChip, enrollChip, commandChip, stateChip, ownershipLabel, modelLabel, enrollTypeLabel, modal, toast, toastError, confirmDialog, plural, copyText, emptyState } from "../lib.js";
import { commandDialog, intentLabel } from "../components.js";
import { lineChart } from "../charts.js";

const TABS = [
  ["overview", "Overview"], ["telemetry", "Battery & network"], ["location", "Location"], ["apps", "Apps"], ["profiles", "Profiles"],
  ["certificates", "Certificates"], ["configuration", "Configuration"], ["commands", "Commands"], ["activity", "Activity"],
];

const yes = (b) => (b ? "Yes" : "No");
const v = (x) => (x === undefined || x === null || x === "" ? html`<span class="muted">—</span>` : x);

export async function render(ctx) {
  const { root, params, navigate } = ctx;
  const udid = params.udid;
  const tab = params.tab || "overview";
  const data = await api.get(`/api/devices/${encodeURIComponent(udid)}`);
  const d = data.device;
  const base = `#/devices/${encodeURIComponent(udid)}`;
  const enrolled = d.enrollment_status === "enrolled";
  root.innerHTML = html`
    <div class="crumb"><a href="#/devices">Devices</a></div>
    <div class="page-head">
      <div><h1>${deviceName(d)}</h1>
        <p class="sub">${modelLabel(d.product_name) || "Unknown model"}${d.os_version ? `, iOS ${d.os_version}` : ""}${d.serial_number ? html`<span class="ident" style="margin-left:12px">${d.serial_number}</span>` : ""}</p>
        <div class="row" style="margin-top:10px">${enrollChip(d.enrollment_status)} ${complianceChip(d.compliance)}
          ${d.user_enrollment ? html`<span class="chip chip-info">User Enrollment</span>` : d.supervised ? html`<span class="chip chip-plain">Supervised</span>` : html`<span class="chip chip-plain">Not supervised</span>`}
          ${d.lost_mode ? html`<span class="chip chip-warn">Lost Mode</span>` : ""}
          <span class="chip chip-plain">${ownershipLabel(d.ownership)}</span>
          ${data.pending_commands ? html`<a class="chip chip-info" href="${base}/commands">${plural(data.pending_commands, "command")} waiting</a>` : ""}</div>
      </div>
      ${enrolled && can("act") ? html`<div class="actions">
        <button class="btn btn-primary" data-a="command">Send command</button>
        <button class="btn" data-a="sync">Refresh</button>
        <button class="btn" data-a="lock">Lock</button>
        ${d.user_enrollment ? "" : d.lost_mode ? html`<button class="btn" data-a="lostoff">Leave Lost Mode</button>` : html`<button class="btn" data-a="loston">Lost Mode</button>`}
        <button class="btn" data-a="more">Manage</button></div>` : ""}
      ${!enrolled && can("manage") ? html`<div class="actions"><button class="btn btn-danger" data-a="forget">Delete record</button></div>` : ""}
    </div>
    <nav class="tabs" aria-label="Device sections">${TABS.map(([id, label]) => html`<a href="${base}/${id}" aria-selected="${id === tab}">${label}</a>`)}</nav>
    <div id="tab"></div>`.s;

  const reload = () => ctx.refresh();
  $$("[data-a]", root).forEach((b) => b.addEventListener("click", () => actions[b.dataset.a]?.()));
  const action = async (name, label) => {
    try {
      const res = await api.post(`/api/devices/${encodeURIComponent(udid)}/actions/${name}`);
      toast(label || "Done");
      return res;
    } catch (e) { toastError(e); }
  };
  const actions = {
    command: () => commandDialog([udid], { onDone: reload, supervised: d.supervised, userEnrollment: d.user_enrollment }),
    sync: () => action("sync", "Inventory refresh queued. The device reports back within a minute when it's online."),
    lock: () => commandDialog([udid], { only: ["DeviceLock"], preselect: "DeviceLock", onDone: reload }),
    loston: () => commandDialog([udid], { only: ["EnableLostMode"], preselect: "EnableLostMode", onDone: reload, supervised: d.supervised }),
    lostoff: () => commandDialog([udid], { only: ["DisableLostMode"], preselect: "DisableLostMode", onDone: reload }),
    forget: async () => {
      if (!(await confirmDialog("Delete this device record?", "The device is no longer enrolled. Its history, telemetry and command log will be deleted.", { danger: true, confirmLabel: "Delete record" }))) return;
      try { await api.del(`/api/devices/${encodeURIComponent(udid)}`); toast("Device record deleted"); navigate("/devices"); } catch (e) { toastError(e); }
    },
    more: () => manageDialog(udid, d, data, reload, navigate),
  };

  const host = $("#tab", root);
  const tabs = { overview, telemetry, location, apps, profiles, certificates, configuration, commands, activity };
  await (tabs[tab] || overview)(host, d, data, { udid, reload, base });
}

function manageDialog(udid, d, data, reload, navigate) {
  const run = async (name, label, confirmText) => {
    if (confirmText && !(await confirmDialog(confirmText[0], confirmText[1], { danger: true, confirmLabel: confirmText[2] }))) return false;
    try { await api.post(`/api/devices/${encodeURIComponent(udid)}/actions/${name}`); toast(label); reload(); } catch (e) { toastError(e); }
  };
  const items = [
    ["push", "Wake the device now", "Send an APNs notification so the device checks in immediately.", () => run("push", "Push sent")],
    ["telemetry", "Sample battery and network", "Request a quick battery, storage and carrier reading.", () => run("telemetry", "Sample requested")],
    ["reconcile", "Re-apply assigned configuration", "Check assigned profiles, apps and declarations and queue anything missing.", () => run("reconcile", "Configuration check queued")],
    ["retry", "Retry failed items", "Forget failures so profiles and apps are tried again.", () => run("retry-failed", "Failed items will be retried")],
    ["clear", "Clear the command queue", "Cancel every command that hasn't been delivered yet.", () => run("clear-queue", "Queue cleared", ["Cancel all waiting commands?", "Commands not yet delivered to the device will be canceled.", "Cancel commands"])],
  ];
  if (can("manage")) {
    items.push(
      ["secrets", "Show recovery codes", "Activation Lock bypass code and companion-app token. Viewing is logged.", async () => {
        const s = await api.get(`/api/devices/${encodeURIComponent(udid)}/secrets`);
        modal({ title: "Recovery codes", body: html`<dl class="kv">
          <dt>Activation Lock bypass</dt><dd>${s.activation_lock_bypass ? html`<span class="ident">${s.activation_lock_bypass}</span>` : html`<span class="muted">Not retrieved yet. Send “Activation Lock bypass code” (supervised devices).</span>`}</dd>
          <dt>Unlock token escrowed</dt><dd>${yes(s.has_unlock_token)} <span class="hint">(lets you clear a forgotten passcode)</span></dd>
          <dt>Companion app token</dt><dd><span class="ident">${s.agent_token}</span></dd>
          <dt>Company Portal link</dt><dd><a href="${s.portal_url}" target="_blank" rel="noopener">${s.portal_url}</a></dd></dl>` });
        return false;
      }],
      ...(d.user_enrollment ? [] : [["renew", "Renew device identity", `Issue a new identity certificate (current one expires ${fmtDate(d.cert_not_after)}).`, () => run("renew-identity", "Identity renewal sent", ["Renew the identity certificate?", "A fresh enrollment profile is installed silently to replace the device certificate.", "Renew"])]]),
      ["unenroll", "Remove management", d.user_enrollment
        ? "Removes the work account. iOS deletes only the work apps, accounts and data; personal data is untouched."
        : "Removes the management profile. Managed apps and profiles flagged for removal go with it; personal data stays. This is not a wipe.", () =>
        run("unenroll", "Unenroll command sent", ["Remove management from this device?", "The device stops being managed. Its personal data stays on it; managed apps, profiles and accounts are removed. You'd need to enroll it again to manage it.", "Remove management"])],
    );
  }
  const ctl = modal({ title: "Manage device", body: html`<div class="picker">${items.map(([id, label, desc]) => html`<button type="button" data-m="${id}">${label}<small>${desc}</small></button>`)}</div>
    <p class="hint" style="margin-top:14px">Orchard MDM deliberately has no remote wipe: the enrollment profile does not grant the erase right, and erase commands are refused.</p>` });
  $$("[data-m]", ctl.el).forEach((b) => b.addEventListener("click", async () => {
    const item = items.find((i) => i[0] === b.dataset.m);
    const keep = await item[3]();
    if (keep !== false) ctl.close();
  }));
}

// ---------- tabs ----------

async function overview(host, d, data, { udid, reload }) {
  const info = d.info || {};
  const sec = d.security || {};
  const loc = d.location;
  host.innerHTML = html`<div class="grid-2">
    <section class="panel"><div class="panel-head"><h2>Device</h2>${can("act") ? html`<button class="btn btn-sm" id="edit">Edit details</button>` : ""}</div><div class="panel-pad"><dl class="kv">
      <dt>Name</dt><dd>${v(d.device_name)}</dd>
      <dt>Model</dt><dd>${v(modelLabel(d.product_name))}${d.product_name && modelLabel(d.product_name) !== d.product_name ? html` <span class="hint">${d.product_name}</span>` : ""}${d.model ? html` <span class="hint">${d.model}</span>` : ""}</dd>
      <dt>iOS</dt><dd>${v(d.os_version)} ${d.build_version ? html`<span class="hint">(${d.build_version})</span>` : ""}</dd>
      ${d.user_enrollment ? html`<dt>Enrollment ID</dt><dd class="ident">${d.udid}</dd>
      <dt>Work account</dt><dd>${d.managed_apple_id}</dd>
      <dt>Device identifiers</dt><dd class="muted">Hidden: personal device enrolled with User Enrollment</dd>`
      : html`<dt>Serial number</dt><dd class="ident">${v(d.serial_number)}</dd>
      <dt>UDID</dt><dd class="ident">${d.udid}</dd>${d.managed_apple_id ? html`<dt>Work account</dt><dd>${d.managed_apple_id}</dd>` : ""}`}
      ${d.imei ? html`<dt>IMEI</dt><dd class="ident">${d.imei}</dd>` : ""}
      <dt>Assigned user</dt><dd>${v(d.assigned_user)}${d.assigned_email ? html` <span class="hint">${d.assigned_email}</span>` : ""}</dd>
      <dt>Asset tag</dt><dd>${v(d.asset_tag)}</dd>
      <dt>Tags</dt><dd>${d.tags.length ? d.tags.map((t) => html`<span class="tag">${t}</span>`) : v("")}</dd>
      <dt>Groups</dt><dd>${data.groups.length ? data.groups.map((g) => html`<a class="tag" href="#/groups/${g.id}">${g.name}</a>`) : v("")}</dd>
      ${d.notes ? html`<dt>Notes</dt><dd style="white-space:pre-wrap">${d.notes}</dd>` : ""}
    </dl></div></section>
    <section class="panel"><div class="panel-head"><h2>Status</h2></div><div class="panel-pad"><dl class="kv">
      <dt>Last check-in</dt><dd>${ago(d.last_seen)} <span class="hint">${fmtTime(d.last_seen)}</span></dd>
      <dt>Last inventory</dt><dd>${ago(d.last_inventory)}</dd>
      <dt>Battery</dt><dd>${pct(d.battery_level)}${d.battery_state ? `, ${d.battery_state}` : ""}${d.battery_health ? html` <span class="hint">health: ${d.battery_health}</span>` : ""}</dd>
      <dt>Storage</dt><dd>${d.capacity_gb ? html`${gb(d.available_gb)} free of ${gb(d.capacity_gb)}` : v("")}</dd>
      <dt>Compliance</dt><dd>${complianceChip(d.compliance)}${d.compliance_reasons?.length ? html`<ul style="margin:6px 0 0;padding-left:18px">${d.compliance_reasons.map((r) => html`<li>${r}</li>`)}</ul>` : ""}</dd>
      <dt>Enrolled</dt><dd>${fmtDate(d.enrolled_at)} <span class="hint">${enrollTypeLabel(d.enrollment_type)}${data.enrollment_token ? `: ${data.enrollment_token.name}` : ""}</span></dd>
      <dt>Push</dt><dd>${d.has_push_token ? `Registered, last sent ${ago(d.last_push)}` : "No push token"}</dd>
      <dt>Location</dt><dd>${d.user_enrollment ? html`<span class="muted">Not shared by personal devices</span>` : loc ? html`<a href="#/devices/${encodeURIComponent(udid)}/location">${(+loc.latitude).toFixed(4)}, ${(+loc.longitude).toFixed(4)}</a> <span class="hint">${ago(d.location_at)}</span>` : html`<span class="muted">Only available in Lost Mode or through the companion app</span>`}</dd>
    </dl></div></section>
    <section class="panel"><div class="panel-head"><h2>Security</h2></div><div class="panel-pad"><dl class="kv">
      <dt>Passcode</dt><dd>${d.passcode_present ? "Set" : "Not set"}${d.passcode_present ? (d.passcode_compliant ? ", meets policy" : ", does not meet policy") : ""}</dd>
      <dt>Data protection</dt><dd>${d.encryption_caps & 2 && d.passcode_present ? "Active" : d.encryption_caps ? "Available (needs a passcode)" : v("")}</dd>
      ${d.user_enrollment ? "" : html`<dt>Supervised</dt><dd>${yes(d.supervised)}</dd>`}
      ${d.user_enrollment ? "" : html`<dt>Activation Lock</dt><dd>${d.activation_lock ? "On" : "Off"}${data.has_bypass_code ? html` <span class="hint">bypass code stored</span>` : ""}</dd>
      <dt>Find My</dt><dd>${d.find_my ? "On" : "Off"}</dd>`}
      ${d.user_enrollment ? "" : html`<dt>Enrolled via ADE</dt><dd>${yes(d.dep_enrolled)}</dd>
      <dt>Passcode clearable</dt><dd>${data.has_unlock_token ? "Yes (unlock token escrowed)" : "No"}</dd>`}
      <dt>Identity certificate</dt><dd>expires ${fmtDate(d.cert_not_after)}</dd>
      ${sec.PasscodeLockGracePeriodEnforced !== undefined ? html`<dt>Lock grace period</dt><dd>${sec.PasscodeLockGracePeriodEnforced} s</dd>` : ""}
    </dl></div></section>
    <section class="panel"><div class="panel-head"><h2>Network</h2></div><div class="panel-pad">${d.user_enrollment ? html`<dl class="kv">
      <dt>Public IP</dt><dd class="ident">${v(d.last_ip)}</dd></dl>
      <p class="hint">Wi-Fi, carrier, phone number and hardware addresses aren't collected from personal devices.</p>` : html`<dl class="kv">
      <dt>Public IP</dt><dd class="ident">${v(d.last_ip)}</dd>
      <dt>Wi-Fi network</dt><dd>${d.ssid ? html`${d.ssid} <span class="hint ident">${d.bssid}</span>` : html`<span class="muted">Reported by the companion app only</span>`}</dd>
      <dt>Wi-Fi MAC</dt><dd class="ident">${v(d.wifi_mac)}</dd>
      <dt>Bluetooth MAC</dt><dd class="ident">${v(d.bluetooth_mac)}</dd>
      <dt>Carrier</dt><dd>${v(d.carrier)}${d.carrier && d.cellular_technology && d.cellular_technology !== "None" ? html` <span class="hint">${d.cellular_technology}</span>` : ""}</dd>
      <dt>Phone number</dt><dd>${v(d.phone_number)}</dd>
      <dt>Roaming</dt><dd>${d.roaming ? "Roaming now" : "No"}${info.DataRoamingEnabled !== undefined ? html` <span class="hint">data roaming ${info.DataRoamingEnabled ? "allowed" : "off"}</span>` : ""}</dd>
      <dt>Personal Hotspot</dt><dd>${d.hotspot ? "On" : "Off"}</dd>
    </dl>`}</div></section>
  </div>
  ${d.os_updates?.length ? html`<section class="panel" style="margin-top:16px"><div class="panel-head"><h2>Available updates</h2>${can("act") ? html`<button class="btn btn-sm" id="upd">Update iOS</button>` : ""}</div>
    <div class="table-wrap"><table class="table"><thead><tr><th>Update</th><th>Version</th><th>Build</th><th>Restart</th></tr></thead><tbody>
    ${d.os_updates.map((u) => html`<tr><td>${u.HumanReadableName || u.ProductName || u.ProductKey}</td><td>${u.Version}</td><td>${u.Build}</td><td>${yes(u.RestartRequired)}</td></tr>`)}</tbody></table></div></section>` : ""}
  ${Object.keys(info).length ? html`<details class="panel panel-pad" style="margin-top:16px"><summary><strong>All reported device information</strong></summary><pre class="code" style="margin-top:12px">${JSON.stringify(info, null, 2)}</pre></details>` : ""}`.s;
  $("#upd", host)?.addEventListener("click", () => commandDialog([udid], { only: ["ScheduleOSUpdate"], preselect: "ScheduleOSUpdate", onDone: reload }));
  $("#edit", host)?.addEventListener("click", () => modal({
    title: "Edit details",
    body: html`<div class="inline-fields">
      <label class="field"><span>Assigned user</span><input type="text" name="assigned_user" value="${d.assigned_user}"></label>
      <label class="field"><span>User email</span><input type="email" name="assigned_email" value="${d.assigned_email}"></label>
      <label class="field"><span>Asset tag</span><input type="text" name="asset_tag" value="${d.asset_tag}"></label>
      <label class="field"><span>Ownership</span><select name="ownership">${["corporate", "personal", "unknown"].map((o) => html`<option value="${o}" ${d.ownership === o ? "selected" : ""}>${ownershipLabel(o)}</option>`)}</select></label></div>
      <label class="field"><span>Tags</span><input type="text" name="tags" value="${d.tags.join(", ")}"><small class="hint">Comma separated. Tags can drive dynamic groups.</small></label>
      <label class="field"><span>Notes</span><textarea name="notes" rows="3">${d.notes}</textarea></label>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Save", kind: "primary", submit: true, onClick: async (c) => {
      const f = $("form", c.el);
      await api.patch(`/api/devices/${encodeURIComponent(udid)}`, { assigned_user: f.assigned_user.value, assigned_email: f.assigned_email.value, asset_tag: f.asset_tag.value,
        ownership: f.ownership.value, notes: f.notes.value, tags: f.tags.value.split(",").map((s) => s.trim()).filter(Boolean) });
      toast("Details saved"); reload();
    } }],
  }));
}

async function telemetry(host, d, data, { udid }) {
  host.innerHTML = html`<div class="toolbar"><label>Period <select id="hours">${[[24, "Last 24 hours"], [168, "Last 7 days"], [720, "Last 30 days"], [2160, "Last 90 days"]].map(([h, l]) => html`<option value="${h}" ${h === 168 ? "selected" : ""}>${l}</option>`)}</select></label>
      ${can("act") ? html`<button class="btn btn-sm" id="sample">Take a sample now</button>` : ""}</div>
    <div id="tel"></div>`.s;
  const load = async () => {
    const hours = $("#hours", host).value;
    const t = await api.get(`/api/devices/${encodeURIComponent(udid)}/telemetry?hours=${hours}`);
    const battery = t.items.filter((s) => s.battery >= 0).map((s) => ({ t: s.ts, v: Math.round(s.battery * 100) }));
    const storage = t.items.filter((s) => s.available_gb >= 0).map((s) => ({ t: s.ts, v: s.available_gb }));
    const tel = $("#tel", host);
    tel.innerHTML = html`<div class="grid-2">
      <section class="panel"><div class="panel-head"><h2>Battery level</h2><span class="hint">${plural(battery.length, "reading")}</span></div><div class="panel-pad"><div id="c-bat"></div></div></section>
      <section class="panel"><div class="panel-head"><h2>Free storage</h2><span class="hint">${plural(storage.length, "reading")}</span></div><div class="panel-pad"><div id="c-sto"></div></div></section></div>
      <section class="panel" style="margin-top:16px"><div class="panel-head"><h2>Networks seen</h2><span class="hint">Public IPs come from check-ins; Wi-Fi names need the companion app</span></div>
        ${t.networks.length ? html`<div class="table-wrap"><table class="table capped"><thead><tr><th>Type</th><th>Network</th><th>First seen</th><th>Last seen</th><th class="num">Samples</th></tr></thead><tbody>
          ${t.networks.map((n) => html`<tr><td>${{ ip: "Public IP", ssid: "Wi-Fi", carrier: "Carrier" }[n.kind]}</td><td class="${n.kind === "ip" ? "ident" : ""}">${n.value}</td><td>${fmtTime(n.first_seen)}</td><td>${fmtTime(n.last_seen)}</td><td class="num">${n.count}</td></tr>`)}
        </tbody></table></div>${showAll(t.networks.length, "network")}` : html`<p class="hint panel-pad" style="margin:0">No network information recorded in this period.</p>`}</section>
      <section class="panel" style="margin-top:16px"><div class="panel-head"><h2>Recent samples</h2></div><div class="table-wrap"><table class="table capped">
        <thead><tr><th>Time</th><th>Source</th><th class="num">Battery</th><th class="num">Free</th><th>Wi-Fi</th><th>IP</th><th>Carrier</th></tr></thead>
        <tbody>${t.items.slice(-40).reverse().map((s) => html`<tr><td class="nowrap">${fmtTime(s.ts)}</td><td>${{ mdm: "MDM inventory", server: "Check-in", agent: "Companion app", ddm: "Status report" }[s.source] || s.source}</td>
          <td class="num">${s.battery >= 0 ? pct(s.battery) : "—"}</td><td class="num">${s.available_gb >= 0 ? gb(s.available_gb) : "—"}</td><td>${v(s.ssid)}</td><td class="ident">${v(s.ip)}</td><td>${v(s.carrier)}${s.roaming ? " (roaming)" : ""}</td></tr>`)}</tbody></table></div>${showAll(Math.min(t.items.length, 40), "sample")}</section>`.s;
    lineChart($("#c-bat", tel), battery, { yMax: 100, format: (x) => `${Math.round(x)}%`, label: "Battery" });
    lineChart($("#c-sto", tel), storage, { format: (x) => `${x >= 10 || x === 0 ? Math.round(x) : x.toFixed(1)} GB`, label: "Free storage" });
  };
  $("#hours", host).addEventListener("change", load);
  host.addEventListener("click", (e) => {
    const b = e.target.closest("[data-showall]");
    if (!b) return;
    b.closest(".panel").querySelector("table.capped")?.classList.remove("capped");
    b.parentElement.remove();
  });
  $("#sample", host)?.addEventListener("click", async () => {
    try { await api.post(`/api/devices/${encodeURIComponent(udid)}/actions/telemetry`); toast("Sample requested. It appears here once the device responds."); } catch (e) { toastError(e); }
  });
  await load();
}

async function location(host, d, data, { udid, reload }) {
  const res = await api.get(`/api/devices/${encodeURIComponent(udid)}/locations?days=30`);
  const last = res.items[0];
  const bbox = last ? [last.longitude - 0.01, last.latitude - 0.006, last.longitude + 0.01, last.latitude + 0.006].map((n) => n.toFixed(5)).join(",") : "";
  host.innerHTML = html`<div class="callout"><p>iOS only shares location with MDM while a supervised device is in <strong>Lost Mode</strong>; Orchard then records a fix every few minutes.
      A companion app using the managed app configuration variables can also report location with the user's consent.</p></div>
    ${can("act") ? html`<div class="toolbar">${d.lost_mode ? html`<button class="btn btn-primary" id="locate">Locate now</button><button class="btn" id="lostoff">Leave Lost Mode</button>` : html`<button class="btn" id="loston">Enable Lost Mode</button>`}</div>` : ""}
    ${last ? html`<div class="grid-2"><section class="panel"><div class="panel-head"><h2>Last known position</h2><span class="hint">${ago(last.ts)}, ±${Math.round(last.accuracy)} m</span></div>
        <div class="panel-pad"><iframe class="mapframe" title="Map of the device's last position" loading="lazy" referrerpolicy="no-referrer"
          src="https://www.openstreetmap.org/export/embed.html?bbox=${bbox}&layer=mapnik&marker=${last.latitude},${last.longitude}"></iframe>
          <p class="hint row" style="gap:18px"><a href="https://www.openstreetmap.org/?mlat=${last.latitude}&mlon=${last.longitude}#map=17/${last.latitude}/${last.longitude}" target="_blank" rel="noopener">Open in OpenStreetMap</a>
          <a href="https://maps.apple.com/?ll=${last.latitude},${last.longitude}&q=${encodeURIComponent(deviceName(d))}" target="_blank" rel="noopener">Open in Apple Maps</a></p></div></section>
      <section class="panel"><div class="panel-head"><h2>History</h2><span class="hint">${plural(res.items.length, "fix", "fixes")} in 30 days</span></div>
        <div class="table-wrap" style="max-height:420px"><table class="table"><thead><tr><th>Time</th><th>Position</th><th class="num">Accuracy</th><th>Source</th></tr></thead><tbody>
        ${res.items.map((l) => html`<tr><td class="nowrap">${fmtTime(l.ts)}</td><td class="ident">${l.latitude.toFixed(5)}, ${l.longitude.toFixed(5)}</td><td class="num">${Math.round(l.accuracy)} m</td><td>${l.source === "agent" ? "Companion app" : "Lost Mode"}</td></tr>`)}
        </tbody></table></div></section></div>`
      : emptyState("No location recorded", d.supervised ? "Enable Lost Mode to locate this device." : "This device isn't supervised, so MDM can't request its location.")}`.s;
  const cmd = (id) => commandDialog([udid], { only: [id], preselect: id, onDone: reload, supervised: d.supervised });
  if (d.user_enrollment) {
    host.innerHTML = emptyState("Location isn't available", "This is a personal device enrolled with User Enrollment. iOS never shares its location with the organization.").s;
    return;
  }
  $("#loston", host)?.addEventListener("click", () => cmd("EnableLostMode"));
  $("#lostoff", host)?.addEventListener("click", () => cmd("DisableLostMode"));
  $("#locate", host)?.addEventListener("click", async () => {
    try { await api.post(`/api/devices/${encodeURIComponent(udid)}/actions/locate`); toast("Location requested"); } catch (e) { toastError(e); }
  });
}

async function apps(host, d, data, { udid }) {
  const res = await api.get(`/api/devices/${encodeURIComponent(udid)}/apps`);
  host.innerHTML = html`<div class="toolbar"><input type="search" id="aq" placeholder="Filter apps" style="max-width:320px">
      <label class="check"><input type="checkbox" id="managed-only"><span>Managed apps only</span></label><span class="spacer"></span>
      ${can("act") ? html`<button class="btn btn-sm" id="install">Install app</button>` : ""}</div>
    <section class="panel">${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>App</th><th>Version</th><th class="num">Size</th><th>Management</th><th></th></tr></thead>
      <tbody>${res.items.map((a) => html`<tr data-text="${(a.name + " " + a.bundle_id).toLowerCase()}" data-managed="${a.is_managed ? 1 : 0}"><td><strong>${a.name}</strong><span class="cell-sub ident" style="margin-left:0">${a.bundle_id}</span></td>
        <td>${a.short_version || a.version}</td><td class="num">${bytes(a.bundle_size + a.dynamic_size)}</td><td>${a.is_managed ? html`<span class="chip chip-good">Managed</span>${a.managed_status && a.managed_status !== "Managed" ? html`<span class="cell-sub">${a.managed_status}</span>` : ""}` : html`<span class="muted">User installed</span>`}</td>
        <td class="right">${a.is_managed && can("act") ? html`<button class="link-btn link-danger" data-rm="${a.bundle_id}">Remove</button>` : ""}</td></tr>`)}</tbody></table></div>`
      : emptyState("No app inventory yet", "Refresh the device to collect its installed apps.")}</section>`.s;
  const filter = () => {
    const q = $("#aq", host).value.toLowerCase(), m = $("#managed-only", host).checked;
    $$("tbody tr", host).forEach((tr) => (tr.style.display = tr.dataset.text.includes(q) && (!m || tr.dataset.managed === "1") ? "" : "none"));
  };
  $("#aq", host).addEventListener("input", filter);
  $("#managed-only", host).addEventListener("change", filter);
  $("#install", host)?.addEventListener("click", () => commandDialog([udid], { only: ["InstallApplication"], preselect: "InstallApplication" }));
  $$("[data-rm]", host).forEach((b) => b.addEventListener("click", async () => {
    if (!(await confirmDialog("Remove this app?", `${b.dataset.rm} and its data will be removed from the device.`, { danger: true, confirmLabel: "Remove app" }))) return;
    try { await api.post(`/api/devices/${encodeURIComponent(udid)}/commands`, { command: "RemoveApplication", params: { Identifier: b.dataset.rm } }); toast("Removal queued"); } catch (e) { toastError(e); }
  }));
}

async function profiles(host, d, data, { udid }) {
  const res = await api.get(`/api/devices/${encodeURIComponent(udid)}/profiles`);
  host.innerHTML = html`<section class="panel"><div class="panel-head"><h2>Installed on the device</h2>${can("act") ? html`<button class="btn btn-sm" id="install">Install profile</button>` : ""}</div>
    ${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Profile</th><th>Payloads</th><th>Source</th><th></th></tr></thead><tbody>
      ${res.items.map((p) => html`<tr><td><strong>${p.display_name || p.identifier}</strong><span class="cell-sub ident" style="margin-left:0">${p.identifier}</span></td>
        <td>${(p.payload_types || []).map((t) => html`<span class="tag">${t.replace("com.apple.", "")}</span>`)}</td>
        <td>${p.identifier === res.mdm_identifier ? "Enrollment" : p.is_managed ? "Managed" : "Installed by user"}</td>
        <td class="right">${p.is_managed && p.identifier !== res.mdm_identifier && can("act") ? html`<button class="link-btn link-danger" data-rm="${p.identifier}">Remove</button>` : ""}</td></tr>`)}
    </tbody></table></div>` : emptyState("No profile inventory yet", "Refresh the device to collect its installed profiles.")}</section>
    <section class="panel"><div class="panel-head"><h2>Assigned profiles</h2><span class="hint">Delivery state of profiles assigned through groups</span></div>
    ${res.managed.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Profile</th><th>Version</th><th>State</th><th>Updated</th></tr></thead><tbody>
      ${res.managed.map((s) => html`<tr><td><a href="#/profiles/${s.profile_id}">${s.profile_name}</a></td><td>${s.version}</td><td>${stateChip(s.status)}${s.error ? html`<span class="cell-sub">${s.error}</span>` : ""}</td><td>${ago(s.updated_at)}</td></tr>`)}
    </tbody></table></div>` : html`<p class="hint panel-pad" style="margin:0">No profiles are assigned to this device's groups.</p>`}</section>`.s;
  $("#install", host)?.addEventListener("click", () => commandDialog([udid], { only: ["InstallProfile"], preselect: "InstallProfile" }));
  $$("[data-rm]", host).forEach((b) => b.addEventListener("click", async () => {
    if (!(await confirmDialog("Remove this profile?", b.dataset.rm, { danger: true, confirmLabel: "Remove profile" }))) return;
    try { await api.post(`/api/devices/${encodeURIComponent(udid)}/commands`, { command: "RemoveProfile", params: { Identifier: b.dataset.rm } }); toast("Removal queued"); } catch (e) { toastError(e); }
  }));
}

async function certificates(host, d, data, { udid }) {
  const res = await api.get(`/api/devices/${encodeURIComponent(udid)}/certificates`);
  host.innerHTML = html`<section class="panel">${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Certificate</th><th>Issuer</th><th>Expires</th><th>Identity</th></tr></thead><tbody>
    ${res.items.map((c) => html`<tr><td><strong>${c.common_name}</strong><span class="cell-sub">${c.subject}</span></td><td>${c.issuer}</td>
      <td class="nowrap">${c.not_after ? html`${fmtDate(c.not_after)}${c.not_after * 1000 < Date.now() ? html` <span class="chip chip-bad">Expired</span>` : c.not_after * 1000 < Date.now() + 30 * 864e5 ? html` <span class="chip chip-warn">Soon</span>` : ""}` : "—"}</td><td>${yes(c.is_identity)}</td></tr>`)}
  </tbody></table></div>` : emptyState("No certificate inventory yet", "Refresh the device to collect installed certificates.")}</section>`.s;
}

async function configuration(host, d, data, { udid, reload }) {
  const [as, decl, comp] = await Promise.all([
    api.get(`/api/devices/${encodeURIComponent(udid)}/assignments`), api.get(`/api/devices/${encodeURIComponent(udid)}/declarations`), api.get(`/api/devices/${encodeURIComponent(udid)}/compliance`),
  ]);
  const link = (t, id) => ({ profile: `#/profiles/${id}`, app: `#/apps/${id}`, declaration: `#/declarations/${id}`, compliance: `#/compliance/${id}` })[t];
  const typeLabel = { profile: "Profile", app: "App", declaration: "Declaration", compliance: "Compliance policy" };
  const statusByID = Object.fromEntries((decl.status || []).map((s) => [s.identifier, s]));
  host.innerHTML = html`<section class="panel"><div class="panel-head"><h2>Assigned to this device</h2>${can("act") ? html`<button class="btn btn-sm" id="retry">Retry failed items</button>` : ""}</div>
    ${as.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Item</th><th>Type</th><th>Through group</th><th>Intent</th><th>State</th></tr></thead><tbody>
      ${as.items.map((a) => html`<tr><td><a href="${link(a.item_type, a.item_id)}">${a.name || a.item_type + " " + a.item_id}</a></td><td>${typeLabel[a.item_type]}</td><td>${a.group}</td><td>${intentLabel(a.intent, a.item_type)}</td>
        <td>${a.item_type === "profile" || a.item_type === "app" ? stateChip(a.status) : html`<span class="muted">—</span>`}${a.error ? html`<span class="cell-sub">${a.error}</span>` : ""}</td></tr>`)}
    </tbody></table></div>` : html`<p class="hint panel-pad" style="margin:0">Nothing is assigned to this device's groups yet.</p>`}</section>
    <section class="panel"><div class="panel-head"><h2>Compliance evaluation</h2>${complianceChip(comp.state)}</div>
      ${comp.policies.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Policy</th><th>Result</th><th>Why</th></tr></thead><tbody>
        ${comp.policies.map((p) => html`<tr><td><a href="#/compliance/${p.policy_id}">${p.policy_name}</a></td><td>${p.compliant ? html`<span class="chip chip-good">Passes</span>` : html`<span class="chip chip-bad">Fails</span>`}</td><td>${(p.reasons || []).join("; ") || "—"}</td></tr>`)}
      </tbody></table></div>` : html`<p class="hint panel-pad" style="margin:0">No compliance policies apply to this device.</p>`}</section>
    <section class="panel"><div class="panel-head"><h2>Declarative management</h2><span class="hint">${decl.supported ? `Last status report ${ago(decl.last_status)}` : "Needs iOS 16 or later"}</span></div>
      <div class="table-wrap"><table class="table"><thead><tr><th>Declaration</th><th>Type</th><th>Device reports</th></tr></thead><tbody>
      ${decl.items.map((it) => { const st = statusByID[it.Identifier]; return html`<tr><td>${it.name || it.Identifier}<span class="cell-sub ident" style="margin-left:0">${it.Identifier}</span></td><td>${it.Type.replace("com.apple.", "")}</td>
        <td>${st ? html`${st.active ? html`<span class="chip chip-good">Active</span>` : html`<span class="chip chip-warn">Inactive</span>`} ${st.valid === "invalid" ? html`<span class="chip chip-bad">Invalid</span>` : ""}${st.server_token && st.server_token !== it.ServerToken ? html`<span class="cell-sub">Older version on device</span>` : ""}` : html`<span class="muted">Not reported</span>`}</td></tr>`; })}
      </tbody></table></div>
      ${decl.status_items && Object.keys(decl.status_items).length ? html`<details class="panel-pad"><summary>Latest status report</summary><pre class="code" style="margin-top:10px">${JSON.stringify(decl.status_items, null, 2)}</pre></details>` : ""}
    </section>`.s;
  $("#retry", host)?.addEventListener("click", async () => {
    try { const r = await api.post(`/api/devices/${encodeURIComponent(udid)}/actions/retry-failed`); toast(`${r.reset} failed items reset; ${r.queued} commands queued`); reload(); } catch (e) { toastError(e); }
  });
}

function showAll(n, noun) {
  return n > 15 ? html`<div class="pager"><span>Showing 15 of ${plural(n, noun)}</span><button type="button" class="btn btn-sm" data-showall>Show all</button></div>` : "";
}

export async function showCommand(uuid) {
  const c = await api.get(`/api/commands/${encodeURIComponent(uuid)}`);
  const cmd = c.command;
  modal({ title: cmd.request_type, wide: true, body: html`<dl class="kv">
      <dt>Status</dt><dd>${commandChip(cmd.status)}${cmd.error_text ? html`<div class="hint">${cmd.error_text}</div>` : ""}</dd>
      <dt>Device</dt><dd><a href="#/devices/${encodeURIComponent(cmd.device_id)}">${cmd.device_name || cmd.device_id}</a></dd>
      <dt>Queued</dt><dd>${fmtTime(cmd.created_at)} <span class="hint">by ${cmd.created_by || cmd.source}</span></dd>
      <dt>Delivered</dt><dd>${cmd.sent_at ? fmtTime(cmd.sent_at) : "Not yet"}</dd>
      <dt>Completed</dt><dd>${cmd.completed_at ? fmtTime(cmd.completed_at) : "—"}</dd>
      <dt>Command UUID</dt><dd class="ident">${cmd.uuid}</dd></dl>
    <h3 style="margin-top:16px">Command</h3><pre class="code">${JSON.stringify(c.payload, null, 2)}</pre>
    ${c.result ? html`<h3 style="margin-top:16px">Device response</h3><pre class="code">${JSON.stringify(c.result, null, 2)}</pre>` : ""}`,
    actions: [
      ...(["Queued", "Sent", "NotNow"].includes(cmd.status) && can("act") ? [{ id: "cancel", label: "Cancel command", kind: "danger", onClick: async () => { await api.del(`/api/commands/${encodeURIComponent(uuid)}`); toast("Command canceled"); } }] : []),
    ] });
}

async function commands(host, d, data, { udid }) {
  const res = await api.get(`/api/devices/${encodeURIComponent(udid)}/commands?limit=200`);
  host.innerHTML = html`<section class="panel">${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Command</th><th>Status</th><th>Queued</th><th>Source</th><th>Completed</th></tr></thead><tbody>
    ${res.items.map((c) => html`<tr class="clickable" data-uuid="${c.uuid}" tabindex="0"><td><strong>${c.request_type}</strong>${c.error_text ? html`<span class="cell-sub">${c.error_text}</span>` : ""}</td><td>${commandChip(c.status)}</td>
      <td class="nowrap">${fmtTime(c.created_at)}</td><td>${c.created_by || c.source}</td><td class="nowrap">${c.completed_at ? ago(c.completed_at) : "—"}</td></tr>`)}
  </tbody></table></div><div class="pager"><span>${res.items.length} of ${res.total} shown</span></div>` : emptyState("No commands yet", "Commands you send, and the ones Orchard sends automatically, appear here.")}</section>`.s;
  $$("[data-uuid]", host).forEach((tr) => {
    const open = () => showCommand(tr.dataset.uuid).catch(toastError);
    tr.addEventListener("click", open);
    tr.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
  });
}

async function activity(host, d, data, { udid }) {
  const res = await api.get(`/api/devices/${encodeURIComponent(udid)}/events?limit=200`);
  host.innerHTML = html`<section class="panel panel-pad">${res.items.length ? html`<ul class="timeline">${res.items.map((e) => html`<li><time title="${fmtTime(e.ts)}">${ago(e.ts)}</time>
    <div class="${e.level === "warn" ? "lvl-warn" : ""}">${e.message}${e.details && !e.details.startsWith("{") ? html`<div class="hint" style="white-space:pre-wrap">${e.details}</div>` : ""}</div></li>`)}</ul>`
    : html`<p class="hint" style="margin:0">No activity recorded.</p>`}</section>`.s;
}

export { raw };
