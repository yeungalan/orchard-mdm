import { html, $, $$, api, can, session, ago, fmtDate, fmtTime, modal, toast, toastError, confirmDialog, fileToBase64, plural, copyText, emptyState, bytes } from "../lib.js";

const TABS = [
  ["general", "General", "read"], ["schedule", "Schedule & data", "read"], ["push", "Apple Push", "read"], ["vpp", "Apps and Books", "read"],
  ["users", "Administrators", "admin"], ["apikeys", "API keys", "admin"], ["webhooks", "Webhooks", "read"], ["about", "Certificates & about", "read"],
];

export async function render(ctx) {
  const { root, params } = ctx;
  const tab = params.tab || "general";
  root.innerHTML = html`<div class="page-head"><div><h1>Settings</h1></div></div>
    <nav class="tabs" aria-label="Settings sections">${TABS.filter((t) => can(t[2])).map(([id, label]) => html`<a href="#/settings/${id}" aria-selected="${id === tab}">${label}</a>`)}</nav>
    <div id="tab"></div>`.s;
  const host = $("#tab", root);
  const pages = { general, schedule, push, vpp, users, apikeys, webhooks, about };
  await (pages[tab] || general)(host, ctx);
}

const ro = () => (can("admin") ? "" : "disabled");

async function loadSettings() { return api.get("/api/settings"); }

async function saveSettings(form, keys) {
  const body = {};
  for (const k of keys) {
    const el = form.elements[k];
    if (!el) continue;
    body[k] = el.type === "checkbox" ? (el.checked ? "1" : "0") : el.value;
  }
  await api.put("/api/settings", body);
  toast("Settings saved");
}

async function general(host, ctx) {
  const s = await loadSettings();
  const v = s.settings;
  const on = (k) => (v[k] === "1" || v[k] === "true" ? "checked" : "");
  host.innerHTML = html`<form class="stack" id="f" style="max-width:820px">
    <section class="panel panel-pad"><h3>Organization</h3>
      <div class="inline-fields"><label class="field"><span>Organization name</span><input type="text" name="org_name" value="${v.org_name}" ${ro()}></label>
        <label class="field"><span>Public server URL</span><input type="url" name="public_url" value="${s.public_url_locked ? s.effective_public_url : v.public_url}" ${s.public_url_locked ? "disabled" : ro()} placeholder="https://mdm.example.com">
          <small class="hint">${s.public_url_locked ? "Set by the -url flag." : "The HTTPS address devices use. Changing it after enrolling devices breaks their enrollment."}</small></label></div>
      <div class="inline-fields"><label class="field"><span>Support email</span><input type="email" name="support_email" value="${v.support_email}" ${ro()}></label>
        <label class="field"><span>Support phone</span><input type="text" name="support_phone" value="${v.support_phone}" ${ro()}></label>
        <label class="field"><span>Help center URL</span><input type="url" name="support_url" value="${v.support_url}" ${ro()}></label></div>
      <p class="hint" style="margin:0">Support details appear on the enrollment page and in the Company Portal.</p></section>
    <section class="panel panel-pad"><h3>Enrollment</h3>
      <label class="check"><input type="checkbox" name="enroll_require_token" ${on("enroll_require_token")} ${ro()}><span>Require an enrollment link or code<small>Recommended. When off, anyone who reaches the enrollment page can enroll a device and receive profiles assigned to All Devices.</small></span></label>
      <label class="check"><input type="checkbox" name="portal_enabled" ${on("portal_enabled")} ${ro()}><span>Company Portal<small>Self-service page on each device: status, compliance and optional apps.</small></span></label>
      <label class="check"><input type="checkbox" name="sign_profiles" ${on("sign_profiles")} ${ro()}><span>Sign profiles with the server's TLS certificate</span></label>
      <label class="field" style="margin-top:10px"><span>Consent text shown during enrollment</span><textarea name="enroll_consent_text" rows="3" ${ro()}>${v.enroll_consent_text}</textarea></label>
      <label class="field"><span>Extra trusted certificates (PEM)</span><textarea name="extra_trust_certs" class="code" rows="4" ${ro()} placeholder="-----BEGIN CERTIFICATE-----">${v.extra_trust_certs}</textarea>
        <small class="hint">Root certificates added to the enrollment profile, e.g. for a private TLS CA.</small></label>
      <label class="field"><span>Devices with no compliance policy count as</span><select name="compliance_default" ${ro()}><option value="compliant" ${v.compliance_default !== "noncompliant" ? "selected" : ""}>Compliant</option><option value="noncompliant" ${v.compliance_default === "noncompliant" ? "selected" : ""}>Not compliant</option></select></label>
      <label class="check"><input type="checkbox" name="ade_allow_unknown_serials" ${on("ade_allow_unknown_serials")} ${ro()}><span>Accept Automated Device Enrollment from serial numbers not yet synced</span></label></section>
    ${can("admin") ? html`<div><button class="btn btn-primary" type="submit">Save settings</button></div>` : ""}</form>`.s;
  $("#f", host).addEventListener("submit", async (e) => {
    e.preventDefault();
    const keys = ["org_name", "support_email", "support_phone", "support_url", "enroll_require_token", "portal_enabled", "sign_profiles", "enroll_consent_text", "extra_trust_certs", "compliance_default", "ade_allow_unknown_serials"];
    if (!s.public_url_locked) keys.push("public_url");
    try { await saveSettings(e.target, keys); } catch (err) { toastError(err); }
  });
}

