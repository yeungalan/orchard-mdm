import { html, raw, $, $$, api, can, ago, fmtDate, stateChip, modal, toast, toastError, confirmDialog, fileToBase64, plural, emptyState, copyText } from "../lib.js";
import { assignmentsPanel } from "../components.js";

let schemaCache = null;
async function schemas() {
  if (!schemaCache) {
    schemaCache = await api.get("/api/profile-schemas");
    typeNames = Object.fromEntries(schemaCache.items.map((s) => [s.type, s.name]));
  }
  return schemaCache;
}
const short = (t) => t.replace("com.apple.", "");
let typeNames = {};
const typeName = (t) => typeNames[t] || short(t);
// "Add an Identity certificate (.p12) or SCEP certificate payload to this profile first."
function addFirstHint(types) {
  const known = types.filter((t) => typeNames[t]);
  const names = [...new Set((known.length ? known : types).map(typeName))];
  const article = /^[aeiou]/i.test(names[0] || "") ? "an" : "a";
  return `Add ${article} ${names.join(" or ")} payload to this profile first.`;
}

export async function render(ctx) {
  if (ctx.name === "profile") return editor(ctx);
  const { root, navigate } = ctx;
  const [res] = await Promise.all([api.get("/api/profiles"), schemas()]);
  root.innerHTML = html`<div class="page-head"><div><h1>Configuration profiles</h1><p class="sub">Wi-Fi, VPN, email, restrictions, passcode rules and more. Build profiles here or upload ones made in Apple Configurator, then assign them to groups.</p></div>
    ${can("manage") ? html`<div class="actions"><button class="btn" id="upload">Upload .mobileconfig</button><a class="btn btn-primary" href="#/profiles/new">New profile</a></div>` : ""}</div>
    <section class="panel">${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Profile</th><th>Contents</th><th class="num">Assignments</th><th>Deployment</th><th>Updated</th></tr></thead><tbody>
      ${res.items.map((p) => html`<tr><td><a class="primary" href="#/profiles/${p.id}">${p.name}</a><span class="cell-sub ident" style="margin-left:0">${p.identifier}</span></td>
        <td>${p.payload_types.slice(0, 4).map((t) => html`<span class="tag">${typeName(t)}</span>`)}${p.payload_types.length > 4 ? html`<span class="hint">+${p.payload_types.length - 4}</span>` : ""}</td>
        <td class="num">${p.assignments}</td>
        <td>${deployment(p.states)}</td><td class="nowrap">${ago(p.updated_at)}<span class="cell-sub">version ${p.version}</span></td></tr>`)}
    </tbody></table></div>` : emptyState("No profiles yet", "Create your first profile, for example office Wi-Fi or a passcode policy.", can("manage") ? html`<a class="btn btn-primary" href="#/profiles/new">New profile</a>` : "")}</section>`.s;
  $("#upload", root)?.addEventListener("click", () => uploadDialog((p) => navigate("/profiles/" + p.id)));
}

function deployment(states) {
  const parts = [];
  if (states.installed) parts.push(html`<span class="chip chip-good">${states.installed} installed</span>`);
  const pending = (states.pending || 0) + (states.removing || 0);
  if (pending) parts.push(html`<span class="chip chip-info">${pending} pending</span>`);
  const failed = (states.failed || 0) + (states.remove_failed || 0) + (states.missing || 0);
  if (failed) parts.push(html`<span class="chip chip-bad">${failed} failed</span>`);
  return parts.length ? html`<span class="row" style="gap:6px">${parts}</span>` : html`<span class="muted">Not deployed</span>`;
}

function uploadDialog(onDone, existing) {
  modal({
    title: existing ? "Replace profile file" : "Upload a profile",
    body: html`<label class="field"><span>.mobileconfig file</span><input type="file" name="file" accept=".mobileconfig,.plist,application/x-apple-aspen-config" required></label>
      <p class="hint">Signed and unsigned profiles are accepted. Uploading a profile with the same identifier as an existing one replaces it and redeploys it.
      Variables such as <span class="kbd">{{device.serial}}</span> in the XML are filled in per device.</p>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "u", label: "Upload", kind: "primary", submit: true, onClick: async (c) => {
      const file = $("form", c.el).file.files[0];
      if (!file) { toast("Choose a file", "err"); return false; }
      const res = await api.post("/api/profiles/upload", { data: await fileToBase64(file) });
      for (const w of res.warnings || []) toast(w, "err");
      toast(res.replaced ? "Profile replaced" : "Profile uploaded");
      onDone(res.profile);
    } }],
  });
}

// ---------- editor ----------

