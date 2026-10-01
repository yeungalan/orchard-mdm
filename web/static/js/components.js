// Reusable console components: command dialog, assignments panel, group picker.
import { html, $, $$, api, can, modal, toast, toastError, fileToBase64, confirmDialog, plural, esc } from "./lib.js";

let catalogCache = null;
export async function catalog() {
  if (!catalogCache) catalogCache = await api.get("/api/command-catalog");
  return catalogCache;
}

let groupsCache = null;
export async function groups(force = false) {
  if (!groupsCache || force) groupsCache = (await api.get("/api/groups")).items;
  return groupsCache;
}
export function invalidateGroups() { groupsCache = null; }

function paramInput(p, refs) {
  const help = p.help ? html`<small class="hint">${p.help}</small>` : "";
  switch (p.type) {
    case "bool":
      return html`<label class="check"><input type="checkbox" name="${p.key}" ${p.default ? "checked" : ""}><span>${p.label}${help}</span></label>`;
    case "enum":
      return html`<label class="field"><span>${p.label}</span><select name="${p.key}">${p.options.map((o) => html`<option ${o === p.default ? "selected" : ""}>${o}</option>`)}</select>${help}</label>`;
    case "text":
      return html`<label class="field"><span>${p.label}</span><textarea name="${p.key}" class="code" rows="6" ${p.required ? "required" : ""}></textarea>${help}</label>`;
    case "stringlist":
      return html`<label class="field"><span>${p.label}</span><textarea name="${p.key}" rows="3" placeholder="One per line"></textarea>${help}</label>`;
    case "int":
      return html`<label class="field"><span>${p.label}</span><input type="number" name="${p.key}">${help}</label>`;
    case "file": case "image":
      return html`<label class="field"><span>${p.label}</span><input type="file" name="${p.key}" ${p.type === "image" ? 'accept="image/png,image/jpeg"' : ""}>${help}</label>`;
    case "app":
      return html`<label class="field"><span>${p.label}</span><select name="${p.key}" required><option value="">Choose an app…</option>${refs.apps.map((a) => html`<option value="${a.id}">${a.name}</option>`)}</select>${help}</label>`;
    case "profile":
      return html`<label class="field"><span>${p.label}</span><select name="${p.key}"><option value="">Not selected</option>${refs.profiles.map((a) => html`<option value="${a.id}">${a.name}</option>`)}</select>${help}</label>`;
    default:
      return html`<label class="field"><span>${p.label}</span><input type="text" name="${p.key}" ${p.required ? "required" : ""}>${help}</label>`;
  }
}

async function readParams(form, spec) {
  const out = {};
  for (const p of spec.params || []) {
    const el = form.elements[p.key];
    if (!el) continue;
    if (p.type === "bool") out[p.key] = el.checked;
    else if (p.type === "file" || p.type === "image") {
      if (el.files && el.files[0]) out[p.key] = await fileToBase64(el.files[0]);
    } else if (p.type === "int" || p.type === "app" || p.type === "profile") {
      if (el.value !== "") out[p.key] = Number(el.value);
    } else if (el.value !== "") out[p.key] = el.value;
  }
  return out;
}

