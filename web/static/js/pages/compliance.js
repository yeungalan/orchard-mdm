import { html, $, api, can, ago, deviceName, complianceChip, toast, toastError, confirmDialog, emptyState } from "../lib.js";
import { assignmentsPanel } from "../components.js";
import { statusStack } from "../charts.js";

export async function render(ctx) {
  if (ctx.name === "policy") return editor(ctx);
  const { root, navigate } = ctx;
  const [pol, sum] = await Promise.all([api.get("/api/compliance/policies"), api.get("/api/compliance/summary")]);
  root.innerHTML = html`<div class="page-head"><div><h1>Compliance</h1><p class="sub">Rules every device must meet. Devices that fail get a grace period, then are marked not compliant; you can also lock them automatically. Orchard never wipes devices.</p></div>
    <div class="actions">${can("manage") ? html`<button class="btn" id="eval">Evaluate now</button><a class="btn btn-primary" href="#/compliance/new">New policy</a>` : ""}</div></div>
    <div class="grid-2">
      <section class="panel"><div class="panel-head"><h2>Fleet status</h2></div><div class="panel-pad">${statusStack(sum.counts)}</div></section>
      <section class="panel"><div class="panel-head"><h2>Policies</h2></div>
        ${pol.items.length ? html`<div class="table-wrap"><table class="table"><tbody>${pol.items.map((p) => html`<tr><td><a class="primary" href="#/compliance/${p.id}">${p.name}</a>${p.description ? html`<span class="cell-sub">${p.description}</span>` : ""}</td>
          <td>${p.enabled ? html`<span class="chip chip-good">On</span>` : html`<span class="chip chip-unknown">Off</span>`}</td><td class="num">${p.assignments} groups</td></tr>`)}</tbody></table></div>`
          : html`<p class="hint panel-pad" style="margin:0">No policies yet. Without a policy every device counts as compliant.</p>`}</section>
    </div>
    <h2 class="section">Devices that need attention</h2>
    <section class="panel">${sum.devices.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Device</th><th>Status</th><th>Why</th><th>Last seen</th></tr></thead><tbody>
      ${sum.devices.map((d) => html`<tr><td><a class="primary" href="#/devices/${encodeURIComponent(d.udid)}">${deviceName(d)}</a><span class="cell-sub">${d.assigned_user}</span></td><td>${complianceChip(d.compliance)}</td>
        <td>${(d.compliance_reasons || []).map((r) => html`<div>${r}</div>`)}</td><td>${ago(d.last_seen)}</td></tr>`)}</tbody></table></div>`
      : emptyState("Every device is compliant", "Devices that fail a policy will be listed here.")}</section>`.s;
  $("#eval", root)?.addEventListener("click", async () => {
    try { await api.post("/api/compliance/evaluate"); toast("All devices evaluated"); ctx.refresh(); } catch (e) { toastError(e); }
  });
}

async function editor({ root, params, navigate, refresh }) {
  const isNew = params.id === "new";
  let p = { name: "", description: "", enabled: true, rules: {}, actions: {}, grace_hours: 0 };
  if (!isNew) p = (await api.get(`/api/compliance/policies/${params.id}`)).policy;
  const r = p.rules || {}, a = p.actions || {};
  const editable = can("manage");
  const dis = editable ? "" : "disabled";
  root.innerHTML = html`<div class="crumb"><a href="#/compliance">Compliance</a></div>
    <div class="page-head"><div><h1>${isNew ? "New compliance policy" : p.name}</h1></div>
      ${!isNew && editable ? html`<div class="actions"><button class="btn btn-danger" id="del">Delete</button></div>` : ""}</div>
    <div class="grid-2"><form class="panel panel-pad" id="f">
      <label class="field"><span>Name</span><input type="text" name="name" value="${p.name}" required ${dis}></label>
      <label class="field"><span>Description</span><input type="text" name="description" value="${p.description}" ${dis}></label>
      <label class="check"><input type="checkbox" name="enabled" ${p.enabled ? "checked" : ""} ${dis}><span>Policy is on</span></label>
      <fieldset style="margin-top:14px"><legend>Operating system</legend><div class="inline-fields">
        <label class="field"><span>Minimum iOS version</span><input type="text" name="min_os_version" value="${r.min_os_version || ""}" placeholder="e.g. 18.0" ${dis}></label>
        <label class="field"><span>Maximum iOS version</span><input type="text" name="max_os_version" value="${r.max_os_version || ""}" placeholder="optional" ${dis}></label></div></fieldset>
      <fieldset><legend>Security</legend>
        <label class="check"><input type="checkbox" name="require_passcode" ${r.require_passcode ? "checked" : ""} ${dis}><span>Require a passcode</span></label>
        <label class="check"><input type="checkbox" name="require_passcode_compliant" ${r.require_passcode_compliant ? "checked" : ""} ${dis}><span>Passcode must meet assigned passcode profiles</span></label>
        <label class="check"><input type="checkbox" name="require_encryption" ${r.require_encryption ? "checked" : ""} ${dis}><span>Require data protection (encryption)</span></label>
        <label class="check"><input type="checkbox" name="require_supervised" ${r.require_supervised ? "checked" : ""} ${dis}><span>Require supervision</span></label>
        <label class="check"><input type="checkbox" name="block_activation_lock" ${r.block_activation_lock ? "checked" : ""} ${dis}><span>Fail when Activation Lock is on</span></label>
        <label class="check"><input type="checkbox" name="require_profiles_installed" ${r.require_profiles_installed ? "checked" : ""} ${dis}><span>All assigned profiles must be installed</span></label></fieldset>
      <fieldset><legend>Activity and resources</legend><div class="inline-fields">
        <label class="field"><span>Must check in within (days)</span><input type="number" min="0" name="max_inactive_days" value="${r.max_inactive_days || ""}" ${dis}></label>
        <label class="field"><span>Minimum free storage (GB)</span><input type="number" min="0" step="0.5" name="min_free_storage_gb" value="${r.min_free_storage_gb || ""}" ${dis}></label></div>
        <label class="check"><input type="checkbox" name="block_roaming" ${r.block_roaming ? "checked" : ""} ${dis}><span>Fail while roaming</span></label></fieldset>
      <fieldset><legend>Apps</legend>
        <label class="field"><span>Required apps (bundle IDs)</span><textarea name="required_apps" rows="2" ${dis}>${(r.required_apps || []).join("\n")}</textarea></label>
        <label class="field"><span>Blocked apps (bundle IDs, * wildcards)</span><textarea name="blocked_apps" rows="2" placeholder="com.example.game*" ${dis}>${(r.blocked_apps || []).join("\n")}</textarea></label></fieldset>
      <fieldset><legend>When a device fails</legend><div class="inline-fields">
        <label class="field"><span>Grace period (hours)</span><input type="number" min="0" name="grace_hours" value="${p.grace_hours || 0}" ${dis}><small class="hint">The device shows as “in grace period” first.</small></label>
        <label class="field"><span>Then lock the device after (hours)</span><input type="number" min="0" name="lock_after_hours" value="${a.lock_after_hours || ""}" placeholder="never" ${dis}><small class="hint">Sent once per failure. Leave empty to only report.</small></label></div>
        <label class="field"><span>Lock screen message</span><input type="text" name="lock_message" value="${a.lock_message || ""}" placeholder="This device doesn't meet company requirements. Contact IT." ${dis}></label></fieldset>
      ${editable ? html`<button class="btn btn-primary" type="submit">${isNew ? "Create policy" : "Save policy"}</button>` : ""}
    </form>
    ${isNew ? html`<div class="callout"><p>Save the policy, then assign it to groups. Devices in those groups are evaluated whenever their inventory changes.</p></div>` : html`<section id="assign"></section>`}</div>`.s;
  if (!isNew) await assignmentsPanel($("#assign", root), "compliance", params.id);
  $("#f", root).addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    const lines = (s) => s.split("\n").map((x) => x.trim()).filter(Boolean);
    const num = (s) => (s === "" ? 0 : Number(s));
    const body = {
      name: f.name.value, description: f.description.value, enabled: f.enabled.checked, grace_hours: num(f.grace_hours.value),
      rules: {
        min_os_version: f.min_os_version.value.trim(), max_os_version: f.max_os_version.value.trim(), require_passcode: f.require_passcode.checked,
        require_passcode_compliant: f.require_passcode_compliant.checked, require_encryption: f.require_encryption.checked, require_supervised: f.require_supervised.checked,
        block_activation_lock: f.block_activation_lock.checked, require_profiles_installed: f.require_profiles_installed.checked, max_inactive_days: num(f.max_inactive_days.value),
        min_free_storage_gb: num(f.min_free_storage_gb.value), block_roaming: f.block_roaming.checked, required_apps: lines(f.required_apps.value), blocked_apps: lines(f.blocked_apps.value),
      },
      actions: { lock_after_hours: num(f.lock_after_hours.value), lock_message: f.lock_message.value.trim() },
    };
    try {
      const saved = isNew ? await api.post("/api/compliance/policies", body) : await api.put(`/api/compliance/policies/${params.id}`, body);
      toast(isNew ? "Policy created. Now assign it to groups." : "Policy saved");
      if (isNew) navigate("/compliance/" + saved.id); else refresh();
    } catch (err) { toastError(err); }
  });
  $("#del", root)?.addEventListener("click", async () => {
    if (!(await confirmDialog(`Delete “${p.name}”?`, "Devices are re-evaluated without it.", { danger: true, confirmLabel: "Delete policy" }))) return;
    try { await api.del(`/api/compliance/policies/${params.id}`); toast("Policy deleted"); navigate("/compliance"); } catch (err) { toastError(err); }
  });
}