function uuid() {
  return (crypto.randomUUID ? crypto.randomUUID() : "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0; return (c === "x" ? r : (r & 3) | 8).toString(16);
  })).toUpperCase();
}

function defaultsFor(schema) {
  const values = {};
  for (const f of schema.fields) if (f.default !== undefined && f.default !== null) values[f.key] = f.default;
  return values;
}

function showIfOK(field, values, schema) {
  if (!field.show_if) return true;
  return Object.entries(field.show_if).every(([k, allowed]) => {
    let val = values[k];
    if (val === undefined || val === "" || val === null) val = schema.fields.find((f) => f.key === k)?.default;
    return allowed.map(String).includes(String(val));
  });
}

function fieldControl(f, value, path, state) {
  const name = f.key;
  const dk = `data-path="${path}"`;
  const label = html`${f.label}${f.supervised ? html`<span class="sup">supervised</span>` : ""}${f.required ? html` <span class="muted">(required)</span>` : ""}`;
  const help = f.help ? html`<small class="hint">${f.help}</small>` : "";
  const val = value ?? "";
  switch (f.type) {
    case "bool": case "intbool":
      return html`<label class="check"><input type="checkbox" ${raw(dk)} data-type="bool" ${value === true || value === 1 ? "checked" : ""}><span>${label}${help}</span></label>`;
    case "enum":
      return html`<label class="field"><span>${label}</span><select ${raw(dk)} data-type="enum"><option value="">Not set</option>${f.options.map((o) => html`<option value="${o.value}" ${String(o.value) === String(val) ? "selected" : ""}>${o.label}</option>`)}</select>${help}</label>`;
    case "multienum": {
      const sel = (Array.isArray(value) ? value : []).map(String);
      return html`<div class="field"><span class="field-label">${label}</span><div class="row">${f.options.map((o) => html`<label class="check" style="margin-right:12px"><input type="checkbox" data-multi="${path}" value="${o.value}" ${sel.includes(String(o.value)) ? "checked" : ""}><span>${o.label}</span></label>`)}</div>${help}</div>`;
    }
    case "int": case "real":
      return html`<label class="field"><span>${label}</span><input type="number" ${raw(dk)} data-type="${f.type}" value="${val}" step="${f.type === "real" ? "any" : "1"}">${help}</label>`;
    case "password": case "datastring":
      return html`<label class="field"><span>${label}</span><input type="password" ${raw(dk)} value="${val}" autocomplete="new-password">${help}</label>`;
    case "text": case "plistdict":
      return html`<label class="field"><span>${label}</span><textarea ${raw(dk)} class="${f.type === "plistdict" ? "code" : ""}" rows="${f.type === "plistdict" ? 8 : 3}">${val}</textarea>${help}</label>`;
    case "stringlist": case "dock":
      return html`<label class="field"><span>${label}</span><textarea ${raw(dk)} data-type="list" rows="3" placeholder="One per line">${Array.isArray(value) ? value.join("\n") : val}</textarea>${help}</label>`;
    case "pages":
      return html`<label class="field"><span>${label}</span><textarea ${raw(dk)} rows="6" class="code">${val}</textarea>${help}</label>`;
    case "data": case "image": case "cert":
      return html`<div class="field"><span class="field-label">${label}</span>
        ${value ? html`<span class="hint">File loaded (${Math.round((String(value).length * 3) / 4).toLocaleString()} bytes). Choose another to replace it.</span>` : ""}
        <input type="file" data-file="${path}" ${f.type === "image" ? 'accept="image/png,image/jpeg"' : f.type === "cert" ? 'accept=".cer,.crt,.pem,.der"' : ""}>${help}</div>`;
    case "payloadref": case "payloadrefs": {
      const opts = state.payloads.filter((p) => (f.ref_types || []).includes(p.type));
      const selected = Array.isArray(value) ? value : value ? [value] : [];
      if (!opts.length) return html`<div class="field"><span class="field-label">${label}</span><span class="hint">${addFirstHint(f.ref_types || [])}</span></div>`;
      if (f.type === "payloadrefs") return html`<div class="field"><span class="field-label">${label}</span>${opts.map((p) => html`<label class="check"><input type="checkbox" data-multi="${path}" value="${p.uuid}" ${selected.includes(p.uuid) ? "checked" : ""}><span>${p.display_name || typeName(p.type)}</span></label>`)}</div>`;
      return html`<label class="field"><span>${label}</span><select ${raw(dk)}><option value="">None</option>${opts.map((p) => html`<option value="${p.uuid}" ${selected.includes(p.uuid) ? "selected" : ""}>${p.display_name || typeName(p.type)}</option>`)}</select>${help}</label>`;
    }
    case "dictlist": {
      const rows = Array.isArray(value) ? value : [];
      return html`<div class="field"><span class="field-label">${label}</span>
        ${rows.map((row, i) => html`<div class="dictlist-row"><div class="inline-fields">${f.fields.map((sf) => fieldControl(sf, row[sf.key], `${path}|${i}|${sf.key}`, state))}</div>
          <button type="button" class="link-btn link-danger" data-rmrow="${path}|${i}" style="margin-bottom:8px">Remove row</button></div>`)}
        <button type="button" class="btn btn-sm" data-addrow="${path}">Add row</button>${help}</div>`;
    }
    default:
      return html`<label class="field"><span>${label}</span><input type="text" ${raw(dk)} value="${val}" ${name === "SSID_STR" ? "autocomplete=off" : ""}>${help}</label>`;
  }
}