async function schedule(host) {
  const s = await loadSettings();
  const v = s.settings;
  const num = (k, label, help, unit) => html`<label class="field"><span>${label}</span><input type="number" min="0" name="${k}" value="${v[k]}" ${ro()}><small class="hint">${help}${unit ? ` (${unit})` : ""}</small></label>`;
  host.innerHTML = html`<form class="stack" id="f" style="max-width:820px">
    <section class="panel panel-pad"><h3>Collecting device data</h3><div class="inline-fields">
      ${num("inventory_interval_hours", "Full inventory every", "Apps, profiles, certificates and security details", "hours")}
      ${num("telemetry_interval_minutes", "Battery and network sample every", "Battery, storage, carrier and roaming; 0 turns sampling off", "minutes")}
      ${num("lost_mode_location_minutes", "Locate devices in Lost Mode every", "0 turns automatic locating off", "minutes")}
      ${num("portal_agent_report_seconds", "Companion app reports every", "Suggested interval returned to the companion app", "seconds")}
      ${num("ade_sync_minutes", "Sync Apple Business Manager every", "", "minutes")}
      ${num("scep_validity_days", "Device identity valid for", "Certificates issued at enrollment", "days")}</div>
      <label class="check"><input type="checkbox" name="record_connection_ips" ${v.record_connection_ips === "1" ? "checked" : ""} ${ro()}><span>Record the public IP address devices connect from</span></label></section>
    <section class="panel panel-pad"><h3>Retention</h3><div class="inline-fields">
      ${num("command_expiry_days", "Give up on undelivered commands after", "", "days")}
      ${num("command_retention_days", "Keep command and activity history for", "", "days")}
      ${num("telemetry_retention_days", "Keep battery, network and location history for", "", "days")}</div></section>
    ${can("admin") ? html`<div><button class="btn btn-primary" type="submit">Save settings</button></div>` : ""}</form>`.s;
  $("#f", host).addEventListener("submit", async (e) => {
    e.preventDefault();
    try { await saveSettings(e.target, ["inventory_interval_hours", "telemetry_interval_minutes", "lost_mode_location_minutes", "portal_agent_report_seconds", "ade_sync_minutes", "scep_validity_days", "record_connection_ips", "command_expiry_days", "command_retention_days", "telemetry_retention_days"]); } catch (err) { toastError(err); }
  });
}

