import { html, $, api, can, ago, modal, toast, toastError, confirmDialog, emptyState } from "../lib.js";
import { assignmentsPanel } from "../components.js";

const KIND = { configuration: "Configuration", asset: "Asset", activation: "Activation", management: "Management" };

export async function render(ctx) {
  if (ctx.name === "declaration") return detail(ctx);
  const { root, navigate } = ctx;
  const [res, tpl] = await Promise.all([api.get("/api/declarations"), api.get("/api/declaration-templates").catch(() => ({ items: [] }))]);
  const typeName = Object.fromEntries(tpl.items.map((t) => [t.type, t.name]));
  root.innerHTML = html`<div class="page-head"><div><h1>Declarations</h1><p class="sub">Declarative Device Management (iOS 16 and later): devices apply these configurations on their own and report status back.
    Orchard automatically activates assigned configurations and subscribes to battery health, OS version, passcode and software-update status.</p></div>
    ${can("manage") ? html`<div class="actions"><button class="btn btn-primary" id="new">New declaration</button></div>` : ""}</div>
    <section class="panel">${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Declaration</th><th>Kind</th><th>Type</th><th class="num">Assignments</th><th>Updated</th></tr></thead><tbody>
      ${res.items.map((d) => html`<tr><td><a class="primary" href="#/declarations/${d.id}">${d.name}</a><span class="cell-sub ident" style="margin-left:0">${d.identifier}</span></td><td>${KIND[d.kind]}</td><td>${typeName[d.type] || d.type.replace("com.apple.", "")}${typeName[d.type] ? html`<span class="cell-sub ident" style="margin-left:0">${d.type.replace("com.apple.", "")}</span>` : ""}</td><td class="num">${d.assignments}</td><td>${ago(d.updated_at)}</td></tr>`)}
    </tbody></table></div>` : emptyState("No declarations yet", "Start from a template, such as enforcing an iOS update by a deadline.", can("manage") ? html`<button class="btn btn-primary" id="new2">New declaration</button>` : "")}</section>`.s;
  const open = async () => {
    const t = await api.get("/api/declaration-templates");
    const ctl = modal({ title: "New declaration", wide: true, body: html`<p class="hint">Pick a starting point. You can edit the JSON before saving.</p>
      <div class="picker">${t.items.map((x, i) => html`<button type="button" data-i="${i}">${x.name}<small>${x.description}</small></button>`)}</div>` });
    ctl.el.addEventListener("click", async (e) => {
      const b = e.target.closest("[data-i]");
      if (!b) return;
      const x = t.items[Number(b.dataset.i)];
      try {
        const d = await api.post("/api/declarations", { type: x.type, name: x.name, payload: x.payload });
        ctl.close(); toast("Declaration created. Review it, then assign it."); navigate("/declarations/" + d.id);
      } catch (err) { toastError(err); }
    });
  };
  $("#new", root)?.addEventListener("click", open);
  $("#new2", root)?.addEventListener("click", open);
}

async function detail({ root, params, navigate }) {
  const res = await api.get(`/api/declarations/${params.id}`);
  const d = res.declaration;
  const editable = can("manage");
  root.innerHTML = html`<div class="crumb"><a href="#/declarations">Declarations</a></div>
    <div class="page-head"><div><h1>${d.name}</h1><p class="sub"><span class="ident">${d.identifier}</span><span style="margin-left:14px">${KIND[res.kind]}, updated ${ago(d.updated_at)}</span></p></div>
      ${editable ? html`<div class="actions"><button class="btn btn-danger" id="del">Delete</button></div>` : ""}</div>
    <div class="grid-2 top">
      <section class="panel"><div class="panel-head"><h2>Definition</h2></div><form class="panel-pad" id="f">
        <label class="field"><span>Name</span><input type="text" name="name" value="${d.name}" ${editable ? "" : "disabled"}></label>
        <label class="field"><span>Type</span><input type="text" name="type" class="code" value="${d.type}" ${editable ? "" : "disabled"}></label>
        <label class="field"><span>Description</span><input type="text" name="description" value="${d.description}" ${editable ? "" : "disabled"}></label>
        <label class="field"><span>Payload (JSON)</span><textarea name="payload" class="code" rows="14" ${editable ? "" : "disabled"}>${JSON.stringify(d.payload, null, 2)}</textarea>
          <small class="hint">See Apple's device-management documentation for the keys of each declaration type. Saving bumps the server token, so devices fetch the new version.</small></label>
        ${editable ? html`<button class="btn btn-primary" type="submit">Save and redeploy</button>` : ""}
      </form></section>
      <section id="assign"></section>
    </div>`.s;
  await assignmentsPanel($("#assign", root), "declaration", params.id);
  $("#f", root).addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    let payload;
    try { payload = JSON.parse(f.payload.value || "{}"); } catch { return toast("The payload is not valid JSON", "err"); }
    try { await api.put(`/api/declarations/${d.id}`, { name: f.name.value, type: f.type.value, description: f.description.value, payload }); toast("Saved. Devices sync the change shortly."); } catch (err) { toastError(err); }
  });
  $("#del", root)?.addEventListener("click", async () => {
    if (!(await confirmDialog(`Delete “${d.name}”?`, "Devices remove the configuration at their next sync.", { danger: true, confirmLabel: "Delete" }))) return;
    try { await api.del(`/api/declarations/${d.id}`); toast("Declaration deleted"); navigate("/declarations"); } catch (err) { toastError(err); }
  });
}
