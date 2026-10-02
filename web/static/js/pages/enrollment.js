import { html, $, $$, api, can, ago, fmtDate, ownershipLabel, modelLabel, modal, toast, toastError, confirmDialog, copyText, emptyState } from "../lib.js";
import { groups } from "../components.js";

export async function render({ root, refresh }) {
  const [info, tokens, gs, acct] = await Promise.all([api.get("/api/enrollment/info"), api.get("/api/enrollment/tokens"), groups(true), api.get("/api/enrollment/account")]);
  const gname = Object.fromEntries(gs.map((g) => [g.id, g.name]));
  const ready = info.readiness.filter((r) => r.id !== "signing").every((r) => r.ok);
  root.innerHTML = html`<div class="page-head"><div><h1>Enrollment</h1><p class="sub">Share an enrollment link with people setting up an iPhone or iPad. Each link can place devices in groups and set their owner.</p></div>
    ${can("manage") ? html`<div class="actions"><button class="btn" id="dl" ${ready ? "" : "disabled"}>Download profile</button><button class="btn btn-primary" id="new">New enrollment link</button></div>` : ""}</div>
    <section class="panel"><div class="panel-head"><h2>Server readiness</h2>${ready ? html`<span class="chip chip-good">Ready to enroll</span>` : html`<span class="chip chip-warn">Setup needed</span>`}</div>
      <ul class="checklist panel-pad" style="padding-top:2px;padding-bottom:2px">${info.readiness.map((r) => html`<li class="${r.ok ? "ok" : "no"}"><span class="mark" aria-hidden="true">${r.ok ? "✓" : "!"}</span>
        <div><strong>${r.label}</strong><div class="hint">${r.ok ? r.detail || "Done" : r.help}${r.warning ? html` <strong>${r.warning}</strong>` : ""}</div></div></li>`)}</ul></section>
    <h2 class="section">Enrollment links</h2>
    <section class="panel">${tokens.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Link</th><th>Puts devices in</th><th>Owner</th><th class="num">Used</th><th>Expires</th><th></th></tr></thead><tbody>
      ${tokens.items.map((t) => html`<tr><td><strong>${t.name}</strong>${t.usable ? "" : html` <span class="chip chip-unknown">Inactive</span>`}<span class="cell-sub ident" style="margin-left:0">${t.url}</span></td>
        <td>${t.group_ids.length ? t.group_ids.map((id) => html`<span class="tag">${gname[id] || "#" + id}</span>`) : html`<span class="muted">All Devices only</span>`}</td>
        <td>${ownershipLabel(t.ownership)}${t.assigned_user ? html`<span class="cell-sub">${t.assigned_user}</span>` : ""}</td>
        <td class="num">${t.uses}${t.max_uses ? ` / ${t.max_uses}` : ""}</td><td>${t.expires_at ? fmtDate(t.expires_at) : "Never"}</td>
        <td class="right nowrap"><button class="btn btn-sm" data-share="${t.id}">Share</button> ${can("manage") ? html`<button class="btn btn-sm btn-ghost" data-edit="${t.id}">Edit</button>` : ""}</td></tr>`)}
    </tbody></table></div>` : emptyState("No enrollment links yet", "Create a link, then open it on the iPhone or iPad you want to enroll.")}</section>
    <h2 class="section">Work account sign-in</h2>
    ${accountSection(acct, gname)}
    <h2 class="section">Addresses devices use</h2>
    <section class="panel panel-pad"><dl class="kv">
      <dt>Enrollment page</dt><dd class="ident">${info.enroll_url}</dd>
      <dt>Automated Device Enrollment</dt><dd class="ident">${info.ade_url}</dd>
      <dt>MDM check-in</dt><dd class="ident">${info.checkin_url}</dd>
      <dt>MDM commands</dt><dd class="ident">${info.server_url}</dd>
      <dt>Certificate enrollment (SCEP)</dt><dd class="ident">${info.scep_url}</dd>
      <dt>Open enrollment</dt><dd>${info.require_token ? "Off: an enrollment link or code is required" : "On: anyone with the enrollment page can enroll"} <a href="#/settings/general">Change</a></dd>
    </dl><p class="hint">The enrollment profile grants every MDM right except device erase. Orchard MDM has no remote wipe.</p></section>`.s;

  const byId = Object.fromEntries(tokens.items.map((t) => [String(t.id), t]));
  $$("[data-share]", root).forEach((b) => b.addEventListener("click", () => share(byId[b.dataset.share])));
  $$("[data-edit]", root).forEach((b) => b.addEventListener("click", () => editToken(byId[b.dataset.edit], gs, refresh)));
  $("#new", root)?.addEventListener("click", () => editToken(null, gs, refresh));
  $("#dl", root)?.addEventListener("click", () => api.download("GET", "/api/enrollment/profile", undefined, "enroll.mobileconfig").catch(toastError));
  $("#acct-edit", root)?.addEventListener("click", () => editAccount(acct, gs, refresh));
  $$("[data-discovery]", root).forEach((b) => b.addEventListener("click", async () => {
    try { await api.post(`/api/ade/servers/${b.dataset.discovery}/service-discovery`); toast("Registered with Apple Business Manager"); refresh(); } catch (e) { toastError(e); }
  }));
}

const AUTH_LABEL = { code: "Enrollment code", sso: "Single sign-on", both: "Single sign-on or enrollment code" };

function accountSection(acct, gname) {
  const c = acct.config;
  const byod = c.mode !== "adde";
  return html`<section class="panel"><div class="panel-head"><h2>Sign In to Work or School Account</h2>
      <span class="row">${c.enabled ? html`<span class="chip chip-good">On</span>` : html`<span class="chip chip-unknown">Off</span>`}${can("admin") ? html`<button class="btn btn-sm" id="acct-edit">Configure</button>` : ""}</span></div>
    <div class="panel-pad">
      <p style="margin-top:0">People enroll from <strong>Settings → General → VPN &amp; Device Management → Sign In to Work or School Account</strong> with their work email.
        ${byod ? "With User Enrollment, work apps and data live in a separate encrypted volume. You can't see personal apps, data, identifiers or location, and you can't erase the device." : "Account-driven device enrollment fully manages an organization-owned device."}
        It needs Managed Apple Accounts (federated or created in Apple Business Manager).</p>
      <div class="grid-2"><dl class="kv">
        <dt>Enrolls as</dt><dd>${byod ? "User Enrollment (personal devices)" : "Device enrollment (organization-owned)"}</dd>
        <dt>Work domains</dt><dd>${c.domains.length ? c.domains.join(", ") : html`<span class="muted">Any</span>`}</dd>
        <dt>Sign-in</dt><dd>${AUTH_LABEL[c.auth] || c.auth}${c.oidc_issuer && c.auth !== "code" ? html`<span class="cell-sub ident" style="margin-left:0">${c.oidc_issuer}</span>` : ""}</dd>
        <dt>Adds devices to</dt><dd>${c.group_ids.length ? c.group_ids.map((id) => html`<span class="tag">${gname[id] || "#" + id}</span>`) : html`<span class="muted">All Devices only</span>`}</dd>
      </dl>
      <div><div class="field-label">How devices find this server</div>
        <p class="hint" style="margin-top:4px">Either serve this file at <span class="ident">https://&lt;your work domain&gt;/.well-known/com.apple.remotemanagement</span> (a redirect that keeps the query string works):</p>
        <p class="secret">${acct.well_known_url}</p>
        <p class="hint">or, for iOS 18.2 and later, register it with Apple Business Manager:</p>
        ${acct.ade_servers.length ? html`<ul class="checklist">${acct.ade_servers.map((s) => html`<li class="${s.registered ? "ok" : "no"}"><span class="mark" aria-hidden="true">${s.registered ? "✓" : "–"}</span><div style="flex:1"><strong>${s.name}</strong>
          <div class="hint">${s.registered ? "Registered" : s.has_token ? "Not registered" : "Upload the server token first"}</div></div>${can("admin") && s.has_token ? html`<button class="btn btn-sm" data-discovery="${s.id}">${s.registered ? "Register again" : "Register"}</button>` : ""}</li>`)}</ul>`
          : html`<p class="hint">Connect Apple Business Manager under <a href="#/ade">Automated enrollment</a> to use this option.</p>`}
      </div></div>
    </div>
    ${acct.recent.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Signed in</th><th>Work account</th><th>Method</th><th>Device</th></tr></thead><tbody>
      ${acct.recent.slice(0, 15).map((a) => html`<tr><td class="nowrap">${ago(a.created_at)}</td><td>${a.managed_apple_id}${a.display_name && a.display_name !== a.managed_apple_id.split("@")[0] ? html`<span class="cell-sub">${a.display_name}</span>` : ""}</td>
        <td>${a.auth_method === "sso" ? "Single sign-on" : "Enrollment code"}<span class="cell-sub">${a.mode === "adde" ? "Device enrollment" : "User Enrollment"}</span></td>
        <td>${a.device_id ? html`<a href="#/devices/${encodeURIComponent(a.device_id)}">${a.device_name || modelLabel(a.product) || "View device"}</a>` : a.profile_issued_at ? html`<span class="muted">Profile downloaded, not enrolled yet</span>` : html`<span class="muted">Signed in only</span>`}</td></tr>`)}
    </tbody></table></div>` : ""}
  </section>`;
}