async function push(host, ctx) {
  const st = await api.get("/api/apns");
  const c = st.certificate;
  const admin = can("admin");
  host.innerHTML = html`<div class="stack" style="max-width:960px">
    <section class="panel"><div class="panel-head"><h2>Push certificate</h2>${c.configured ? (c.days_left > 30 ? html`<span class="chip chip-good">Active</span>` : html`<span class="chip chip-bad">Expires in ${c.days_left} days</span>`) : html`<span class="chip chip-warn">Not set up</span>`}</div>
      <div class="panel-pad">${c.configured ? html`<dl class="kv"><dt>Topic</dt><dd class="ident">${c.topic}</dd><dt>Expires</dt><dd>${fmtDate(c.not_after)} (${c.days_left} days)</dd><dt>Apple Account used</dt><dd>${c.apple_id || html`<span class="muted">Not recorded. Note it, you'll need the same account to renew.</span>`}</dd></dl>
        ${admin ? html`<div class="row" style="margin-top:12px"><button class="btn btn-sm" id="test">Send a test push</button></div>` : ""}`
        : html`<p style="margin-top:0">Apple requires an MDM push certificate before devices can enroll. It's free and takes a few minutes.</p>`}</div></section>
    ${admin ? html`<section class="panel"><div class="panel-head"><h2>${c.configured ? "Renew or replace" : "Get a push certificate"}</h2></div><div class="panel-pad">
      <ol class="checklist" style="list-style:none">
        <li><span class="mark" style="background:var(--brand-wash)">1</span><div style="flex:1"><strong>Create a certificate request</strong>
          <p class="hint" style="margin:2px 0 8px">${st.pending_csr ? "A request is ready. Creating a new one replaces it." : "Orchard keeps the private key; only the request leaves the server."}</p>
          <div class="row"><input type="email" id="csr-email" placeholder="Your email" style="max-width:260px"><button class="btn btn-sm" id="csr">Create request</button>${st.pending_csr ? html`<button class="btn btn-sm btn-ghost" id="csr-dl">Download request</button>` : ""}</div></div></li>
        <li><span class="mark" style="background:var(--brand-wash)">2</span><div style="flex:1"><strong>Have the request signed by an MDM vendor</strong>
          <p class="hint" style="margin:2px 0 8px">Either sign it with your own MDM vendor certificate from the Apple Developer Program, or use the free mdmcert.download service.</p>
          <div class="row"><button class="btn btn-sm" id="vendor" ${st.pending_csr ? "" : "disabled"}>Sign with vendor certificate</button><button class="btn btn-sm" id="mdmcert" ${st.pending_csr ? "" : "disabled"}>Use mdmcert.download</button><button class="btn btn-sm btn-ghost" id="decrypt">Decrypt mdmcert.download reply</button></div></div></li>
        <li><span class="mark" style="background:var(--brand-wash)">3</span><div style="flex:1"><strong>Upload the signed request to Apple</strong>
          <p class="hint" style="margin:2px 0 0">At <a href="https://identity.apple.com/pushcert/" target="_blank" rel="noopener">identity.apple.com/pushcert</a>, sign in with a company Apple Account, choose ${c.configured ? html`<strong>Renew</strong> next to the existing certificate` : "Create a Certificate"}, upload the file, and download the .pem certificate.</p></div></li>
        <li><span class="mark" style="background:var(--brand-wash)">4</span><div style="flex:1"><strong>Upload Apple's certificate here</strong>
          <div class="row" style="margin-top:8px"><button class="btn btn-sm btn-primary" id="upload">Upload certificate</button></div></div></li>
      </ol></div></section>` : ""}
  </div>`.s;
  $("#test", host)?.addEventListener("click", async () => {
    const devs = await api.get("/api/devices?status=enrolled&limit=200&sort=name");
    if (!devs.items.length) return toast("Enroll a device first", "err");
    modal({ title: "Send a test push", body: html`<label class="field"><span>Device</span><select name="u">${devs.items.map((d) => html`<option value="${d.udid}">${d.device_name || d.serial_number}</option>`)}</select></label>`,
      actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Send push", kind: "primary", submit: true, onClick: async (m) => {
        const r = await api.post("/api/apns/test", { udid: $("form", m.el).u.value });
        toast(`Apple accepted the push (HTTP ${r.status})`);
      } }] });
  });
  $("#csr", host)?.addEventListener("click", async () => {
    try { await api.post("/api/apns/csr", { email: $("#csr-email", host).value }); toast("Certificate request created"); ctx.refresh(); } catch (e) { toastError(e); }
  });
  $("#csr-dl", host)?.addEventListener("click", () => api.download("GET", "/api/apns/csr", undefined, "push.csr").catch(toastError));
  $("#vendor", host)?.addEventListener("click", () => modal({ title: "Sign with your MDM vendor certificate",
    body: html`<label class="field"><span>Vendor certificate (.p12)</span><input type="file" name="f" accept=".p12,.pfx" required></label><label class="field"><span>Password</span><input type="password" name="pw"></label>
      <p class="hint">Created in your Apple Developer account under Certificates → MDM CSR. The signed request downloads as a file to upload to Apple.</p>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Sign request", kind: "primary", submit: true, onClick: async (m) => {
      const f = $("form", m.el);
      if (!f.f.files[0]) { toast("Choose the .p12 file", "err"); return false; }
      await api.download("POST", "/api/apns/vendor-sign", { p12: await fileToBase64(f.f.files[0]), password: f.pw.value }, "PushCertificateRequest.plist.b64");
      toast("Signed request downloaded. Upload it to Apple next.");
    } }] }));
  $("#mdmcert", host)?.addEventListener("click", () => modal({ title: "Use mdmcert.download",
    body: html`<p>Register once at <a href="https://mdmcert.download" target="_blank" rel="noopener">mdmcert.download</a>, then send your request. You'll receive an encrypted file by email; upload it with “Decrypt mdmcert.download reply”.</p>
      <label class="field"><span>Email registered at mdmcert.download</span><input type="email" name="e" required></label>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Send request", kind: "primary", submit: true, onClick: async (m) => {
      const r = await api.post("/api/apns/mdmcert", { email: $("form", m.el).e.value });
      toast(r.message);
    } }] }));
  $("#decrypt", host)?.addEventListener("click", () => modal({ title: "Decrypt the mdmcert.download reply",
    body: html`<label class="field"><span>Encrypted file from the email</span><input type="file" name="f" required></label>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Decrypt", kind: "primary", submit: true, onClick: async (m) => {
      const file = $("form", m.el).f.files[0];
      if (!file) { toast("Choose the file", "err"); return false; }
      await api.download("POST", "/api/apns/mdmcert/decrypt", { data: await fileToBase64(file) }, "PushCertificateRequest.plist.b64");
      toast("Decrypted request downloaded. Upload it to Apple next.");
    } }] }));
  $("#upload", host)?.addEventListener("click", () => uploadCert(ctx));
}