function payloadCard(p, i, schema, state) {
  const sections = [];
  for (const f of schema.fields) {
    const sec = f.section || "";
    let s = sections.find((x) => x.name === sec);
    if (!s) sections.push((s = { name: sec, fields: [] }));
    s.fields.push(f);
  }
  return html`<article class="payload-card" data-pi="${i}">
    <header><h3>${schema.name}</h3><input type="text" data-display="${i}" value="${p.display_name || ""}" placeholder="Display name (optional)" style="max-width:260px" aria-label="Payload display name">
      ${can("manage") ? html`<button type="button" class="btn btn-ghost btn-sm" data-rmpayload="${i}">Remove</button>` : ""}</header>
    <div class="pc-body">${schema.description ? html`<p class="hint" style="margin-top:0">${schema.description}${schema.supervised ? " Supervised devices only." : !schema.user_enrollment ? " Not supported on personal devices enrolled with User Enrollment." : ""}</p>` : ""}
      ${sections.map((s) => html`${s.name ? html`<div class="section-label">${s.name}</div>` : ""}<div class="inline-fields">${s.fields.map((f) => html`<div data-fkey="${f.key}" ${!showIfOK(f, p.values, schema) ? raw('style="display:none"') : ""}>${fieldControl(f, p.values[f.key], `${i}|${f.key}`, state)}</div>`)}</div>`)}
    </div></article>`;
}

function setPath(state, path, value) {
  const parts = path.split("|");
  const p = state.payloads[Number(parts[0])];
  if (parts.length === 2) { p.values[parts[1]] = value; return; }
  const list = p.values[parts[1]] || (p.values[parts[1]] = []);
  list[Number(parts[2])][parts[3]] = value;
}
function getPath(state, path) {
  const parts = path.split("|");
  const p = state.payloads[Number(parts[0])];
  if (parts.length === 2) return p.values[parts[1]];
  return (p.values[parts[1]] || [])[Number(parts[2])]?.[parts[3]];
}

