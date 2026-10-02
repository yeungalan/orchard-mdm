// Lightweight charts: bar list, status stack, single-series line chart, orchard grid.
import { html, esc, $, ago, pct, deviceName } from "./lib.js";

const STATUS_ORDER = ["noncompliant", "grace", "unknown", "compliant"];
const STATUS_LABEL = { compliant: "Compliant", grace: "In grace period", noncompliant: "Not compliant", unknown: "Not evaluated" };

/** Horizontal bars for one series; every bar is labeled with its value. */
export function barList(items, { total, format = (v) => v.toLocaleString(), href } = {}) {
  if (!items || !items.length) return html`<p class="hint">No data yet.</p>`;
  const max = Math.max(...items.map((i) => i.count), 1);
  const sum = total || items.reduce((a, i) => a + i.count, 0);
  return html`<div class="bars">${items.map((i) => {
    const w = Math.max(0.01, i.count / max);
    const share = sum ? Math.round((i.count / sum) * 100) : 0;
    const text = i.label || i.key;
    const label = href ? html`<a href="${href(i)}">${text}</a>` : text;
    return html`<div class="bar-row" title="${i.label || i.key}: ${format(i.count)} (${share}%)">
      <span class="b-label">${label}</span>
      <span class="bar-track"><span class="bar-fill" style="width:calc((100% - 52px) * ${w.toFixed(3)})"></span><span class="bar-val">${format(i.count)}</span></span>
    </div>`;
  })}</div>`;
}

/** Part-to-whole compliance stack with a shape+label legend. */
export function statusStack(counts) {
  const total = STATUS_ORDER.reduce((a, k) => a + (counts[k] || 0), 0);
  if (!total) return html`<p class="hint">No enrolled devices yet.</p>`;
  const order = ["compliant", "grace", "noncompliant", "unknown"];
  return html`<div class="stackbar" role="img" aria-label="${order.map((k) => `${STATUS_LABEL[k]} ${counts[k] || 0}`).join(", ")}">
      ${order.filter((k) => counts[k]).map((k) => html`<span class="seg-${k}" style="flex:${counts[k]}" title="${STATUS_LABEL[k]}: ${counts[k]}"></span>`)}
    </div>
    <div class="legend">${order.map((k) => html`<span><i class="s-${k}"></i>${STATUS_LABEL[k]} <b>${(counts[k] || 0).toLocaleString()}</b><span class="muted">${Math.round(((counts[k] || 0) / total) * 100)}%</span></span>`)}</div>`;
}

function niceTicks(max, count = 4) {
  if (max <= 0) return [0, 1];
  const raw = max / count;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) || raw;
  const ticks = [];
  for (let v = 0; v <= max + step * 0.001; v += step) ticks.push(+v.toFixed(6));
  if (ticks[ticks.length - 1] < max) ticks.push(+(ticks[ticks.length - 1] + step).toFixed(6));
  return ticks;
}

/**
 * Single-series time chart with crosshair tooltip, keyboard navigation and a table view.
 * points: [{t: unixSeconds, v: number}]
 */