function editAccount(acct, gs, refresh) {
  const c = acct.config;
  const statics = gs.filter((g) => g.kind === "static");
  const ctl = modal({ title: "Work account sign-in", wide: true,
    body: html`<label class="check"><input type="checkbox" name="enabled" ${c.enabled ? "checked" : ""}><span>Allow enrollment by signing in with a work account</span></label>
      <fieldset style="margin-top:12px"><legend>Enroll devices as</legend>
        <label class="check"><input type="radio" name="mode" value="byod" ${c.mode !== "adde" ? "checked" : ""}><span>User Enrollment (BYOD)<small>For personal devices. Work data is separate; the organization can't see personal data or locate or erase the device.</small></span></label>
        <label class="check"><input type="radio" name="mode" value="adde" ${c.mode === "adde" ? "checked" : ""}><span>Device enrollment<small>For organization-owned devices that aren't in Apple Business Manager. Full management (still no remote wipe).</small></span></label></fieldset>
      <label class="field"><span>Work domains</span><input type="text" name="domains" value="${c.domains.join(", ")}" placeholder="acme.com, acme.co.jp"><small class="hint">Only accounts in these domains can enroll. Leave empty to allow any.</small></label>
      <label class="field"><span>How people sign in</span><select name="auth">${["code", "sso", "both"].map((v) => html`<option value="${v}" ${c.auth === v ? "selected" : ""}>${AUTH_LABEL[v]}</option>`)}</select>
        <small class="hint">Enrollment codes are the codes of your enrollment links. Single sign-on works with Microsoft Entra ID, Google, Okta or any OpenID Connect provider.</small></label>
      <fieldset id="sso-box"><legend>Single sign-on (OpenID Connect)</legend>
        <div class="inline-fields"><label class="field"><span>Issuer URL</span><input type="url" name="oidc_issuer" value="${c.oidc_issuer}" placeholder="https://login.microsoftonline.com/<tenant>/v2.0"></label>
          <label class="field"><span>Client ID</span><input type="text" name="oidc_client_id" value="${c.oidc_client_id}"></label>
          <label class="field"><span>Client secret</span><input type="password" name="oidc_client_secret" placeholder="${c.oidc_has_secret ? "Saved; leave empty to keep it" : ""}" autocomplete="new-password"></label>
          <label class="field"><span>Identity claim</span><input type="text" name="oidc_identity_claim" value="${c.oidc_identity_claim}" placeholder="email"><small class="hint">email, upn or preferred_username</small></label></div>
        <label class="check"><input type="checkbox" name="oidc_require_match" ${c.oidc_require_match ? "checked" : ""}><span>The account people sign in with must match the work account on the device</span></label>
        <p class="hint">Register this redirect URI with your identity provider: <span class="ident">${acct.oidc_redirect_url}</span></p></fieldset>
      <div class="field"><span class="field-label">Add enrolled devices to these static groups</span>
        ${statics.length ? statics.map((g) => html`<label class="check"><input type="checkbox" name="g" value="${g.id}" ${c.group_ids.includes(g.id) ? "checked" : ""}><span>${g.name}</span></label>`) : html`<span class="hint">No static groups yet.</span>`}</div>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: "Save", kind: "primary", submit: true, onClick: async (m) => {
      const f = $("form", m.el);
      const body = { enabled: f.enabled.checked, mode: f.querySelector('input[name="mode"]:checked').value, auth: f.auth.value,
        domains: f.domains.value.split(",").map((s) => s.trim()).filter(Boolean), group_ids: $$('input[name="g"]:checked', m.el).map((i) => Number(i.value)),
        oidc_issuer: f.oidc_issuer.value, oidc_client_id: f.oidc_client_id.value, oidc_identity_claim: f.oidc_identity_claim.value, oidc_require_match: f.oidc_require_match.checked };
      if (f.oidc_client_secret.value) body.oidc_client_secret = f.oidc_client_secret.value;
      await api.put("/api/enrollment/account", body);
      toast("Work account sign-in saved");
      refresh();
    } }] });
  const sso = () => ($("#sso-box", ctl.el).style.display = $("form", ctl.el).auth.value === "code" ? "none" : "");
  $("form", ctl.el).auth.addEventListener("change", sso);
  sso();
}

function share(t) {
  modal({ title: `Share “${t.name}”`, body: html`<div class="row" style="align-items:flex-start;gap:20px">
      <img src="/enroll/${encodeURIComponent(t.token)}/qr.png" width="180" height="180" alt="QR code for ${t.name}" style="border-radius:8px;background:#fff;padding:6px;image-rendering:pixelated">
      <div style="flex:1;min-width:220px"><p>Open this link in Safari on the iPhone or iPad, or scan the code with its Camera app.</p>
        <p class="secret">${t.url}</p><p class="hint">Enrollment code: <span class="kbd">${t.token}</span> (for the enrollment page's code box).</p></div></div>`,
    actions: [{ id: "copy", label: "Copy link", onClick: () => { copyText(t.url); return false; } }, { id: "c", label: "Done", kind: "primary" }] });
}

function editToken(t, gs, refresh) {
  const isNew = !t;
  t = t || { name: "", ownership: "corporate", group_ids: [], assigned_user: "", max_uses: 0, expires_at: 0, enabled: true };
  const statics = gs.filter((g) => g.kind === "static");
  const exp = t.expires_at ? new Date(t.expires_at * 1000).toISOString().slice(0, 16) : "";
  modal({ title: isNew ? "New enrollment link" : "Edit enrollment link",
    body: html`<label class="field"><span>Name</span><input type="text" name="name" value="${t.name}" required placeholder="e.g. Sales team iPhones"></label>
      <div class="inline-fields"><label class="field"><span>Ownership</span><select name="ownership">${["corporate", "personal"].map((o) => html`<option value="${o}" ${t.ownership === o ? "selected" : ""}>${ownershipLabel(o)}</option>`)}</select></label>
        <label class="field"><span>Assigned user</span><input type="text" name="assigned_user" value="${t.assigned_user}" placeholder="optional"></label>
        <label class="field"><span>Maximum uses</span><input type="number" min="0" name="max_uses" value="${t.max_uses || ""}" placeholder="unlimited"></label>
        <label class="field"><span>Expires</span><input type="datetime-local" name="expires" value="${exp}"></label></div>
      <div class="field"><span class="field-label">Add enrolled devices to these static groups</span>
        ${statics.length ? statics.map((g) => html`<label class="check"><input type="checkbox" name="g" value="${g.id}" ${t.group_ids.includes(g.id) ? "checked" : ""}><span>${g.name}</span></label>`) : html`<span class="hint">No static groups yet. Create one under Groups.</span>`}</div>
      ${isNew ? "" : html`<label class="check"><input type="checkbox" name="enabled" ${t.enabled ? "checked" : ""}><span>Link is active</span></label>`}`,
    actions: [
      ...(isNew ? [] : [{ id: "del", label: "Delete link", kind: "danger", onClick: async () => {
        if (!(await confirmDialog("Delete this link?", "It stops working immediately. Devices already enrolled are not affected.", { danger: true, confirmLabel: "Delete" }))) return false;
        await api.del(`/api/enrollment/tokens/${t.id}`); toast("Link deleted"); refresh();
      } }]),
      { id: "c", label: "Cancel" },
      { id: "s", label: isNew ? "Create link" : "Save", kind: "primary", submit: true, onClick: async (c) => {
        const f = $("form", c.el);
        const body = { name: f.name.value, ownership: f.ownership.value, assigned_user: f.assigned_user.value, max_uses: Number(f.max_uses.value || 0),
          expires_at: f.expires.value ? Math.floor(new Date(f.expires.value).getTime() / 1000) : 0,
          group_ids: $$('input[name="g"]:checked', c.el).map((i) => Number(i.value)), enabled: f.enabled ? f.enabled.checked : true };
        if (isNew) await api.post("/api/enrollment/tokens", body); else await api.put(`/api/enrollment/tokens/${t.id}`, body);
        toast(isNew ? "Enrollment link created" : "Link saved");
        refresh();
      } },
    ] });
}

export { ago };