function uploadCert(ctx, allowTopicChange = false) {
  modal({ title: "Upload push certificate",
    body: html`<label class="field"><span>Certificate from Apple (.pem), or a .p12 with its key</span><input type="file" name="f" accept=".pem,.cer,.crt,.p12,.pfx" required></label>
      <label class="field"><span>.p12 password</span><input type="password" name="pw" placeholder="only for .p12 files"></label>
      <label class="field"><span>Apple Account used</span><input type="text" name="apple" placeholder="it@example.com"><small class="hint">Recorded so you renew with the same account. Renewing with a different account breaks every enrolled device.</small></label>
      ${allowTopicChange ? html`<div class="callout bad"><p><strong>This certificate has a different topic.</strong> Every enrolled device will stop receiving commands and must be re-enrolled.</p></div>` : ""}`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: allowTopicChange ? "Replace anyway" : "Upload", kind: allowTopicChange ? "danger" : "primary", submit: true, onClick: async (m) => {
      const f = $("form", m.el);
      if (!f.f.files[0]) { toast("Choose the certificate file", "err"); return false; }
      try {
        const r = await api.post("/api/apns/certificate", { data: await fileToBase64(f.f.files[0]), password: f.pw.value, apple_id: f.apple.value, allow_topic_change: allowTopicChange });
        toast(`Push certificate installed. It expires ${fmtDate(r.not_after)}.`);
        ctx.refresh();
      } catch (e) {
        if (e.body && e.body.topic_changed) { m.close(); uploadCert(ctx, true); return; }
        throw e;
      }
    } }] });
}