export function lineChart(host, points, { yMax, format = (v) => v, label = "Value", height = 190 } = {}) {
  host.classList.add("chart");
  if (!points.length) { host.innerHTML = html`<p class="hint">No samples recorded in this period.</p>`.s; return; }
  const draw = () => {
    const W = Math.max(host.clientWidth, 280), H = height;
    const m = { l: 44, r: 62, t: 12, b: 26 };
    const t0 = points[0].t, t1 = points[points.length - 1].t === t0 ? t0 + 3600 : points[points.length - 1].t;
    const vmax = yMax ?? Math.max(...points.map((p) => p.v)) * 1.1;
    const ticks = niceTicks(vmax || 1);
    const top = yMax ?? ticks[ticks.length - 1];
    const x = (t) => m.l + ((t - t0) / (t1 - t0)) * (W - m.l - m.r);
    const y = (v) => m.t + (1 - v / top) * (H - m.t - m.b);
    const path = points.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join("");
    const area = `${path}L${x(points[points.length - 1].t).toFixed(1)},${y(0)}L${x(points[0].t).toFixed(1)},${y(0)}Z`;
    const yt = (yMax ? [0, 0.25, 0.5, 0.75, 1].map((f) => f * yMax) : ticks);
    const span = t1 - t0;
    const xticks = [];
    for (let i = 0; i <= 4; i++) xticks.push(t0 + (span * i) / 4);
    const fmtX = (t) => {
      const d = new Date(t * 1000);
      return span > 2 * 86400 ? d.toLocaleDateString(undefined, { month: "short", day: "numeric" }) : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
    };
    const last = points[points.length - 1];
    host.innerHTML = html`<svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-label="${label} over time" tabindex="0">
      ${yt.map((v) => html`<line class="gridline" x1="${m.l}" x2="${W - m.r}" y1="${y(v)}" y2="${y(v)}"/><text class="tick" x="${m.l - 8}" y="${y(v) + 4}" text-anchor="end">${format(v)}</text>`)}
      <line class="axis" x1="${m.l}" x2="${W - m.r}" y1="${y(0)}" y2="${y(0)}"/>
      ${xticks.map((t, i) => html`<text class="tick" x="${x(t)}" y="${H - 6}" text-anchor="${i === 0 ? "start" : i === 4 ? "end" : "middle"}">${fmtX(t)}</text>`)}
      <path class="series-area" d="${area}"/>
      <path class="series-line" d="${path}"/>
      <circle class="end-dot" cx="${x(last.t)}" cy="${y(last.v)}" r="4.5"/>
      <text class="end-label" x="${x(last.t) + 9}" y="${y(last.v) + 4}">${format(last.v)}</text>
      <g class="hover" style="display:none"><line class="crosshair" y1="${m.t}" y2="${y(0)}"/><circle class="hover-dot" r="5"/></g>
      <rect x="${m.l}" y="${m.t}" width="${W - m.l - m.r}" height="${H - m.t - m.b}" fill="transparent" class="hit"/>
    </svg><div class="tooltip" style="display:none"></div>`.s;
    const svg = $("svg", host), g = $(".hover", host), tip = $(".tooltip", host);
    let idx = points.length - 1;
    const show = (i) => {
      idx = Math.max(0, Math.min(points.length - 1, i));
      const p = points[idx], px = x(p.t), py = y(p.v);
      g.style.display = "";
      $(".crosshair", g).setAttribute("x1", px); $(".crosshair", g).setAttribute("x2", px);
      $(".hover-dot", g).setAttribute("cx", px); $(".hover-dot", g).setAttribute("cy", py);
      tip.style.display = "";
      tip.replaceChildren();
      const strong = document.createElement("strong");
      const key = document.createElement("i"); key.className = "key";
      strong.append(key, document.createTextNode(format(p.v)));
      const sub = document.createElement("span");
      sub.textContent = new Date(p.t * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
      tip.append(strong, sub);
      const left = Math.min(Math.max(px - tip.offsetWidth / 2, 0), W - tip.offsetWidth);
      tip.style.left = left + "px";
      tip.style.top = Math.max(py - tip.offsetHeight - 14, 0) + "px";
    };
    const hide = () => { g.style.display = "none"; tip.style.display = "none"; };
    const nearest = (clientX) => {
      const r = svg.getBoundingClientRect();
      const t = t0 + ((clientX - r.left - m.l) / (W - m.l - m.r)) * (t1 - t0);
      let best = 0;
      for (let i = 1; i < points.length; i++) if (Math.abs(points[i].t - t) < Math.abs(points[best].t - t)) best = i;
      return best;
    };
    svg.addEventListener("pointermove", (e) => show(nearest(e.clientX)));
    svg.addEventListener("pointerleave", hide);
    svg.addEventListener("focus", () => show(idx));
    svg.addEventListener("blur", hide);
    svg.addEventListener("keydown", (e) => {
      if (e.key === "ArrowLeft") { show(idx - 1); e.preventDefault(); }
      if (e.key === "ArrowRight") { show(idx + 1); e.preventDefault(); }
    });
  };
  draw();
  const ro = new ResizeObserver(() => draw());
  ro.observe(host);
  const rows = points.slice(-60).reverse();
  const table = document.createElement("details");
  table.className = "tableview";
  table.innerHTML = html`<summary>Show ${label.toLowerCase()} as a table</summary><div class="table-wrap"><table class="table"><thead><tr><th>Time</th><th class="num">${label}</th></tr></thead>
    <tbody>${rows.map((p) => html`<tr><td>${new Date(p.t * 1000).toLocaleString()}</td><td class="num">${format(p.v)}</td></tr>`)}</tbody></table></div>`.s;
  host.after(table);
}

/** The orchard: one plot per device, shaped and colored by health. */
export function orchardGrid(host, devices, { onPick } = {}) {
  const sorted = [...devices].sort((a, b) => STATUS_ORDER.indexOf(a.compliance) - STATUS_ORDER.indexOf(b.compliance) || deviceName(a).localeCompare(deviceName(b)));
  const n = sorted.length;
  const size = n <= 60 ? 18 : n <= 300 ? 14 : n <= 1200 ? 10 : 7;
  const gap = Math.round(size * 0.55);
  const counts = {};
  for (const d of devices) counts[d.compliance || "unknown"] = (counts[d.compliance || "unknown"] || 0) + 1;
  const render = () => {
    const width = Math.max(host.clientWidth - 40, 200);
    const cols = Math.max(4, Math.floor((width - (size + gap) / 2) / (size + gap)));
    const rows = [];
    for (let i = 0; i < Math.max(n, 1); i += cols) rows.push(sorted.slice(i, i + cols));
    host.innerHTML = html`<div class="panel-head"><h2>Your orchard</h2><span class="hint">Every enrolled device, worst health first</span></div>
      <div class="orchard">
        <div class="orchard-field" style="--og-size:${size}px;--og-gap:${gap}px">
          ${n ? rows.map((r) => html`<div class="orchard-row">${r.map((d) => html`<button class="plot s-${d.compliance || "unknown"}${d.lost_mode ? " lost" : ""}" data-udid="${d.udid}" aria-label="${deviceName(d)}: ${STATUS_LABEL[d.compliance] || "Not evaluated"}"></button>`)}</div>`)
            : html`<p class="hint">Enrolled devices will appear here, one plot per device.</p>`}
        </div>
        <div class="legend">${["compliant", "grace", "noncompliant", "unknown"].map((k) => html`<span><i class="s-${k}"></i>${STATUS_LABEL[k]} <b>${(counts[k] || 0).toLocaleString()}</b></span>`)}
          ${devices.some((d) => d.lost_mode) ? html`<span><i style="background:transparent;border:2px solid var(--ink)"></i>Ring: in Lost Mode</span>` : ""}</div>
      </div><div class="tooltip" style="display:none"></div>`.s;
  };
  render();
  host.style.position = "relative";
  const byId = Object.fromEntries(devices.map((d) => [d.udid, d]));
  const tip = () => $(".tooltip", host);
  const show = (btn) => {
    const d = byId[btn.dataset.udid];
    if (!d) return;
    const t = tip();
    t.replaceChildren();
    const s = document.createElement("strong"); s.textContent = deviceName(d);
    const l1 = document.createElement("span"); l1.textContent = `${STATUS_LABEL[d.compliance] || "Not evaluated"}, battery ${pct(d.battery_level)}`;
    const l2 = document.createElement("span"); l2.style.display = "block"; l2.textContent = `${d.product_name || "Unknown model"}, iOS ${d.os_version || "?"}, seen ${ago(d.last_seen)}`;
    t.append(s, l1, l2);
    t.style.display = "";
    const hr = host.getBoundingClientRect(), br = btn.getBoundingClientRect();
    t.style.left = Math.min(Math.max(br.left - hr.left - t.offsetWidth / 2, 0), hr.width - t.offsetWidth) + "px";
    t.style.top = br.top - hr.top - t.offsetHeight - 10 + "px";
  };
  host.addEventListener("pointerover", (e) => { const b = e.target.closest(".plot"); if (b) show(b); });
  host.addEventListener("focusin", (e) => { const b = e.target.closest(".plot"); if (b) show(b); });
  host.addEventListener("pointerout", (e) => { if (e.target.closest(".plot")) tip().style.display = "none"; });
  host.addEventListener("focusout", () => { tip().style.display = "none"; });
  host.addEventListener("click", (e) => { const b = e.target.closest(".plot"); if (b && onPick) onPick(b.dataset.udid); });
  let lastW = host.clientWidth;
  new ResizeObserver(() => { if (Math.abs(host.clientWidth - lastW) > 30) { lastW = host.clientWidth; render(); } }).observe(host);
}
