import { html, $, $$, api, can, ago, fmtDate, ownershipLabel, modal, toast, toastError, confirmDialog, copyText, emptyState } from "../lib.js";
import { groups } from "../components.js";

export async function render({ root, refresh }) {
  const [info, tokens, gs] = await Promise.all([api.get("/api/enrollment/info"), api.get("/api/enrollment/tokens"), groups(true)]);
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
