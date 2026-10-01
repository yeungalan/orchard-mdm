import { html, $, $$, api, can, session, ago, bytes, stateChip, modal, toast, toastError, confirmDialog, plural, emptyState, debounce, copyText } from "../lib.js";
import { assignmentsPanel, pickDevices } from "../components.js";

const KIND = { appstore: "App Store", vpp: "App Store (licensed)", enterprise: "In-house" };

export async function render(ctx) {
  if (ctx.name === "app") return detail(ctx);
  const { root, navigate } = ctx;
  const [res, vpp] = await Promise.all([api.get("/api/apps"), api.get("/api/vpp/assets").catch(() => ({ items: [] }))]);
  root.innerHTML = html`<div class="page-head"><div><h1>Apps</h1><p class="sub">Your app catalog. Assign apps as required (installed automatically), available (offered in the Company Portal) or remove.</p></div>
    ${can("manage") ? html`<div class="actions"><button class="btn" id="ipa">Upload in-house app</button><button class="btn btn-primary" id="store">Add from App Store</button></div>` : ""}</div>
    <section class="panel">${res.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>App</th><th>Source</th><th class="num">On devices</th><th class="num">Assignments</th><th>Deployment</th></tr></thead><tbody>
      ${res.items.map((a) => html`<tr><td><div class="app-cell">${a.icon_url ? html`<img class="app-icon" src="${a.icon_url}" alt="" loading="lazy">` : html`<span class="app-icon"></span>`}<div><a class="primary" href="#/apps/${a.id}">${a.name}</a><span class="cell-sub ident" style="margin-left:0">${a.bundle_id}</span></div></div></td>
        <td>${KIND[a.kind]}${a.use_vpp ? html`<span class="cell-sub">uses Apps and Books licenses</span>` : ""}</td><td class="num">${a.installed}</td><td class="num">${a.assignments}</td>
        <td>${deployment(a.states)}</td></tr>`)}
    </tbody></table></div>` : emptyState("No apps yet", "Add an App Store app, upload an in-house .ipa, or connect Apps and Books to use purchased licenses.")}</section>
    ${vpp.items.length ? html`<h2 class="section">Apps and Books licenses</h2><section class="panel"><div class="table-wrap"><table class="table"><thead><tr><th>App</th><th class="num">Assigned</th><th class="num">Available</th><th class="num">Total</th><th></th></tr></thead><tbody>
      ${vpp.items.map((v) => html`<tr><td><div class="app-cell">${v.icon_url ? html`<img class="app-icon" src="${v.icon_url}" alt="" loading="lazy">` : html`<span class="app-icon"></span>`}<div><strong>${v.name || "App " + v.adam_id}</strong><span class="cell-sub">${v.product_type} ${v.adam_id}</span></div></div></td>
        <td class="num">${v.assigned_count}</td><td class="num">${v.available_count}</td><td class="num">${v.total_count}</td>
        <td class="right">${can("manage") && !res.items.some((a) => String(a.itunes_id) === v.adam_id) ? html`<button class="btn btn-sm" data-add-vpp="${v.adam_id}">Add to catalog</button>` : ""}</td></tr>`)}
    </tbody></table></div></section>` : ""}`.s;
  $("#store", root)?.addEventListener("click", () => storeDialog((a) => navigate("/apps/" + a.id)));
  $("#ipa", root)?.addEventListener("click", () => ipaDialog((a) => navigate("/apps/" + a.id)));
  $$("[data-add-vpp]", root).forEach((b) => b.addEventListener("click", async () => {
    try { const a = await api.post("/api/apps", { itunes_id: b.dataset.addVpp, kind: "vpp" }); toast(`${a.name} added`); navigate("/apps/" + a.id); } catch (e) { toastError(e); }
  }));
}

function deployment(st) {
  const parts = [];
  if (st.installed) parts.push(html`<span class="chip chip-good">${st.installed} installed</span>`);
  const busy = (st.pending || 0) + (st.installing || 0) + (st.removing || 0);
  if (busy) parts.push(html`<span class="chip chip-info">${busy} in progress</span>`);
  const bad = (st.failed || 0) + (st.missing || 0);
  if (bad) parts.push(html`<span class="chip chip-bad">${bad} failed</span>`);
  return parts.length ? html`<span class="row" style="gap:6px">${parts}</span>` : html`<span class="muted">Not deployed</span>`;
}

function storeDialog(onAdded) {
  const ctl = modal({ title: "Add from the App Store", wide: true,
    body: html`<div class="row"><input type="search" id="term" placeholder="App name, App Store link, ID or bundle ID" style="flex:1" autofocus>
      <select id="country" style="width:auto" aria-label="Store country">${["us", "gb", "jp", "hk", "tw", "cn", "au", "ca", "de", "fr", "sg", "kr"].map((c) => html`<option value="${c}">${c.toUpperCase()}</option>`)}</select></div>
      <label class="check"><input type="checkbox" id="vpp"><span>Use Apps and Books licenses<small>Installs without an Apple Account on the device. Requires licenses for this app.</small></span></label>
      <div id="results" style="margin-top:12px"></div>`, actions: [{ id: "c", label: "Close" }] });
  const el = ctl.el;
  const search = debounce(async () => {
    const term = $("#term", el).value.trim();
    if (term.length < 2) return;
    try {
      const res = await api.get(`/api/apps/search?term=${encodeURIComponent(term)}&country=${$("#country", el).value}`);
      $("#results", el).innerHTML = (res.items.length ? html`<div class="table-wrap"><table class="table"><tbody>${res.items.map((a) => html`<tr><td><div class="app-cell"><img class="app-icon" src="${a.artworkUrl100}" alt="" loading="lazy"><div><strong>${a.trackName}</strong><span class="cell-sub">${a.sellerName}</span></div></div></td>
        <td class="ident">${a.bundleId}</td><td class="right"><button type="button" class="btn btn-sm btn-primary" data-id="${a.trackId}">Add</button></td></tr>`)}</tbody></table></div>` : html`<p class="hint">No apps found.</p>`).s;
    } catch (e) { toastError(e); }
  }, 400);
  $("#term", el).addEventListener("input", search);
  $("#country", el).addEventListener("change", search);
  el.addEventListener("click", async (e) => {
    const b = e.target.closest("[data-id]");
    if (!b) return;
    b.disabled = true;
    try {
      const a = await api.post("/api/apps", { itunes_id: b.dataset.id, country: $("#country", el).value, kind: $("#vpp", el).checked ? "vpp" : "appstore" });
      toast(`${a.name} added`); ctl.close(); onAdded(a);
    } catch (err) { toastError(err); b.disabled = false; }
  });
}

function ipaDialog(onAdded) {
  modal({ title: "Upload an in-house app",
    body: html`<label class="field"><span>.ipa file</span><input type="file" name="file" accept=".ipa" required></label>
      <p class="hint">Enterprise-signed (Apple Developer Enterprise Program) or ad-hoc builds. Orchard hosts the file and its install manifest; devices download it over HTTPS.</p>
      <progress id="prog" max="100" value="0" style="width:100%;display:none"></progress>`,
    actions: [{ id: "c", label: "Cancel" }, { id: "u", label: "Upload", kind: "primary", submit: true, onClick: (c) => new Promise((resolve, reject) => {
      const file = $("form", c.el).file.files[0];
      if (!file) { toast("Choose an .ipa file", "err"); return resolve(false); }
      const fd = new FormData();
      fd.append("file", file);
      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/apps/enterprise");
      xhr.setRequestHeader("X-CSRF-Token", session.csrf);
      const prog = $("#prog", c.el);
      prog.style.display = "";
      xhr.upload.onprogress = (e) => { if (e.lengthComputable) prog.value = (e.loaded / e.total) * 100; };
      xhr.onload = () => {
        let body = {};
        try { body = JSON.parse(xhr.responseText); } catch {}
        if (xhr.status === 200) { toast(`${body.name} uploaded`); onAdded(body); resolve(); }
        else reject(new Error(body.error || xhr.statusText));
      };
      xhr.onerror = () => reject(new Error("Upload failed"));
      xhr.send(fd);
    }) }] });
}

async function detail({ root, params, navigate, refresh }) {
  const res = await api.get(`/api/apps/${params.id}`);
  const a = res.app;
  const editable = can("manage");
  root.innerHTML = html`<div class="crumb"><a href="#/apps">Apps</a></div>
    <div class="page-head"><div class="app-cell" style="align-items:flex-start;gap:14px">${a.icon_url ? html`<img class="app-icon" style="width:56px;height:56px;border-radius:13px" src="${a.icon_url}" alt="">` : ""}
      <div><h1>${a.name}</h1><p class="sub">${KIND[a.kind]}${a.seller ? `, ${a.seller}` : ""}${a.version ? `, version ${a.version}` : ""}</p><p class="sub ident">${a.bundle_id}</p></div></div>
      <div class="actions">${can("act") ? html`<button class="btn" id="install">Install on devices</button>` : ""}${editable ? html`<button class="btn btn-danger" id="del">Delete</button>` : ""}</div></div>
    <div class="grid-2">
      <section class="panel"><div class="panel-head"><h2>Settings</h2></div><form class="panel-pad" id="settings">
        <label class="check"><input type="checkbox" name="remove_on_unenroll" ${a.remove_on_unenroll ? "checked" : ""} ${editable ? "" : "disabled"}><span>Remove the app when the device leaves management</span></label>
        <label class="check"><input type="checkbox" name="prevent_backup" ${a.prevent_backup ? "checked" : ""} ${editable ? "" : "disabled"}><span>Prevent backup of app data</span></label>
        <label class="check"><input type="checkbox" name="take_management" ${a.take_management ? "checked" : ""} ${editable ? "" : "disabled"}><span>Take over management if the user already installed it</span></label>
        ${a.kind !== "enterprise" ? html`<label class="check"><input type="checkbox" name="use_vpp" ${a.use_vpp ? "checked" : ""} ${editable ? "" : "disabled"}><span>Install with an Apps and Books license<small>No Apple Account needed on the device.</small></span></label>` : ""}
        <label class="field" style="margin-top:12px"><span>Managed app configuration (JSON)</span>
          <textarea name="config" class="code" rows="8" ${editable ? "" : "disabled"} placeholder='{"ServerURL": "https://example.com", "DeviceSerial": "{{device.serial}}"}'>${a.config ? JSON.stringify(a.config, null, 2) : ""}</textarea>
          <small class="hint">Delivered to the app as com.apple.configuration.managed. Variables are filled in per device; a companion app can use <span class="kbd">{{device.agent_token}}</span> and <span class="kbd">{{server.agent_url}}</span> to report Wi-Fi and location.</small></label>
        <label class="field"><span>App attributes (JSON)</span><textarea name="attributes" class="code" rows="3" ${editable ? "" : "disabled"} placeholder='{"VPNUUID": "…", "AssociatedDomains": ["example.com"]}'>${a.attributes ? JSON.stringify(a.attributes, null, 2) : ""}</textarea></label>
        ${editable ? html`<button class="btn btn-primary" type="submit">Save settings</button>` : ""}
      </form></section>
      <div class="stack"><section id="assign"></section>
        <section class="panel"><div class="panel-head"><h2>Details</h2></div><div class="panel-pad"><dl class="kv">
          ${a.itunes_id ? html`<dt>App Store ID</dt><dd><a href="https://apps.apple.com/app/id${a.itunes_id}" target="_blank" rel="noopener">${a.itunes_id}</a></dd>` : ""}
          ${a.kind === "enterprise" ? html`<dt>Package</dt><dd>${bytes(a.ipa_size)}</dd><dt>Manifest URL</dt><dd><button class="link-btn" id="copy-manifest">Copy</button></dd>` : ""}
          ${res.licenses ? html`<dt>Licenses</dt><dd>${res.licenses.assigned_count} assigned, ${res.licenses.available_count} available of ${res.licenses.total_count}</dd>` : ""}
          <dt>Added</dt><dd>${ago(a.created_at)}</dd></dl>
          ${a.description ? html`<details><summary>Description</summary><p style="white-space:pre-wrap">${a.description}</p></details>` : ""}</div></section></div>
    </div>
    <section class="panel" id="status" style="margin-top:16px"></section>`.s;
  await assignmentsPanel($("#assign", root), "app", params.id, { onChange: () => setTimeout(loadStatus, 2500) });
  $("#copy-manifest", root)?.addEventListener("click", () => copyText(res.manifest_url));
  $("#settings", root).addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    const parse = (txt, what) => { if (!txt.trim()) return {}; try { return JSON.parse(txt); } catch { throw new Error(`${what} is not valid JSON`); } };
    try {
      await api.put(`/api/apps/${a.id}`, {
        remove_on_unenroll: f.remove_on_unenroll.checked, prevent_backup: f.prevent_backup.checked, take_management: f.take_management.checked,
        use_vpp: f.use_vpp ? f.use_vpp.checked : false, config: parse(f.config.value, "Managed app configuration"), attributes: parse(f.attributes.value, "App attributes"),
      });
      toast("Settings saved");
    } catch (err) { toastError(err); }
  });
  $("#del", root)?.addEventListener("click", async () => {
    if (!(await confirmDialog(`Delete “${a.name}” from the catalog?`, "Assignments are removed. The app stays on devices that already have it.", { danger: true, confirmLabel: "Delete app" }))) return;
    try { await api.del(`/api/apps/${a.id}`); toast("App deleted"); navigate("/apps"); } catch (e) { toastError(e); }
  });
  $("#install", root)?.addEventListener("click", async () => {
    const ids = await pickDevices(`Install ${a.name}`, "Install");
    if (!ids.length) return;
    try {
      const r = await api.post(`/api/apps/${a.id}/install`, { udids: ids });
      const failed = r.results.filter((x) => x.error);
      toast(failed.length ? `${failed.length} failed: ${failed[0].error}` : `Install queued for ${plural(ids.length, "device")}`, failed.length ? "err" : "ok");
    } catch (e) { toastError(e); }
  });
  const loadStatus = async () => {
    const st = await api.get(`/api/apps/${params.id}/status`);
    const failed = st.items.filter((s) => s.status === "failed" || s.status === "missing").length;
    $("#status", root).innerHTML = html`<div class="panel-head"><h2>Deployment</h2>${failed && can("manage") ? html`<button class="btn btn-sm" id="retry">Retry ${failed} failed</button>` : ""}</div>
      ${st.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Device</th><th>Intent</th><th>State</th><th class="num">Attempts</th><th>Updated</th></tr></thead><tbody>
      ${st.items.map((s) => html`<tr><td><a href="#/devices/${encodeURIComponent(s.device_id)}">${s.device_name || s.device_id}</a></td><td>${s.intent}</td><td>${stateChip(s.status)}${s.error ? html`<span class="cell-sub">${s.error}</span>` : ""}</td><td class="num">${s.attempts}</td><td>${ago(s.updated_at)}</td></tr>`)}
      </tbody></table></div>` : html`<p class="hint panel-pad" style="margin:0">Not deployed to any device yet.</p>`}`.s;
    $("#retry", root)?.addEventListener("click", async () => {
      try { await api.post(`/api/apps/${params.id}/retry`); toast("Failed installs will be retried"); setTimeout(loadStatus, 2500); } catch (e) { toastError(e); }
    });
  };
  await loadStatus();
}