async function vpp(host, ctx) {
  const res = await api.get("/api/vpp/tokens");
  host.innerHTML = html`<section class="panel" style="max-width:960px"><div class="panel-head"><h2>Content tokens</h2>${can("admin") ? html`<button class="btn btn-sm btn-primary" id="add">Add token</button>` : ""}</div>
    ${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Location</th><th class="num">Apps</th><th>Expires</th><th>Last sync</th><th></th></tr></thead><tbody>
      ${res.items.map((t) => html`<tr><td><strong>${t.name}</strong><span class="cell-sub">${t.location_name || t.org_name}</span>${t.last_error ? html`<span class="err-text" title="${t.last_error}">${t.last_error}</span>` : ""}</td>
        <td class="num">${t.asset_count}</td><td>${fmtDate(t.exp_date)}</td><td>${ago(t.last_sync)}</td>
        <td class="right nowrap">${can("manage") ? html`<button class="btn btn-sm" data-sync="${t.id}">Sync</button>` : ""} ${can("admin") ? html`<button class="btn btn-sm btn-ghost link-danger" data-del="${t.id}">Remove</button>` : ""}</td></tr>`)}
    </tbody></table></div>` : emptyState("No Apps and Books token", "Download a content token in Apple Business Manager (Preferences → Payments and Billing) to install purchased apps without Apple Accounts.")}</section>`.s;
  $("#add", host)?.addEventListener("click", () => modal({ title: "Add content token",
    body: html`<label class="field"><span>Content token (.vpptoken)</span><input type="file" name="f" required></label><label class="field"><span>Name</span><input type="text" name="n" placeholder="optional"></label>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Add", kind: "primary", submit: true, onClick: async (m) => {
      const f = $("form", m.el);
      if (!f.f.files[0]) { toast("Choose the token file", "err"); return false; }
      await api.post("/api/vpp/tokens", { data: await fileToBase64(f.f.files[0]), name: f.n.value });
      toast("Token added. Licenses are syncing."); ctx.refresh();
    } }] }));
  $$("[data-sync]", host).forEach((b) => b.addEventListener("click", async () => {
    try { const r = await api.post(`/api/vpp/tokens/${b.dataset.sync}/sync`); toast(`${r.assets} licensed items synced`); ctx.refresh(); } catch (e) { toastError(e); }
  }));
  $$("[data-del]", host).forEach((b) => b.addEventListener("click", async () => {
    if (!(await confirmDialog("Remove this token?", "Apps already installed stay installed.", { danger: true, confirmLabel: "Remove" }))) return;
    try { await api.del(`/api/vpp/tokens/${b.dataset.del}`); toast("Token removed"); ctx.refresh(); } catch (e) { toastError(e); }
  }));
}

const ROLE_HELP = { admin: "Everything, including users, certificates and settings", operator: "Configure profiles, apps, groups, policies and enrollment", helpdesk: "Run device actions and commands", readonly: "View only" };

