import { html, $, $$, api, can, ago, fmtDate, modal, toast, toastError, confirmDialog, fileToBase64, plural, emptyState } from "../lib.js";
import { groups } from "../components.js";

export async function render({ root, refresh }) {
  const [srv, devs, profs, gs] = await Promise.all([api.get("/api/ade/servers"), api.get("/api/ade/devices"), api.get("/api/ade/profiles"), groups(true)]);
  const pname = Object.fromEntries(profs.items.map((p) => [p.id, p.name]));
  root.innerHTML = html`<div class="page-head"><div><h1>Automated Device Enrollment</h1><p class="sub">Devices bought through Apple or an authorized reseller enroll themselves during Setup Assistant, supervised and unremovable.
    Connect Apple Business Manager (or Apple School Manager) to use it.</p></div>
    ${can("admin") ? html`<div class="actions"><button class="btn btn-primary" id="add-srv">Connect Apple Business Manager</button></div>` : ""}</div>
    ${srv.items.length ? srv.items.map((s) => serverCard(s, profs.items)) : html`<section class="panel">${emptyState("Not connected yet", "Connect an Apple Business Manager MDM server to sync your organization's devices.")}</section>`}
    <h2 class="section">Setup Assistant profiles</h2>
    <section class="panel"><div class="panel-head"><h2>Profiles</h2>${can("manage") ? html`<button class="btn btn-sm" id="add-prof">New profile</button>` : ""}</div>
      ${profs.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Profile</th><th>Supervised</th><th>Removable</th><th>Skipped screens</th><th>Groups</th><th></th></tr></thead><tbody>
      ${profs.items.map((p) => html`<tr><td><strong>${p.name}</strong></td><td>${p.config.is_supervised === false ? "No" : "Yes"}</td><td>${p.config.is_mdm_removable ? "Yes" : "No"}</td>
        <td>${(p.config.skip_setup_items || []).length}</td><td>${p.group_ids.map((id) => html`<span class="tag">${gs.find((g) => g.id === id)?.name || id}</span>`)}</td>
        <td class="right">${can("manage") ? html`<button class="btn btn-sm btn-ghost" data-prof="${p.id}">Edit</button>` : ""}</td></tr>`)}</tbody></table></div>`
        : html`<p class="hint panel-pad" style="margin:0">Create a profile to control which Setup Assistant screens appear and which groups new devices join.</p>`}</section>
    <h2 class="section">Devices in Apple Business Manager</h2>
    <div id="bulk"></div>
    <section class="panel">${devs.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr>${can("manage") ? html`<th class="sel"><input type="checkbox" id="all" aria-label="Select all"></th>` : ""}<th>Serial number</th><th>Model</th><th>Profile</th><th>Status</th><th>Enrolled</th></tr></thead><tbody>
      ${devs.items.map((d) => html`<tr>${can("manage") ? html`<td class="sel"><input type="checkbox" value="${d.serial_number}" aria-label="Select ${d.serial_number}"></td>` : ""}
        <td class="ident">${d.serial_number}${d.asset_tag ? html`<span class="cell-sub">${d.asset_tag}</span>` : ""}</td><td>${d.model || d.description}<span class="cell-sub">${d.color}</span></td>
        <td>${d.assigned_profile_id ? pname[d.assigned_profile_id] || "Unknown" : d.profile_uuid ? "Set elsewhere" : html`<span class="muted">None</span>`}</td>
        <td>${{ assigned: "Assigned", pushed: "Pushed to device", empty: "No profile", removed: "Removed" }[d.profile_status] || d.profile_status || "—"}</td>
        <td>${d.enrolled_udid ? html`<a href="#/devices/${encodeURIComponent(d.enrolled_udid)}">View device</a>` : html`<span class="muted">Not yet</span>`}</td></tr>`)}
    </tbody></table></div>` : html`<p class="hint panel-pad" style="margin:0">No devices synced yet.</p>`}</section>`.s;

  const selected = () => $$("tbody .sel input:checked", root).map((i) => i.value);
  const renderBulk = () => {
    const ids = selected();
    $("#bulk", root).innerHTML = ids.length ? html`<div class="bulkbar"><strong>${plural(ids.length, "device")} selected</strong>
      <select id="bp" style="width:auto">${profs.items.map((p) => html`<option value="${p.id}">${p.name}</option>`)}</select>
      <button class="btn btn-sm btn-primary" id="assign" ${profs.items.length ? "" : "disabled"}>Assign profile</button><button class="btn btn-sm" id="unassign">Remove profile</button></div>`.s : "";
    $("#assign", root)?.addEventListener("click", async () => {
      try {
        const r = await api.post("/api/ade/assign", { profile_id: Number($("#bp", root).value), serials: ids });
        toast(r.error ? `Assigned ${r.assigned}; ${r.error}` : `Profile assigned to ${plural(r.assigned, "device")}`, r.error ? "err" : "ok");
        refresh();
      } catch (e) { toastError(e); }
    });
    $("#unassign", root)?.addEventListener("click", async () => {
      try { await api.post("/api/ade/unassign", { serials: ids }); toast("Profile removed"); refresh(); } catch (e) { toastError(e); }
    });
  };
  $("#all", root)?.addEventListener("change", (e) => { $$("tbody .sel input", root).forEach((i) => (i.checked = e.target.checked)); renderBulk(); });
  $$("tbody .sel input", root).forEach((i) => i.addEventListener("change", renderBulk));

  $("#add-srv", root)?.addEventListener("click", async () => {
    try { const s = await api.post("/api/ade/servers", { name: "Apple Business Manager" }); connectSteps(s, refresh); } catch (e) { toastError(e); }
  });
  $$("[data-srv-act]", root).forEach((b) => b.addEventListener("click", () => serverAction(b.dataset.srvAct, srv.items.find((s) => String(s.id) === b.dataset.id), refresh)));
  $$("[data-default]", root).forEach((sel) => sel.addEventListener("change", async () => {
    try { await api.put(`/api/ade/servers/${sel.dataset.default}`, { default_profile_id: Number(sel.value) }); toast("Default profile saved. New devices get it at the next sync."); } catch (e) { toastError(e); }
  }));
  $("#add-prof", root)?.addEventListener("click", () => editProfile(null, profs.skip_items, gs, refresh));
  $$("[data-prof]", root).forEach((b) => b.addEventListener("click", () => editProfile(profs.items.find((p) => String(p.id) === b.dataset.prof), profs.skip_items, gs, refresh)));
}