async function editor({ root, params, navigate, refresh }) {
  const sch = await schemas();
  const byType = Object.fromEntries(sch.items.map((s) => [s.type, s]));
  const isNew = !params.id;
  let profile = null, assignments = [], xml = "";
  const state = { payloads: [] };
  if (!isNew) {
    const res = await api.get(`/api/profiles/${params.id}`);
    profile = res.profile; xml = res.xml; assignments = res.assignments;
    // stored payloads omit values equal to their defaults (e.g. most restrictions); show them as they apply
    state.payloads = (profile.payloads || []).map((p) => ({ ...p, values: { ...(byType[p.type] ? defaultsFor(byType[p.type]) : {}), ...p.values } }));
  }
  const builder = isNew || profile.source === "builder";
  const editable = can("manage");
  root.innerHTML = html`<div class="crumb"><a href="#/profiles">Configuration profiles</a></div>
    <div class="page-head"><div><h1>${isNew ? "New profile" : profile.name}</h1>
      ${isNew ? html`<p class="sub">Add payloads, fill them in, and save. You can assign the profile to groups after saving.</p>` : html`<p class="sub"><span class="ident">${profile.identifier}</span><span style="margin-left:14px">Version ${profile.version}, ${profile.source === "upload" ? "uploaded" : "built here"}, updated ${ago(profile.updated_at)}</span></p>`}</div>
      <div class="actions">${!isNew ? html`<button class="btn" id="dl">Download</button>` : ""}${!isNew && editable ? html`<button class="btn btn-danger" id="del">Delete</button>` : ""}
        ${editable ? html`<button class="btn btn-primary" id="save">${isNew ? "Save profile" : "Save and redeploy"}</button>` : ""}</div></div>
    <div class="stack">
      <section class="panel panel-pad"><div class="inline-fields wide">
        <label class="field"><span>Name</span><input type="text" id="p-name" value="${profile?.name || ""}" required placeholder="e.g. Office Wi-Fi"></label>
        <label class="field"><span>Identifier</span><input type="text" id="p-ident" class="code" value="${profile?.identifier || ""}" ${isNew ? "" : "disabled"} placeholder="Generated if left blank"></label></div>
        <label class="field"><span>Description</span><input type="text" id="p-desc" value="${profile?.description || ""}" placeholder="Shown on the device in Settings"></label>
        ${builder ? html`<p class="hint" style="margin:0">Text fields accept variables, filled in per device: ${sch.variables.slice(0, 8).map((v) => html`<span class="kbd">{{${v}}}</span> `)}and more.</p>` : ""}
      </section>
      ${builder ? html`<div id="payloads"></div>${editable ? html`<div><button class="btn" id="add">Add payload</button></div>` : ""}`
        : html`<section class="panel panel-pad"><p style="margin-top:0">This profile was uploaded. Its contents: ${profile.payload_types.map((t) => html`<span class="tag">${typeName(t)}</span>`)}</p>${editable ? html`<button class="btn" id="replace">Replace file</button>` : ""}</section>`}
      ${!isNew ? html`<section id="assign"></section><section class="panel" id="status"></section>
        <details class="panel panel-pad"><summary><strong>Profile XML</strong> <span class="hint">(unsigned, before variables are filled in)</span></summary><pre class="code" style="margin-top:10px">${xml}</pre></details>` : ""}
    </div>`.s;

  const renderPayloads = () => {
    const host = $("#payloads", root);
    if (!host) return;
    host.innerHTML = (state.payloads.length ? html`${state.payloads.map((p, i) => byType[p.type] ? payloadCard(p, i, byType[p.type], state) : html`<div class="callout warn">Unknown payload type ${p.type}</div>`)}`
      : html`<section class="panel">${emptyState("No payloads yet", "A profile is made of payloads: add Wi-Fi, a passcode policy, restrictions, and so on.")}</section>`).s;
  };
  const updateVisibility = (pi) => {
    const p = state.payloads[pi], schema = byType[p.type];
    const card = $(`[data-pi="${pi}"]`, root);
    if (!card) return;
    for (const f of schema.fields) {
      const w = card.querySelector(`[data-fkey="${CSS.escape(f.key)}"]`);
      if (w) w.style.display = showIfOK(f, p.values, schema) ? "" : "none";
    }
  };
  renderPayloads();

  const host = $("#payloads", root);
  host?.addEventListener("input", (e) => {
    const t = e.target;
    if (t.dataset.display !== undefined) { state.payloads[Number(t.dataset.display)].display_name = t.value; return; }
    if (!t.dataset.path) return;
    let val = t.value;
    if (t.dataset.type === "bool") val = t.checked;
    else if (t.dataset.type === "list") val = t.value.split("\n").map((s) => s.trim()).filter(Boolean);
    else if ((t.dataset.type === "int" || t.dataset.type === "real") && val !== "") val = Number(val);
    setPath(state, t.dataset.path, val);
    updateVisibility(Number(t.dataset.path.split("|")[0]));
  });
  host?.addEventListener("change", async (e) => {
    const t = e.target;
    if (t.dataset.multi) {
      const vals = $$(`[data-multi="${CSS.escape(t.dataset.multi)}"]:checked`, host).map((i) => i.value);
      setPath(state, t.dataset.multi, vals);
    } else if (t.dataset.file && t.files[0]) {
      setPath(state, t.dataset.file, await fileToBase64(t.files[0]));
      toast("File attached");
    } else if (t.dataset.path) {
      let val = t.type === "checkbox" ? t.checked : t.value;
      if (t.dataset.type === "int" && val !== "") val = Number(val);
      if (t.dataset.type === "list") val = t.value.split("\n").map((s) => s.trim()).filter(Boolean);
      setPath(state, t.dataset.path, val);
      updateVisibility(Number(t.dataset.path.split("|")[0]));
    }
  });
  host?.addEventListener("click", (e) => {
    const add = e.target.closest("[data-addrow]");
    if (add) { const cur = getPath(state, add.dataset.addrow) || []; cur.push({}); setPath(state, add.dataset.addrow, cur); renderPayloads(); return; }
    const rm = e.target.closest("[data-rmrow]");
    if (rm) { const parts = rm.dataset.rmrow.split("|"); const path = parts.slice(0, -1).join("|"); const cur = getPath(state, path) || []; cur.splice(Number(parts.at(-1)), 1); setPath(state, path, cur); renderPayloads(); return; }
    const rp = e.target.closest("[data-rmpayload]");
    if (rp) { state.payloads.splice(Number(rp.dataset.rmpayload), 1); renderPayloads(); }
  });

  $("#add", root)?.addEventListener("click", () => {
    const cats = [...new Set(sch.items.map((s) => s.category))];
    const ctl = modal({ title: "Add payload", wide: true, body: html`${cats.map((c) => html`<div class="section-label">${c}</div><div class="picker">${sch.items.filter((s) => s.category === c).map((s) => {
      const taken = s.unique && state.payloads.some((p) => p.type === s.type);
      return html`<button type="button" data-type="${s.type}" ${taken ? "disabled" : ""}><span>${s.name}${s.supervised ? html`<span class="sup">supervised</span>` : ""}${!s.user_enrollment && !s.supervised ? html`<span class="sup">not on BYOD</span>` : ""}</span><small>${taken ? "Already in this profile" : s.description || short(s.type)}</small></button>`;
    })}</div>`)}` });
    ctl.el.addEventListener("click", (e) => {
      const b = e.target.closest("[data-type]");
      if (!b || b.disabled) return;
      const s = byType[b.dataset.type];
      state.payloads.push({ type: s.type, uuid: uuid(), display_name: "", values: defaultsFor(s) });
      ctl.close();
      renderPayloads();
      $(`[data-pi="${state.payloads.length - 1}"]`, root)?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  });

  $("#save", root)?.addEventListener("click", async (e) => {
    const body = { name: $("#p-name", root).value.trim(), identifier: $("#p-ident", root).value.trim(), description: $("#p-desc", root).value.trim(), payloads: state.payloads };
    if (!body.name) return toast("Give the profile a name", "err");
    e.target.disabled = true;
    try {
      const res = isNew ? await api.post("/api/profiles", body) : await api.put(`/api/profiles/${params.id}`, body);
      for (const w of res.warnings || []) toast(w, "err");
      toast(isNew ? "Profile saved. Assign it to a group to deploy it." : "Saved. Devices with this profile get the new version.");
      if (isNew) navigate("/profiles/" + res.profile.id); else refresh();
    } catch (err) { toastError(err); } finally { e.target.disabled = false; }
  });
  $("#dl", root)?.addEventListener("click", () => api.download("GET", `/api/profiles/${params.id}/download?signed=1`, undefined, "profile.mobileconfig").catch(toastError));
  $("#del", root)?.addEventListener("click", async () => {
    if (!(await confirmDialog(`Delete “${profile.name}”?`, "It will be removed from every device it was installed on.", { danger: true, confirmLabel: "Delete profile" }))) return;
    try { await api.del(`/api/profiles/${params.id}`); toast("Profile deleted"); navigate("/profiles"); } catch (err) { toastError(err); }
  });
  $("#replace", root)?.addEventListener("click", () => uploadDialog(() => refresh(), profile));

  if (!isNew) {
    await assignmentsPanel($("#assign", root), "profile", params.id, { onChange: () => setTimeout(loadStatus, 2500) });
    const loadStatus = async () => {
      const st = await api.get(`/api/profiles/${params.id}/status`);
      const failed = st.items.filter((s) => ["failed", "remove_failed", "missing"].includes(s.status)).length;
      $("#status", root).innerHTML = html`<div class="panel-head"><h2>Deployment</h2>${failed && can("manage") ? html`<button class="btn btn-sm" id="retry">Retry ${failed} failed</button>` : ""}</div>
        ${st.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Device</th><th>Version</th><th>State</th><th>Updated</th></tr></thead><tbody>
        ${st.items.map((s) => html`<tr><td><a href="#/devices/${encodeURIComponent(s.device_id)}">${s.device_name || s.device_id}</a></td><td>${s.version}</td><td>${stateChip(s.status)}${s.error ? html`<span class="cell-sub">${s.error}</span>` : ""}</td><td>${ago(s.updated_at)}</td></tr>`)}
        </tbody></table></div>` : html`<p class="hint panel-pad" style="margin:0">Not sent to any device yet.</p>`}`.s;
      $("#retry", root)?.addEventListener("click", async () => {
        try { const r = await api.post(`/api/profiles/${params.id}/retry`); toast(`${plural(r.reset, "device")} will be retried`); setTimeout(loadStatus, 2500); } catch (e) { toastError(e); }
      });
    };
    await loadStatus();
  }
}

export { fmtDate, copyText };
