import { html, $, $$, api, can, ago, deviceName, complianceChip, modal, toast, toastError, confirmDialog, plural, emptyState } from "../lib.js";
import { invalidateGroups, pickDevices, intentLabel } from "../components.js";

const KIND = { all: "Built in", static: "Static", dynamic: "Dynamic" };

export async function render(ctx) {
  if (ctx.name === "group") return detail(ctx);
  const { root, navigate } = ctx;
  const res = await api.get("/api/groups");
  root.innerHTML = html`<div class="page-head"><div><h1>Groups</h1><p class="sub">Assign profiles, apps, declarations and compliance policies to groups. Dynamic groups update themselves from device attributes.</p></div>
    ${can("manage") ? html`<div class="actions"><button class="btn btn-primary" id="new">New group</button></div>` : ""}</div>
    <section class="panel"><div class="table-wrap"><table class="table"><thead><tr><th>Group</th><th>Type</th><th class="num">Devices</th><th class="num">Assignments</th><th>Updated</th></tr></thead><tbody>
    ${res.items.map((g) => html`<tr><td><a class="primary" href="#/groups/${g.id}">${g.name}</a>${g.description ? html`<span class="cell-sub">${g.description}</span>` : ""}</td>
      <td>${KIND[g.kind]}${g.kind === "dynamic" ? html`<span class="cell-sub">${plural(g.rules?.rules?.length || 0, "rule")}, match ${g.rules?.match === "any" ? "any" : "all"}</span>` : ""}</td>
      <td class="num">${g.member_count}</td><td class="num">${g.assignments}</td><td>${ago(g.updated_at)}</td></tr>`)}
    </tbody></table></div></section>`.s;
  $("#new", root)?.addEventListener("click", () => editGroup(null, (g) => navigate("/groups/" + g.id)));
}

async function ruleMeta() {
  return api.get("/api/group-rule-fields");
}

function ruleRow(meta, r = { field: "model", op: "eq", value: "" }) {
  return html`<div class="row rule" style="margin-bottom:8px">
    <select name="field" style="width:auto">${meta.fields.map((f) => html`<option value="${f.id}" ${f.id === r.field ? "selected" : ""}>${f.label}</option>`)}</select>
    <select name="op" style="width:auto">${meta.ops.map((o) => html`<option value="${o.id}" ${o.id === r.op ? "selected" : ""}>${o.label}</option>`)}</select>
    <input type="text" name="value" value="${r.value}" style="flex:1;min-width:140px" aria-label="Value">
    <button type="button" class="btn btn-ghost btn-sm" data-rmrule>Remove</button></div>`;
}

async function editGroup(g, onSaved) {
  const meta = await ruleMeta();
  const isNew = !g;
  g = g || { name: "", description: "", kind: "static", rules: { match: "all", rules: [] } };
  const builtin = g.kind === "all";
  const ctl = modal({
    title: isNew ? "New group" : "Edit group", wide: true,
    body: html`${builtin ? "" : html`<label class="field"><span>Name</span><input type="text" name="name" value="${g.name}" required></label>`}
      <label class="field"><span>Description</span><input type="text" name="description" value="${g.description}"></label>
      ${builtin ? "" : html`<fieldset><legend>Membership</legend>
        <label class="check"><input type="radio" name="kind" value="static" ${g.kind === "static" ? "checked" : ""}><span>Static<small>You add and remove devices by hand.</small></span></label>
        <label class="check"><input type="radio" name="kind" value="dynamic" ${g.kind === "dynamic" ? "checked" : ""}><span>Dynamic<small>Devices that match the rules join and leave automatically.</small></span></label></fieldset>
      <div id="rules-box" style="${g.kind === "dynamic" ? "" : "display:none"}">
        <div class="row" style="margin-bottom:10px"><span>Include devices that match</span><select name="match" style="width:auto"><option value="all" ${g.rules?.match !== "any" ? "selected" : ""}>all rules</option><option value="any" ${g.rules?.match === "any" ? "selected" : ""}>any rule</option></select></div>
        <div id="rules">${(g.rules?.rules?.length ? g.rules.rules : [undefined]).map((r) => ruleRow(meta, r))}</div>
        <div class="row"><button type="button" class="btn btn-sm" id="add-rule">Add rule</button><button type="button" class="btn btn-sm" id="preview">Preview matches</button><span class="hint" id="preview-out"></span></div>
      </div>`}`,
    actions: [{ id: "c", label: "Cancel" }, { id: "s", label: isNew ? "Create group" : "Save", kind: "primary", submit: true, onClick: async (c) => {
      const body = readGroup(c.el, g);
      const saved = isNew ? await api.post("/api/groups", body) : await api.put(`/api/groups/${g.id}`, body);
      invalidateGroups();
      toast(isNew ? "Group created" : "Group saved");
      onSaved && onSaved(saved);
    } }],
  });
  const el = ctl.el;
  $$('input[name="kind"]', el).forEach((r) => r.addEventListener("change", () => {
    const kind = el.querySelector('input[name="kind"]:checked')?.value;
    $("#rules-box", el).style.display = kind === "dynamic" ? "" : "none";
  }));
  $("#add-rule", el)?.addEventListener("click", () => $("#rules", el).insertAdjacentHTML("beforeend", ruleRow(meta).s));
  el.addEventListener("click", (e) => { if (e.target.closest("[data-rmrule]")) e.target.closest(".rule").remove(); });
  $("#preview", el)?.addEventListener("click", async () => {
    try {
      const body = readGroup(el, g);
      const res = await api.post("/api/groups/preview", { rules: body.rules });
      $("#preview-out", el).textContent = `${plural(res.total, "device")} match${res.total === 1 ? "es" : ""}${res.total ? ": " + res.items.slice(0, 5).map(deviceName).join(", ") + (res.total > 5 ? "…" : "") : ""}`;
    } catch (e) { toastError(e); }
  });
}