function serverCard(s, profiles) {
  const expires = s.access_token_expiry ? Math.round((s.access_token_expiry * 1000 - Date.now()) / 864e5) : null;
  return html`<section class="panel" style="margin-bottom:16px"><div class="panel-head"><h2>${s.server_name || s.name}</h2>
      <span class="row">${s.has_token ? (expires !== null && expires < 30 ? html`<span class="chip chip-warn">Token expires in ${expires} days</span>` : html`<span class="chip chip-good">Connected</span>`) : html`<span class="chip chip-warn">Waiting for token</span>`}</span></div>
    <div class="panel-pad"><div class="grid-2"><dl class="kv">
      <dt>Organization</dt><dd>${s.org_name || "—"}</dd><dt>Devices</dt><dd>${s.device_count}</dd><dt>Last sync</dt><dd>${s.last_sync ? ago(s.last_sync) : "Never"}</dd>
      <dt>Token expires</dt><dd>${s.access_token_expiry ? fmtDate(s.access_token_expiry) : "—"}</dd></dl>
      <div>${s.last_error ? html`<div class="callout bad"><p>${s.last_error}</p></div>` : ""}
        <label class="field"><span>Profile for new devices</span><select data-default="${s.id}" ${can("admin") ? "" : "disabled"}><option value="0">Don't assign automatically</option>${profiles.map((p) => html`<option value="${p.id}" ${p.id === s.default_profile_id ? "selected" : ""}>${p.name}</option>`)}</select></label>
        <div class="row">${can("manage") && s.has_token ? html`<button class="btn btn-sm btn-primary" data-srv-act="sync" data-id="${s.id}">Sync now</button>` : ""}
          ${can("admin") ? html`<button class="btn btn-sm" data-srv-act="token" data-id="${s.id}">${s.has_token ? "Renew token" : "Upload token"}</button><button class="btn btn-sm" data-srv-act="key" data-id="${s.id}">Public key</button><button class="btn btn-sm btn-ghost link-danger" data-srv-act="delete" data-id="${s.id}">Disconnect</button>` : ""}</div></div></div></div></section>`;
}