async function users(host, ctx) {
  const res = await api.get("/api/users");
  host.innerHTML = html`<section class="panel" style="max-width:960px"><div class="panel-head"><h2>Administrators</h2><button class="btn btn-sm btn-primary" id="add">Add administrator</button></div>
    <div class="table-wrap"><table class="table"><thead><tr><th>User</th><th>Role</th><th>Last sign-in</th><th></th></tr></thead><tbody>
    ${res.items.map((u) => html`<tr><td><strong>${u.display_name || u.username}</strong><span class="cell-sub">${u.username}${u.email ? ", " + u.email : ""}</span></td>
      <td>${u.role}${u.disabled ? html` <span class="chip chip-unknown">Disabled</span>` : ""}</td><td>${u.last_login ? ago(u.last_login) : "Never"}</td>
      <td class="right"><button class="btn btn-sm btn-ghost" data-edit="${u.id}">Edit</button></td></tr>`)}</tbody></table></div></section>
    <section class="panel panel-pad" style="max-width:960px;margin-top:16px"><h3>Change your password</h3><form id="pw" class="pw-form">
      <label class="field"><span>Current password</span><input type="password" name="current" autocomplete="current-password" required></label>
      <label class="field"><span>New password</span><input type="password" name="new" minlength="10" autocomplete="new-password" required></label>
      <button class="btn" type="submit">Change password</button></form></section>`.s;
  const edit = (u) => {
    const isNew = !u;
    u = u || { username: "", display_name: "", email: "", role: "operator", disabled: false };
    modal({ title: isNew ? "Add administrator" : `Edit ${u.username}`,
      body: html`<div class="inline-fields">${isNew ? html`<label class="field"><span>Username</span><input type="text" name="username" required></label>` : ""}
        <label class="field"><span>Display name</span><input type="text" name="display_name" value="${u.display_name}"></label>
        <label class="field"><span>Email</span><input type="email" name="email" value="${u.email}"></label>
        <label class="field"><span>${isNew ? "Password" : "New password"}</span><input type="password" name="password" minlength="10" ${isNew ? "required" : ""} placeholder="${isNew ? "" : "leave blank to keep"}" autocomplete="new-password"></label></div>
        <div class="field"><span class="field-label">Role</span>${res.roles.map((r) => html`<label class="check"><input type="radio" name="role" value="${r}" ${u.role === r ? "checked" : ""}><span>${r}<small>${ROLE_HELP[r]}</small></span></label>`)}</div>
        ${isNew ? "" : html`<label class="check"><input type="checkbox" name="disabled" ${u.disabled ? "checked" : ""}><span>Disabled</span></label>`}`,
      actions: [
        ...(isNew ? [] : [{ id: "del", label: "Delete", kind: "danger", onClick: async () => {
          if (!(await confirmDialog(`Delete ${u.username}?`, "They can no longer sign in.", { danger: true, confirmLabel: "Delete" }))) return false;
          await api.del(`/api/users/${u.id}`); toast("Administrator deleted"); ctx.refresh();
        } }]),
        { id: "c", label: "Cancel" },
        { id: "s", label: isNew ? "Add" : "Save", kind: "primary", submit: true, onClick: async (m) => {
          const f = $("form", m.el);
          const role = f.querySelector('input[name="role"]:checked')?.value;
          if (isNew) await api.post("/api/users", { username: f.username.value, display_name: f.display_name.value, email: f.email.value, password: f.password.value, role });
          else await api.put(`/api/users/${u.id}`, { display_name: f.display_name.value, email: f.email.value, role, disabled: f.disabled.checked, ...(f.password.value ? { password: f.password.value } : {}) });
          toast(isNew ? "Administrator added" : "Saved"); ctx.refresh();
        } },
      ] });
  };
  $("#add", host).addEventListener("click", () => edit(null));
  $$("[data-edit]", host).forEach((b) => b.addEventListener("click", () => edit(res.items.find((u) => String(u.id) === b.dataset.edit))));
  $("#pw", host).addEventListener("submit", async (e) => {
    e.preventDefault();
    try { await api.post("/api/auth/password", { current: e.target.current.value, new: e.target.new.value }); toast("Password changed"); e.target.reset(); } catch (err) { toastError(err); }
  });
}

async function apikeys(host, ctx) {
  const res = await api.get("/api/apikeys");
  host.innerHTML = html`<section class="panel" style="max-width:960px"><div class="panel-head"><h2>API keys</h2><button class="btn btn-sm btn-primary" id="add">Create key</button></div>
    <p class="hint panel-pad" style="margin:0 0 -6px">Use keys for scripts and integrations: <span class="kbd">Authorization: Bearer orch_…</span> on any <span class="kbd">/api</span> endpoint.</p>
    ${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Name</th><th>Key</th><th>Role</th><th>Last used</th><th>Expires</th><th></th></tr></thead><tbody>
      ${res.items.map((k) => html`<tr><td><strong>${k.name}</strong><span class="cell-sub">by ${k.created_by}</span></td><td class="ident">${k.prefix}…</td><td>${k.role}</td><td>${k.last_used ? ago(k.last_used) : "Never"}</td>
        <td>${k.expires_at ? fmtDate(k.expires_at) : "Never"}</td><td class="right"><button class="btn btn-sm btn-ghost link-danger" data-del="${k.id}">Revoke</button></td></tr>`)}</tbody></table></div>`
      : html`<p class="hint panel-pad">No keys yet.</p>`}</section>`.s;
  $("#add", host).addEventListener("click", () => modal({ title: "Create API key",
    body: html`<label class="field"><span>Name</span><input type="text" name="n" required placeholder="e.g. Inventory export"></label>
      <div class="inline-fields"><label class="field"><span>Role</span><select name="r">${["readonly", "helpdesk", "operator", "admin"].map((r) => html`<option value="${r}">${r}</option>`)}</select></label>
      <label class="field"><span>Expires after (days)</span><input type="number" min="0" name="e" placeholder="never"></label></div>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Create key", kind: "primary", submit: true, onClick: async (m) => {
      const f = $("form", m.el);
      const r = await api.post("/api/apikeys", { name: f.n.value, role: f.r.value, expires_in_days: Number(f.e.value || 0) });
      m.close();
      modal({ title: "Copy your API key", body: html`<p>This is the only time the key is shown.</p><p class="secret">${r.secret}</p>`,
        actions: [{ id: "copy", label: "Copy", onClick: () => { copyText(r.secret); return false; } }, { id: "d", label: "Done", kind: "primary", onClick: () => ctx.refresh() }] });
      return false;
    } }] }));
  $$("[data-del]", host).forEach((b) => b.addEventListener("click", async () => {
    if (!(await confirmDialog("Revoke this key?", "Scripts using it stop working immediately.", { danger: true, confirmLabel: "Revoke" }))) return;
    try { await api.del(`/api/apikeys/${b.dataset.del}`); toast("Key revoked"); ctx.refresh(); } catch (e) { toastError(e); }
  }));
}