/** Opens the command picker for one or more devices. */
export async function commandDialog(udids, { only, preselect, onDone, supervised, userEnrollment } = {}) {
  const cat = await catalog();
  let specs = cat.items;
  if (userEnrollment) specs = specs.filter((s) => s.user_enrollment);
  if (udids.length > 1) specs = specs.filter((s) => s.bulk);
  if (only) specs = specs.filter((s) => only.includes(s.id));
  const refs = { apps: [], profiles: [] };
  if (specs.some((s) => s.params?.some((p) => p.type === "app" || p.type === "profile"))) {
    const [a, p] = await Promise.all([api.get("/api/apps").catch(() => ({ items: [] })), api.get("/api/profiles").catch(() => ({ items: [] }))]);
    refs.apps = a.items; refs.profiles = p.items;
  }
  const bulk = udids.length > 1;
  let current = null;
  const listView = () => html`<p class="hint">${bulk ? `Send a command to ${plural(udids.length, "device")}.` : "Choose what to send to this device."} Commands are queued and delivered the next time the device checks in.</p>
    ${userEnrollment ? html`<p class="hint">This is a personal device enrolled with User Enrollment, so only commands that act on work apps and data are listed.</p>` : ""}
    ${cat.categories.map((c) => {
      const items = specs.filter((s) => s.category === c);
      if (!items.length) return "";
      return html`<div class="section-label">${c}</div><div class="picker">${items.map((s) => html`<button type="button" data-spec="${s.id}">${s.label}${s.supervised ? html`<span class="sup">supervised</span>` : ""}<small>${s.description}</small></button>`)}</div>`;
    })}`;
  const formView = (s) => html`<p style="margin-top:0"><strong>${s.label}</strong>${s.supervised ? html` <span class="sup">supervised devices only</span>` : ""}</p><p class="hint">${s.description}</p>
    ${s.params?.length ? s.params.map((p) => paramInput(p, refs)) : html`<p>This command has no options.</p>`}
    ${supervised === false && s.supervised ? html`<div class="callout warn"><p>This device isn't supervised, so it will probably reject this command.</p></div>` : ""}`;
  const showList = () => {
    current = null;
    $("#cmd-body", ctl.el).innerHTML = listView().s;
    $('[data-act="send"]', ctl.el).style.display = "none";
    $('[data-act="back"]', ctl.el).style.display = "none";
  };
  const ctl = modal({
    title: bulk ? "Send command to devices" : "Send command",
    wide: true,
    body: html`<div id="cmd-body"></div>`,
    actions: [
      { id: "back", label: "Back", onClick: () => { showList(); return false; } },
      { id: "send", label: "Send", kind: "primary", submit: true, onClick: async () => {
        if (!current) return false;
        const form = $("form", ctl.el);
        for (const p of current.params || []) {
          const el = form.elements[p.key];
          if (p.required && el && !el.value && !(el.files && el.files.length)) { toast(`${p.label} is required`, "err"); return false; }
        }
        if (current.confirm && !(await confirmDialog(`Send “${current.label}”?`, bulk ? `This goes to ${plural(udids.length, "device")}.` : "The device acts on it as soon as it receives it.", { confirmLabel: "Send", danger: true }))) return false;
        const params = await readParams(form, current);
        if (bulk) {
          const res = await api.post("/api/devices/bulk", { udids, command: current.id, params });
          const failed = res.results.filter((r) => r.error);
          toast(failed.length ? `Queued for ${udids.length - failed.length}; ${failed.length} failed: ${failed[0].error}` : `Queued for ${plural(udids.length, "device")}`, failed.length ? "err" : "ok");
        } else {
          await api.post(`/api/devices/${encodeURIComponent(udids[0])}/commands`, { command: current.id, params });
          toast(`${current.label} queued`);
        }
        onDone && onDone();
      } },
    ],
  });
  const showForm = (id) => {
    current = specs.find((s) => s.id === id);
    $("#cmd-body", ctl.el).innerHTML = formView(current).s;
    $('[data-act="send"]', ctl.el).style.display = "";
    $('[data-act="back"]', ctl.el).style.display = only && only.length === 1 ? "none" : "";
  };
  $("#cmd-body", ctl.el).addEventListener("click", (e) => { const b = e.target.closest("[data-spec]"); if (b) showForm(b.dataset.spec); });
  if (preselect) showForm(preselect);
  else if (specs.length === 1) showForm(specs[0].id);
  else showList();
}

const INTENT_LABEL = { install: "Required", available: "Available in Company Portal", uninstall: "Remove", exclude: "Exclude" };
const INTENTS = {
  profile: ["install", "exclude"], app: ["install", "available", "uninstall", "exclude"], declaration: ["install", "exclude"], compliance: ["install", "exclude"],
};
export const intentLabel = (i, type) => (type !== "app" && i === "install" ? (type === "compliance" ? "Evaluate" : "Install") : INTENT_LABEL[i] || i);

