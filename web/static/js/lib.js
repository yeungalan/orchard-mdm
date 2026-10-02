// Shared helpers: safe HTML templating, API client, formatting, dialogs.

export class Safe {
  constructor(s) { this.s = s; }
  toString() { return this.s; }
}
export const raw = (s) => new Safe(String(s));

const ESC = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;", "`": "&#96;" };
export function esc(v) {
  if (v === null || v === undefined || v === false) return "";
  if (v instanceof Safe) return v.s;
  if (Array.isArray(v)) return v.map(esc).join("");
  return String(v).replace(/[&<>"'`]/g, (c) => ESC[c]);
}

/** html`...` escapes every interpolation unless it is Safe (e.g. a nested html``). */
export function html(strings, ...vals) {
  let out = strings[0];
  for (let i = 0; i < vals.length; i++) out += esc(vals[i]) + strings[i + 1];
  return new Safe(out);
}

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

// ---------- session ----------
export const session = { csrf: "", me: null, perms: {} };
export const can = (p) => !!session.perms[p];

// ---------- API ----------
export class ApiError extends Error {
  constructor(status, message, body) { super(message); this.status = status; this.body = body; }
}

async function request(method, path, body, opts = {}) {
  const headers = { Accept: "application/json" };
  let payload;
  if (body instanceof FormData) payload = body;
  else if (body !== undefined) { headers["Content-Type"] = "application/json"; payload = JSON.stringify(body); }
  if (method !== "GET" && session.csrf) headers["X-CSRF-Token"] = session.csrf;
  const res = await fetch(path, { method, headers, body: payload, credentials: "same-origin" });
  if (opts.blob) {
    if (!res.ok) {
      let msg = res.statusText;
      try { msg = (await res.json()).error || msg; } catch {}
      throw new ApiError(res.status, msg);
    }
    return res;
  }
  let data = null;
  const text = await res.text();
  if (text) { try { data = JSON.parse(text); } catch { data = { error: text }; } }
  if (res.status === 401 && !opts.allow401) {
    window.dispatchEvent(new CustomEvent("orchard:unauthorized"));
  }
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText, data);
  return data;
}

export const api = {
  get: (p, o) => request("GET", p, undefined, o),
  post: (p, b, o) => request("POST", p, b ?? {}, o),
  put: (p, b) => request("PUT", p, b ?? {}),
  patch: (p, b) => request("PATCH", p, b ?? {}),
  del: (p, b) => request("DELETE", p, b),
  download: async (method, p, body, fallbackName) => {
    const res = await request(method, p, body, { blob: true });
    const blob = await res.blob();
    const cd = res.headers.get("Content-Disposition") || "";
    const m = cd.match(/filename="([^"]+)"/);
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = m ? m[1] : fallbackName || "download";
    document.body.appendChild(a);
    a.click();
    setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
  },
};

export function qsParams(obj) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(obj)) if (v !== undefined && v !== null && v !== "") p.set(k, v);
  const s = p.toString();
  return s ? "?" + s : "";
}