const EVENT_LABEL = {
  "device.enrolled": "Device enrolled", "device.unenrolled": "Device unenrolled", "device.token_update": "Push token updated", "device.inventory": "Inventory updated",
  "device.location": "Location recorded", "device.telemetry": "Battery/network sample", "command.completed": "Command completed", "command.failed": "Command failed",
  "compliance.changed": "Compliance changed", "ade.device_added": "ADE device added", "ade.device_removed": "ADE device removed", "ddm.status": "Declarative status report",
  "apns.cert_expiring": "Push certificate expiring",
};

async function webhooks(host, ctx) {
  const res = await api.get("/api/webhooks");
  host.innerHTML = html`<section class="panel" style="max-width:960px"><div class="panel-head"><h2>Webhooks</h2>${can("admin") ? html`<button class="btn btn-sm btn-primary" id="add">Add webhook</button>` : ""}</div>
    <p class="hint panel-pad" style="margin:0 0 -6px">Orchard POSTs JSON to these URLs. Verify the <span class="kbd">X-Orchard-Signature</span> header (HMAC-SHA256 of the body with the webhook secret).</p>
    ${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Webhook</th><th>Events</th><th>Last delivery</th><th></th></tr></thead><tbody>
      ${res.items.map((w) => html`<tr><td><strong>${w.name}</strong>${w.enabled ? "" : html` <span class="chip chip-unknown">Off</span>`}<span class="cell-sub ident" style="margin-left:0">${w.url}</span></td>
        <td class="nowrap">${w.events.length ? plural(w.events.length, "event") : "All events"}</td>
        <td>${w.last_delivery ? html`<span class="row" style="gap:8px">${w.last_error ? html`<span class="chip chip-bad">Failed</span>` : html`<span class="chip chip-good">${w.last_status}</span>`}${ago(w.last_delivery)}</span>${w.last_error ? html`<span class="err-text" title="${w.last_error}">${w.last_error}</span>` : ""}` : "Never"}</td>
        <td class="right nowrap">${can("admin") ? html`<button class="btn btn-sm" data-test="${w.id}">Test</button> <button class="btn btn-sm btn-ghost" data-edit="${w.id}">Edit</button>` : ""}</td></tr>`)}</tbody></table></div>`
      : html`<p class="hint panel-pad">No webhooks yet.</p>`}</section>`.s;
  const edit = (w) => {
    const isNew = !w;
    w = w || { name: "", url: "", events: [], enabled: true, secret: "" };
    modal({ title: isNew ? "Add webhook" : "Edit webhook", wide: true,
      body: html`<div class="inline-fields"><label class="field"><span>Name</span><input type="text" name="name" value="${w.name}"></label><label class="field"><span>URL</span><input type="url" name="url" value="${w.url}" required placeholder="https://"></label></div>
        <label class="field"><span>Secret</span><input type="text" name="secret" class="code" value="${w.secret}" placeholder="${isNew ? "generated if left blank" : ""}"></label>
        <div class="field"><span class="field-label">Events (none selected sends all)</span><div class="inline-fields">${res.events.map((e) => html`<label class="check"><input type="checkbox" name="ev" value="${e}" ${w.events.includes(e) ? "checked" : ""}><span>${EVENT_LABEL[e] || e}</span></label>`)}</div></div>
        <label class="check"><input type="checkbox" name="enabled" ${w.enabled ? "checked" : ""}><span>Enabled</span></label>`,
      actions: [
        ...(isNew ? [] : [{ id: "del", label: "Delete", kind: "danger", onClick: async () => { await api.del(`/api/webhooks/${w.id}`); toast("Webhook deleted"); ctx.refresh(); } }]),
        { id: "c", label: "Cancel" },
        { id: "s", label: "Save", kind: "primary", submit: true, onClick: async (m) => {
          const f = $("form", m.el);
          const body = { name: f.name.value, url: f.url.value, secret: f.secret.value, enabled: f.enabled.checked, events: $$('input[name="ev"]:checked', m.el).map((i) => i.value) };
          if (isNew) await api.post("/api/webhooks", body); else await api.put(`/api/webhooks/${w.id}`, body);
          toast("Webhook saved"); ctx.refresh();
        } },
      ] });
  };
  $("#add", host)?.addEventListener("click", () => edit(null));
  $$("[data-edit]", host).forEach((b) => b.addEventListener("click", () => edit(res.items.find((w) => String(w.id) === b.dataset.edit))));
  $$("[data-test]", host).forEach((b) => b.addEventListener("click", async () => {
    try { const r = await api.post(`/api/webhooks/${b.dataset.test}/test`); toast(r.ok ? `Delivered (HTTP ${r.status})` : `Failed: ${r.error}`, r.ok ? "ok" : "err"); ctx.refresh(); } catch (e) { toastError(e); }
  }));
}

const uptime = (s) => s >= 86400 ? plural(Math.floor(s / 86400), "day") : s >= 3600 ? plural(Math.floor(s / 3600), "hour") : plural(Math.max(1, Math.floor(s / 60)), "minute");
const VIA = { token: "Enrollment link", account: "Work account sign-in", ade: "Automated enrollment", manual: "Profile download", renew: "Renewal" };
const viaLabel = (ref) => {
  const [kind] = String(ref || "").split(":");
  return VIA[kind] || ref || "—";
};

async function about(host) {
  const [sys, certs] = await Promise.all([api.get("/api/system"), api.get("/api/pki/issued?limit=50")]);
  const up = sys.uptime_seconds;
  host.innerHTML = html`<div class="grid-2" style="max-width:1100px">
    <section class="panel"><div class="panel-head"><h2>Server</h2></div><div class="panel-pad"><dl class="kv">
      <dt>Version</dt><dd>${sys.version}</dd><dt>Go</dt><dd>${sys.go_version}</dd><dt>Running for</dt><dd>${uptime(up)}</dd>
      <dt>Public URL</dt><dd class="ident">${sys.public_url || "—"}</dd><dt>TLS</dt><dd>${{ acme: "Let's Encrypt (automatic)", files: "Certificate files", proxy: "Terminated by a reverse proxy" }[sys.tls_mode]}</dd>
      <dt>Database</dt><dd>${bytes(sys.db_size)} <span class="hint ident">${sys.data_dir}</span></dd>
      <dt>Remote wipe</dt><dd>Not supported by design. The enrollment profile withholds the erase right and erase commands are refused.</dd></dl></div></section>
    <section class="panel"><div class="panel-head"><h2>Device identity CA</h2><button class="btn btn-sm" id="ca">Download CA</button></div><div class="panel-pad"><dl class="kv">
      <dt>Subject</dt><dd>${certs.ca.subject}</dd><dt>Expires</dt><dd>${fmtDate(certs.ca.not_after)}</dd><dt>SHA-256</dt><dd class="ident" style="font-size:12px">${certs.ca.fingerprint}</dd></dl></div></section>
  </div>
  <h2 class="section">Recently issued device identities</h2>
  <section class="panel" style="max-width:1100px">${certs.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Subject</th><th>Issued</th><th>Expires</th><th>Device</th><th>Via</th></tr></thead><tbody>
    ${certs.items.map((c) => html`<tr><td>${c.subject}</td><td>${fmtTime(c.created_at)}</td><td>${fmtDate(c.not_after)}</td><td>${c.device_id ? html`<a href="#/devices/${encodeURIComponent(c.device_id)}">${c.device_name || "View device"}</a>` : html`<span class="muted">Not enrolled</span>`}</td><td>${viaLabel(c.ref)}</td></tr>`)}</tbody></table></div>`
    : html`<p class="hint panel-pad" style="margin:0">No identities issued yet.</p>`}</section>`.s;
  $("#ca", host).addEventListener("click", () => api.download("GET", "/api/pki/ca", undefined, "orchard-ca.pem").catch(toastError));
}

export { session, plural };