/** Renders and manages the assignments of an item. */
export async function assignmentsPanel(host, itemType, itemID, { onChange } = {}) {
  host.classList.add("panel");
  const load = async () => {
    const [as, gs] = await Promise.all([api.get(`/api/assignments?item_type=${itemType}&item_id=${itemID}`), groups(true)]);
    const memberCount = Object.fromEntries(gs.map((g) => [g.id, g.member_count]));
    host.innerHTML = html`<div class="panel-head"><h2>Assignments</h2>${can("manage") ? html`<button class="btn btn-sm" id="as-add">Assign to group</button>` : ""}</div>
      ${as.items.length ? html`<div class="table-wrap"><table class="table"><thead><tr><th>Group</th><th>Intent</th><th class="num">Devices</th><th></th></tr></thead><tbody>
        ${as.items.map((a) => html`<tr><td><a href="#/groups/${a.group_id}">${a.group_name}</a></td><td>${intentLabel(a.intent, itemType)}</td><td class="num">${memberCount[a.group_id] ?? "—"}</td>
          <td class="right">${can("manage") ? html`<button class="link-btn link-danger" data-del="${a.id}">Remove</button>` : ""}</td></tr>`)}
      </tbody></table></div>` : html`<p class="hint panel-pad" style="margin:0">Not assigned yet. Assign it to a group to ${itemType === "compliance" ? "evaluate devices in that group against it" : "deploy it"}.</p>`}`.s;
    $("#as-add", host)?.addEventListener("click", () => {
      modal({
        title: "Assign to group",
        body: html`<label class="field"><span>Group</span><select name="group_id">${gs.map((g) => html`<option value="${g.id}">${g.name} (${plural(g.member_count, "device")})</option>`)}</select></label>
          <label class="field"><span>Intent</span><select name="intent">${INTENTS[itemType].map((i) => html`<option value="${i}">${intentLabel(i, itemType)}</option>`)}</select></label>
          <p class="hint">“Exclude” overrides other assignments for devices in that group.</p>`,
        actions: [{ id: "c", label: "Cancel" }, { id: "ok", label: "Assign", kind: "primary", submit: true, onClick: async (ctl) => {
          const f = $("form", ctl.el);
          await api.post("/api/assignments", { item_type: itemType, item_id: Number(itemID), group_id: Number(f.group_id.value), intent: f.intent.value });
          toast("Assigned. Devices receive the change within a minute.");
          await load();
          onChange && onChange();
        } }],
      });
    });
    $$("[data-del]", host).forEach((b) => b.addEventListener("click", async () => {
      if (!(await confirmDialog("Remove this assignment?", itemType === "profile" ? "Devices that only received this profile through this group will have it removed." : "Devices in the group stop receiving it."))) return;
      try { await api.del(`/api/assignments/${b.dataset.del}`); toast("Assignment removed"); await load(); onChange && onChange(); } catch (e) { toastError(e); }
    }));
  };
  await load();
}

/** Device picker for adding devices to groups or sending app installs. */
export async function pickDevices(title, actionLabel) {
  const res = await api.get("/api/devices?status=enrolled&limit=1000&sort=name");
  return new Promise((resolve) => {
    let resolved = false;
    const ctl = modal({
      title, wide: true,
      body: html`<input type="search" placeholder="Filter by name, serial or user" id="pd-q" style="margin-bottom:10px">
        <div class="table-wrap" style="max-height:50vh"><table class="table"><thead><tr><th class="sel"></th><th>Device</th><th>Serial</th><th>User</th></tr></thead><tbody>
        ${res.items.map((d) => html`<tr data-text="${[d.device_name, d.serial_number, d.assigned_user, d.product_name].join(" ").toLowerCase()}"><td><input type="checkbox" value="${d.udid}" aria-label="Select ${d.device_name}"></td><td>${d.device_name || d.product_name}</td><td class="ident">${d.serial_number}</td><td>${d.assigned_user}</td></tr>`)}
        </tbody></table></div>`,
      actions: [{ id: "c", label: "Cancel" }, { id: "ok", label: actionLabel, kind: "primary", submit: true, onClick: (c) => {
        resolved = true;
        resolve($$("tbody input:checked", c.el).map((i) => i.value));
      } }],
    });
    $("#pd-q", ctl.el).addEventListener("input", (e) => {
      const q = e.target.value.toLowerCase();
      $$("tbody tr", ctl.el).forEach((tr) => (tr.style.display = tr.dataset.text.includes(q) ? "" : "none"));
    });
    ctl.el.addEventListener("close", () => { if (!resolved) resolve([]); });
  });
}

export { esc };