function uploadToken(s, refresh) {
  modal({ title: "Upload server token", body: html`<label class="field"><span>Server token (.p7m)</span><input type="file" name="f" accept=".p7m" required></label>
      <p class="hint">Download it in Apple Business Manager under Preferences → your MDM server → Download Token.</p>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "u", label: "Upload", kind: "primary", submit: true, onClick: async (c) => {
      const file = $("form", c.el).f.files[0];
      if (!file) { toast("Choose the .p7m file", "err"); return false; }
      const res = await api.post(`/api/ade/servers/${s.id}/token`, { data: await fileToBase64(file) });
      toast(`Connected to ${res.org_name || "Apple Business Manager"}. Syncing devices…`);
      setTimeout(refresh, 1500);
    } }] });
}

function connectSteps(s, refresh) {
  modal({ title: "Connect Apple Business Manager", body: html`<ol class="checklist" style="list-style:none">
      <li><span class="mark" style="background:var(--brand-wash)">1</span><div><strong>Download this server's public key.</strong><div><button type="button" class="btn btn-sm" id="pk">Download public key</button></div></div></li>
      <li><span class="mark" style="background:var(--brand-wash)">2</span><div><strong>Add an MDM server in Apple Business Manager.</strong><div class="hint">Preferences → Your MDM Servers → Add. Name it, then upload the public key.</div></div></li>
      <li><span class="mark" style="background:var(--brand-wash)">3</span><div><strong>Download the server token</strong> (a .p7m file) from the server you just created.</div></li>
      <li><span class="mark" style="background:var(--brand-wash)">4</span><div><strong>Upload the token here.</strong> Then assign devices to this MDM server in Apple Business Manager.</div></li></ol>`,
    onOpen: (c) => $("#pk", c.el).addEventListener("click", () => api.download("GET", `/api/ade/servers/${s.id}/publickey`, undefined, "publickey.pem").catch(toastError)),
    actions: [{ id: "later", label: "Finish later", onClick: () => refresh() }, { id: "up", label: "Upload token", kind: "primary", onClick: () => { uploadToken(s, refresh); } }] });
}

async function serverAction(act, s, refresh) {
  try {
    if (act === "sync") { const r = await api.post(`/api/ade/servers/${s.id}/sync`); toast(`Synced: ${r.added} added, ${r.modified} updated, ${r.removed} removed${r.assigned ? `, ${r.assigned} assigned` : ""}`); refresh(); }
    if (act === "key") await api.download("GET", `/api/ade/servers/${s.id}/publickey`, undefined, "publickey.pem");
    if (act === "token") uploadToken(s, refresh);
    if (act === "delete") {
      if (!(await confirmDialog("Disconnect this server?", "Its device records are removed from Orchard. Enrolled devices stay enrolled.", { danger: true, confirmLabel: "Disconnect" }))) return;
      await api.del(`/api/ade/servers/${s.id}`); toast("Disconnected"); refresh();
    }
  } catch (e) { toastError(e); }
}

const SKIP_LABELS = { AppleID: "Apple Account", Biometric: "Face ID / Touch ID", Diagnostics: "App Analytics", Location: "Location Services", Passcode: "Passcode", Payment: "Apple Pay",
  Privacy: "Data & Privacy", Restore: "Apps & Data (restore)", ScreenTime: "Screen Time", SIMSetup: "Cellular setup", Siri: "Siri", SoftwareUpdate: "Software update", TOS: "Terms and conditions",
  Welcome: "Get started", Appearance: "Appearance", Keyboard: "Keyboard", DeviceToDeviceMigration: "Transfer from iPhone", Safety: "Safety", Intelligence: "Apple Intelligence", TermsOfAddress: "Terms of address",
  MessagingActivationUsingPhoneNumber: "iMessage & FaceTime", iMessageAndFaceTime: "iMessage & FaceTime", Accessibility: "Accessibility", CameraButton: "Camera Control", EnableLockdownMode: "Lockdown Mode",
  RestoreCompleted: "Restore completed", UpdateCompleted: "Update completed", WatchMigration: "Apple Watch", TapToSetup: "Tap to set up", Wallpaper: "Wallpaper", AppStore: "App Store", iCloudDiagnostics: "iCloud Analytics", iCloudStorage: "iCloud storage", FileVault: "FileVault (Mac)", ScreenSaver: "Screen saver (Mac)" };

function editProfile(p, skipItems, gs, refresh) {
  const isNew = !p;
  p = p || { name: "", config: { is_supervised: true, is_mandatory: true, is_mdm_removable: false, await_device_configured: true, allow_pairing: true, skip_setup_items: ["AppleID", "Siri", "Diagnostics", "ScreenTime", "Privacy"] }, group_ids: [] };
  const c = p.config;
  const skip = new Set(c.skip_setup_items || []);
  const statics = gs.filter((g) => g.kind === "static");
  modal({ title: isNew ? "New Setup Assistant profile" : "Edit Setup Assistant profile", wide: true,
    body: html`<label class="field"><span>Name</span><input type="text" name="name" value="${p.name}" required></label>
      <div class="grid-2"><div>
        <label class="check"><input type="checkbox" name="is_supervised" ${c.is_supervised !== false ? "checked" : ""}><span>Supervise devices<small>Unlocks Lost Mode, silent app installs and supervised restrictions.</small></span></label>
        <label class="check"><input type="checkbox" name="is_mandatory" ${c.is_mandatory !== false ? "checked" : ""}><span>Enrollment can't be skipped</span></label>
        <label class="check"><input type="checkbox" name="is_mdm_removable" ${c.is_mdm_removable ? "checked" : ""}><span>Users may remove management</span></label>
        <label class="check"><input type="checkbox" name="await_device_configured" ${c.await_device_configured ? "checked" : ""}><span>Hold Setup Assistant until apps and profiles are queued</span></label>
        <label class="check"><input type="checkbox" name="allow_pairing" ${c.allow_pairing !== false ? "checked" : ""}><span>Allow pairing with computers</span></label>
        <label class="field" style="margin-top:12px"><span>Department</span><input type="text" name="department" value="${c.department || ""}"></label>
        <div class="inline-fields"><label class="field"><span>Support phone</span><input type="text" name="support_phone_number" value="${c.support_phone_number || ""}"></label>
          <label class="field"><span>Support email</span><input type="email" name="support_email_address" value="${c.support_email_address || ""}"></label>
          <label class="field"><span>Language</span><input type="text" name="language" value="${c.language || ""}" placeholder="e.g. en"></label>
          <label class="field"><span>Region</span><input type="text" name="region" value="${c.region || ""}" placeholder="e.g. JP"></label></div>
        <div class="field"><span class="field-label">Add enrolled devices to</span>${statics.length ? statics.map((g) => html`<label class="check"><input type="checkbox" name="g" value="${g.id}" ${p.group_ids.includes(g.id) ? "checked" : ""}><span>${g.name}</span></label>`) : html`<span class="hint">No static groups yet.</span>`}</div>
      </div><div><div class="field-label" style="margin-bottom:6px">Skip these Setup Assistant screens</div>
        ${skipItems.map((s) => html`<label class="check"><input type="checkbox" name="skip" value="${s}" ${skip.has(s) ? "checked" : ""}><span>${SKIP_LABELS[s] || s}</span></label>`)}</div></div>`,
    actions: [
      ...(isNew ? [] : [{ id: "del", label: "Delete", kind: "danger", onClick: async () => {
        if (!(await confirmDialog("Delete this profile?", "Devices keep whatever profile Apple already has for them.", { danger: true, confirmLabel: "Delete" }))) return false;
        await api.del(`/api/ade/profiles/${p.id}`); toast("Profile deleted"); refresh();
      } }]),
      { id: "c", label: "Cancel" },
      { id: "s", label: "Save", kind: "primary", submit: true, onClick: async (ctl) => {
        const f = $("form", ctl.el);
        const config = { ...c, is_supervised: f.is_supervised.checked, is_mandatory: f.is_mandatory.checked, is_mdm_removable: f.is_mdm_removable.checked,
          await_device_configured: f.await_device_configured.checked, allow_pairing: f.allow_pairing.checked, skip_setup_items: $$('input[name="skip"]:checked', ctl.el).map((i) => i.value) };
        for (const k of ["department", "support_phone_number", "support_email_address", "language", "region"]) { if (f[k].value.trim()) config[k] = f[k].value.trim(); else delete config[k]; }
        const body = { name: f.name.value, config, group_ids: $$('input[name="g"]:checked', ctl.el).map((i) => Number(i.value)) };
        if (isNew) await api.post("/api/ade/profiles", body); else await api.put(`/api/ade/profiles/${p.id}`, body);
        toast("Profile saved. It's uploaded to Apple when you assign it to devices.");
        refresh();
      } },
    ] });
}