// ---------- formatting ----------
export function ago(ts) {
  if (!ts) return "never";
  const s = Math.floor(Date.now() / 1000 - ts);
  if (s < 45) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  if (s < 86400 * 45) return `${Math.round(s / 86400)} days ago`;
  return fmtDate(ts);
}
export function fmtDate(ts, withTime = false) {
  if (!ts) return "—";
  const d = typeof ts === "number" ? new Date(ts * 1000) : new Date(ts);
  if (isNaN(d)) return "—";
  return withTime ? d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }) : d.toLocaleDateString(undefined, { dateStyle: "medium" });
}
export const fmtTime = (ts) => fmtDate(ts, true);
export function pct(f) { return f === undefined || f === null || f < 0 ? "—" : `${Math.round(f * 100)}%`; }
export function gb(f) { return f ? `${f >= 100 ? Math.round(f) : f.toFixed(1)} GB` : "—"; }
export function bytes(n) {
  if (!n) return "—";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n >= 10 || i === 0 ? 0 : 1)} ${u[i]}`;
}
export function compact(n) {
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M";
  if (n >= 1e4) return Math.round(n / 1e3) + "K";
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace(/\.0$/, "") + "K";
  return String(n);
}
export const plural = (n, one, many) => `${n.toLocaleString()} ${n === 1 ? one : many || one + "s"}`;

const COMPLIANCE = {
  compliant: ["good", "Compliant"], grace: ["warn", "In grace period"], noncompliant: ["bad", "Not compliant"], unknown: ["unknown", "Not evaluated"],
};
export function complianceChip(state) {
  const [cls, label] = COMPLIANCE[state] || COMPLIANCE.unknown;
  return html`<span class="chip chip-${cls}">${label}</span>`;
}
const ENROLL = { enrolled: ["good", "Enrolled"], pending: ["warn", "Enrolling"], unenrolled: ["unknown", "Unenrolled"] };
export function enrollChip(state) {
  const [cls, label] = ENROLL[state] || ["unknown", state];
  return html`<span class="chip chip-${cls}">${label}</span>`;
}
const CMD = {
  Queued: ["info", "Queued"], Sent: ["info", "Sent"], NotNow: ["warn", "Deferred by device"], Acknowledged: ["good", "Done"],
  Error: ["bad", "Failed"], CommandFormatError: ["bad", "Rejected"], Canceled: ["unknown", "Canceled"], Expired: ["unknown", "Expired"],
};
export function commandChip(status) {
  const [cls, label] = CMD[status] || ["unknown", status];
  return html`<span class="chip chip-${cls}">${label}</span>`;
}
const STATE = {
  installed: ["good", "Installed"], pending: ["info", "Queued"], installing: ["info", "Installing"], failed: ["bad", "Failed"],
  missing: ["warn", "Missing on device"], removing: ["info", "Removing"], removed: ["unknown", "Removed"], remove_failed: ["bad", "Removal failed"],
  skipped: ["unknown", "Not applicable"],
};
export function stateChip(status) {
  if (!status || status === "—") return html`<span class="muted">—</span>`;
  const [cls, label] = STATE[status] || ["unknown", status];
  return html`<span class="chip chip-${cls}">${label}</span>`;
}
// Marketing names for common model identifiers (unknown identifiers are shown as-is).
const MODELS = {
  "iPhone13,1": "iPhone 12 mini", "iPhone13,2": "iPhone 12", "iPhone13,3": "iPhone 12 Pro", "iPhone13,4": "iPhone 12 Pro Max",
  "iPhone14,4": "iPhone 13 mini", "iPhone14,5": "iPhone 13", "iPhone14,2": "iPhone 13 Pro", "iPhone14,3": "iPhone 13 Pro Max", "iPhone14,6": "iPhone SE (3rd generation)",
  "iPhone14,7": "iPhone 14", "iPhone14,8": "iPhone 14 Plus", "iPhone15,2": "iPhone 14 Pro", "iPhone15,3": "iPhone 14 Pro Max",
  "iPhone15,4": "iPhone 15", "iPhone15,5": "iPhone 15 Plus", "iPhone16,1": "iPhone 15 Pro", "iPhone16,2": "iPhone 15 Pro Max",
  "iPhone17,1": "iPhone 16 Pro", "iPhone17,2": "iPhone 16 Pro Max", "iPhone17,3": "iPhone 16", "iPhone17,4": "iPhone 16 Plus", "iPhone17,5": "iPhone 16e",
  "iPad13,18": "iPad (10th generation)", "iPad13,19": "iPad (10th generation)", "iPad14,1": "iPad mini (6th generation)", "iPad14,2": "iPad mini (6th generation)",
  "iPad13,16": "iPad Air (5th generation)", "iPad13,17": "iPad Air (5th generation)", "iPad14,8": "iPad Air 11-inch (M2)", "iPad14,9": "iPad Air 11-inch (M2)",
  "iPad14,10": "iPad Air 13-inch (M2)", "iPad14,11": "iPad Air 13-inch (M2)", "iPad16,1": "iPad mini (A17 Pro)", "iPad16,2": "iPad mini (A17 Pro)",
  "iPad16,3": "iPad Pro 11-inch (M4)", "iPad16,4": "iPad Pro 11-inch (M4)", "iPad16,5": "iPad Pro 13-inch (M4)", "iPad16,6": "iPad Pro 13-inch (M4)",
};
export const modelLabel = (id) => (id && MODELS[id]) || id || "";
export const deviceName = (d) => d.device_name || d.product_name || d.serial_number || d.udid;
export const ownershipLabel = (o) => ({ corporate: "Corporate", personal: "Personal", unknown: "Unknown" })[o] || o;
export const enrollTypeLabel = (t) => ({ ade: "Automated (ADE)", token: "Enrollment link", manual: "Manual profile", byod: "User Enrollment (BYOD)", adde: "Work account sign-in" })[t] || t;

// ---------- toasts ----------
export function toast(msg, kind = "ok") {
  const host = document.getElementById("toasts");
  const el = document.createElement("div");
  el.className = "toast" + (kind === "err" ? " err" : "");
  el.setAttribute("role", kind === "err" ? "alert" : "status");
  el.textContent = msg;
  host.appendChild(el);
  setTimeout(() => el.remove(), kind === "err" ? 7000 : 3500);
}
export const toastError = (e) => toast(e && e.message ? e.message : String(e), "err");

// ---------- dialogs ----------
export function modal({ title, body, actions = [], wide = false, onOpen }) {
  const dlg = document.createElement("dialog");
  if (wide) dlg.classList.add("wide");
  dlg.innerHTML = html`<form method="dialog" class="dlg-form" novalidate>
      <div class="dlg-head"><h2>${title}</h2><button class="dlg-x" value="cancel" formnovalidate aria-label="Close" title="Close">${icons.close}</button></div>
      <div class="dlg-body">${body}</div>
      ${actions.length ? html`<div class="dlg-foot">${actions.map((a) => html`<button type="${a.submit ? "submit" : "button"}" class="btn ${a.kind ? "btn-" + a.kind : ""}" data-act="${a.id}">${a.label}</button>`)}</div>` : ""}
    </form>`.s;
  document.body.appendChild(dlg);
  const ctl = {
    el: dlg,
    close: () => { dlg.close(); },
    busy: (on) => $$(".dlg-foot button", dlg).forEach((b) => (b.disabled = on)),
  };
  dlg.addEventListener("close", () => setTimeout(() => dlg.remove(), 50));
  for (const a of actions) {
    const btn = dlg.querySelector(`[data-act="${a.id}"]`);
    btn.addEventListener("click", async (ev) => {
      ev.preventDefault();
      if (!a.onClick) return ctl.close();
      ctl.busy(true);
      try {
        const keep = await a.onClick(ctl);
        if (keep !== false) ctl.close();
      } catch (e) { toastError(e); } finally { ctl.busy(false); }
    });
  }
  dlg.querySelector("form").addEventListener("submit", (ev) => {
    const submitAct = actions.find((a) => a.submit);
    if (ev.submitter && ev.submitter.value === "cancel") return;
    if (submitAct) { ev.preventDefault(); dlg.querySelector(`[data-act="${submitAct.id}"]`).click(); }
  });
  dlg.showModal();
  if (onOpen) onOpen(ctl);
  return ctl;
}

export function confirmDialog(title, message, { confirmLabel = "Confirm", danger = false } = {}) {
  return new Promise((resolve) => {
    let done = false;
    const ctl = modal({
      title, body: html`<p>${message}</p>`,
      actions: [
        { id: "cancel", label: "Cancel", onClick: () => { done = true; resolve(false); } },
        { id: "ok", label: confirmLabel, kind: danger ? "danger" : "primary", submit: true, onClick: () => { done = true; resolve(true); } },
      ],
    });
    ctl.el.addEventListener("close", () => { if (!done) resolve(false); });
  });
}

export function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).split(",")[1] || "");
    r.onerror = () => reject(r.error);
    r.readAsDataURL(file);
  });
}

export function formData(form) {
  const out = {};
  for (const el of form.elements) {
    if (!el.name || el.disabled) continue;
    if (el.type === "checkbox") out[el.name] = el.checked;
    else if (el.type === "number") out[el.name] = el.value === "" ? null : Number(el.value);
    else if (el.type !== "file" && el.type !== "submit" && el.type !== "button") out[el.name] = el.value;
  }
  return out;
}

export function debounce(fn, ms = 250) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

export const icons = {
  search: raw('<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="9" cy="9" r="6"/><path d="M13.5 13.5 18 18"/></svg>'),
  close: raw('<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M5 5l10 10M15 5 5 15"/></svg>'),
};

export function emptyState(title, text, action) {
  return html`<div class="empty"><h3>${title}</h3><p>${text}</p>${action || ""}</div>`;
}

export function copyText(text) {
  navigator.clipboard?.writeText(text).then(() => toast("Copied to clipboard"), () => toast("Copy failed", "err"));
}