function readGroup(el, g) {
  const f = $("form", el);
  const kind = g.kind === "all" ? "all" : (f.querySelector('input[name="kind"]:checked')?.value || "static");
  const rules = $$(".rule", el).map((r) => ({ field: $('[name="field"]', r).value, op: $('[name="op"]', r).value, value: $('[name="value"]', r).value })).filter((r) => r.value !== "");
  return { name: f.name ? f.name.value : g.name, description: f.description.value, kind, rules: kind === "dynamic" ? { match: f.match.value, rules } : null };
}

async function detail({ root, params, navigate, refresh }) {
  const [res, members] = await Promise.all([api.get(`/api/groups/${params.id}`), api.get(`/api/groups/${params.id}/members?limit=1000`)]);
  const g = res.group;
  const link = { profile: "#/profiles/", app: "#/apps/", declaration: "#/declarations/", compliance: "#/compliance/" };
  const typeLabel = { profile: "Profile", app: "App", declaration: "Declaration", compliance: "Compliance policy" };
  root.innerHTML = html`<div class="crumb"><a href="#/groups">Groups</a></div>
    <div class="page-head"><div><h1>${g.name}</h1><p class="sub">${g.description || KIND[g.kind] + " group"}</p></div>
      ${can("manage") ? html`<div class="actions">${g.kind === "static" ? html`<button class="btn btn-primary" id="add">Add devices</button>` : ""}<button class="btn" id="edit">Edit</button>${g.kind !== "all" ? html`<button class="btn btn-danger" id="del">Delete</button>` : ""}</div>` : ""}</div>
    ${g.kind === "dynamic" ? html`<div class="callout"><p>Devices join when they match ${g.rules?.match === "any" ? "any" : "all"} of: ${(g.rules?.rules || []).map((r, i) => html`${i ? ", " : ""}<strong>${r.field.replace("_", " ")}</strong> ${r.op} “${r.value}”`)}.</p></div>` : ""}
    <div class="grid-2">
      <section class="panel"><div class="panel-head"><h2>Members</h2><span class="hint">${plural(members.total, "device")}</span></div>
        ${members.items.length ? html`<div class="table-wrap" style="max-height:560px"><table class="table"><tbody>${members.items.map((d) => html`<tr><td><a class="primary" href="#/devices/${encodeURIComponent(d.udid)}">${deviceName(d)}</a><span class="cell-sub">${d.product_name}<span class="ident">${d.serial_number}</span></span></td>
          <td>${complianceChip(d.compliance)}</td><td class="right">${g.kind === "static" && can("manage") ? html`<button class="link-btn link-danger" data-rm="${d.udid}">Remove</button>` : ""}</td></tr>`)}</tbody></table></div>`
          : html`<p class="hint panel-pad" style="margin:0">${g.kind === "static" ? "Add devices to this group by hand, or from the device list." : "No devices match yet."}</p>`}</section>
      <section class="panel"><div class="panel-head"><h2>Assigned to this group</h2></div>
        ${res.assignments.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Item</th><th>Type</th><th>Intent</th></tr></thead><tbody>
          ${res.assignments.map((a) => html`<tr><td><a href="${link[a.item_type]}${a.item_id}">${a.item_name}</a></td><td>${typeLabel[a.item_type]}</td><td>${intentLabel(a.intent, a.item_type)}</td></tr>`)}</tbody></table></div>`
          : html`<p class="hint panel-pad" style="margin:0">Nothing assigned yet. Open a profile, app, declaration or compliance policy and assign it to this group.</p>`}</section>
    </div>`.s;
  $("#edit", root)?.addEventListener("click", () => editGroup(g, () => refresh()));
  $("#del", root)?.addEventListener("click", async () => {
    if (!(await confirmDialog(`Delete “${g.name}”?`, "Its assignments are deleted too. Profiles that reached devices only through this group will be removed from them.", { danger: true, confirmLabel: "Delete group" }))) return;
    try { await api.del(`/api/groups/${g.id}`); invalidateGroups(); toast("Group deleted"); navigate("/groups"); } catch (e) { toastError(e); }
  });
  $("#add", root)?.addEventListener("click", async () => {
    const ids = await pickDevices(`Add devices to ${g.name}`, "Add to group");
    if (!ids.length) return;
    try { await api.post(`/api/groups/${g.id}/members`, { udids: ids }); toast(`${plural(ids.length, "device")} added`); refresh(); } catch (e) { toastError(e); }
  });
  $$("[data-rm]", root).forEach((b) => b.addEventListener("click", async () => {
    try { await api.del(`/api/groups/${g.id}/members`, { udids: [b.dataset.rm] }); toast("Removed from group"); refresh(); } catch (e) { toastError(e); }
  }));
}
