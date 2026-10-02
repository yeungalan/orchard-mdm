import { html, $, api, can, ago, fmtTime, emptyState, debounce } from "../lib.js";
import { groups, intentLabel } from "../components.js";

const PAGE = 100;

// "device.secrets_viewed" -> "Device secrets viewed"
const ACTIONS = { "auth.login": "Signed in", "auth.login_failed": "Sign-in failed", "auth.setup": "First-run setup" };
const WORDS = { apikey: "API key", apns: "APNs", ade: "ADE", vpp: "Apps and Books", pki: "PKI", ddm: "DDM", oidc: "OIDC" };
const actionLabel = (a) => {
  if (ACTIONS[a]) return ACTIONS[a];
  const s = a.split(/[._]/).map((w) => WORDS[w] || w).join(" ");
  return s.charAt(0).toUpperCase() + s.slice(1);
};
const keyLabel = (k) => { const s = k.replace(/_/g, " ").replace(/\bid\b/i, "ID").replace(/\burl\b/i, "URL").replace(/\bitunes\b/i, "App Store"); return s.charAt(0).toUpperCase() + s.slice(1); };

function details(raw, groupName, action) {
  if (!raw) return "";
  let obj;
  try { obj = JSON.parse(raw); } catch { return html`<span class="hint">${raw}</span>`; }
  if (!obj || typeof obj !== "object") return html`<span class="hint">${raw}</span>`;
  const type = action.startsWith("assignment.") ? "" : action.split(".")[0];
  const parts = Object.entries(obj).filter(([, v]) => v !== "" && v !== null && !(Array.isArray(v) && !v.length)).map(([k, v]) => {
    let label = keyLabel(k), val = v;
    if (k === "group_id") { label = "Group"; val = groupName[v] || `#${v}`; }
    else if (k === "intent") val = intentLabel(v, type || "app");
    else if (Array.isArray(v)) val = v.map((x) => String(x).replace("com.apple.", "")).join(", ");
    else if (typeof v === "boolean") val = v ? "yes" : "no";
    else if (k === "expires" && typeof v === "string") val = new Date(v).toLocaleDateString();
    else if (typeof v === "string") val = v.replace(/^com\.apple\./, "");
    return html`<span class="detail"><span class="muted">${label}</span> ${String(val)}</span>`;
  });
  return html`<div class="details">${parts}</div>`;
}

export async function render({ root, query }) {
  const tab = query.get("tab") || "events";
  root.innerHTML = html`<div class="page-head"><div><h1>Activity</h1><p class="sub">What devices did, and what administrators changed.</p></div></div>
    <nav class="tabs"><a href="#/activity?tab=events" aria-selected="${tab === "events"}">Device activity</a>${can("admin") ? html`<a href="#/activity?tab=audit" aria-selected="${tab === "audit"}">Audit log</a>` : ""}</nav>
    <div id="body"></div>`.s;
  const body = $("#body", root);
  if (tab === "audit" && can("admin")) return audit(body);
  return events(body);
}

async function audit(body) {
  body.innerHTML = html`<div class="toolbar"><input type="search" id="q" placeholder="Filter by person, action or target" style="max-width:360px"></div><section class="panel" id="list"></section>`.s;
  const gs = await groups().catch(() => []);
  const groupName = Object.fromEntries(gs.map((g) => [g.id, g.name]));
  let items = [], total = 0, seq = 0;
  const row = (a) => html`<tr><td class="nowrap" title="${fmtTime(a.ts)}">${ago(a.ts)}</td><td>${a.actor}</td><td>${actionLabel(a.action)}</td>
    <td>${a.target_href ? html`<a href="${a.target_href}">${a.target_label || a.target}</a>` : a.target_label || a.target || html`<span class="muted">—</span>`}</td>
    <td>${details(a.details, groupName, a.action)}</td><td class="ident">${a.ip}</td></tr>`;
  const draw = () => {
    $("#list", body).innerHTML = (items.length ? html`<div class="table-wrap"><table class="table audit"><thead><tr><th>When</th><th>Who</th><th>Action</th><th>Target</th><th>Details</th><th>IP address</th></tr></thead><tbody>
      ${items.map(row)}</tbody></table></div>${pager(items.length, total, "entries")}`
      : emptyState("No entries", "Nothing matches.")).s;
  };
  const load = async (more) => {
    const my = ++seq;
    const res = await api.get(`/api/audit?limit=${PAGE}&offset=${more ? items.length : 0}&q=${encodeURIComponent($("#q", body).value)}`);
    if (my !== seq) return;
    items = more ? items.concat(res.items) : res.items;
    total = res.total;
    draw();
  };
  $("#q", body).addEventListener("input", debounce(() => load(false), 300));
  body.addEventListener("click", (e) => { if (e.target.closest("[data-more]")) load(true); });
  return load(false);
}

async function events(body) {
  let items = [], done = false;
  const draw = () => {
    body.innerHTML = html`<section class="panel">${items.length ? html`<ul class="timeline panel-pad">${items.map((e) => html`<li><time title="${fmtTime(e.ts)}">${ago(e.ts)}</time>
      <div class="${e.level === "warn" ? "lvl-warn" : ""}">${e.device_id ? html`<a href="#/devices/${encodeURIComponent(e.device_id)}">${e.device_name || "Device"}</a>: ` : ""}${e.message}</div></li>`)}</ul>
      ${pager(items.length, done ? items.length : null, "events")}`
      : html`<p class="hint panel-pad" style="margin:0">Nothing has happened yet.</p>`}</section>`.s;
  };
  const load = async (more) => {
    const res = await api.get(`/api/events?limit=${PAGE}&offset=${more ? items.length : 0}`);
    items = more ? items.concat(res.items) : res.items;
    done = res.items.length < PAGE;
    draw();
  };
  body.addEventListener("click", (e) => { if (e.target.closest("[data-more]")) load(true); });
  return load(false);
}

// total null = unknown (more may exist)
function pager(shown, total, noun) {
  const more = total === null || shown < total;
  return html`<div class="pager"><span class="hint">${total === null ? `Showing the latest ${shown} ${noun}` : `Showing ${shown} of ${total} ${noun}`}</span>
    ${more ? html`<button class="btn btn-sm" data-more>Load more</button>` : ""}</div>`;
}
