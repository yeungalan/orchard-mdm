// Orchard MDM console bootstrap: authentication, layout and routing.
import { html, $, $$, api, session, can, toast, toastError, ApiError } from "./lib.js";

const NAV = [
  { title: "Fleet", items: [
    ["/", "Overview", "M3 10.5 10 4l7 6.5V17H3z"],
    ["/devices", "Devices", "M6 2.5h8a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-13a1 1 0 0 1 1-1zM9 15h2"],
    ["/groups", "Groups", "M3 5h6v6H3zM11 9h6v6h-6zM3 13h6v4H3zM11 3h6v4h-6z"],
    ["/commands", "Command queue", "M4 5h12M4 10h12M4 15h7"],
  ] },
  { title: "Configure", items: [
    ["/profiles", "Configuration profiles", "M5 2.5h7l3 3v12H5zM12 2.5v3h3M8 10h5M8 13h5"],
    ["/apps", "Apps", "M3 3h6v6H3zM11 3h6v6h-6zM3 11h6v6H3zM11 11h6v6h-6z"],
    ["/declarations", "Declarations", "M4 4h12v12H4zM7 8h6M7 12h4"],
    ["/compliance", "Compliance", "M10 2.5 16 5v5c0 4-2.7 6.5-6 7.5C6.7 16.5 4 14 4 10V5zM7.5 10l2 2 3.5-4"],
  ] },
  { title: "Enroll", items: [
    ["/enrollment", "Enrollment", "M10 3v9M6.5 8.5 10 12l3.5-3.5M4 15h12"],
    ["/ade", "Automated enrollment", "M4 6h12v9H4zM7 6V4h6v2M8 10.5h4"],
  ] },
  { title: "Administer", items: [
    ["/activity", "Activity", "M3 10h3l2-5 4 10 2-5h3"],
    ["/settings", "Settings", "M10 7a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM10 2v2M10 16v2M2 10h2M16 10h2M4.3 4.3l1.4 1.4M14.3 14.3l1.4 1.4M4.3 15.7l1.4-1.4M14.3 5.7l1.4-1.4"],
  ] },
];

const ROUTES = [
  ["/", "dashboard"], ["/devices", "devices"], ["/devices/:udid", "device"], ["/devices/:udid/:tab", "device"],
  ["/groups", "groups"], ["/groups/:id", "group"], ["/commands", "commands"],
  ["/profiles", "profiles"], ["/profiles/new", "profile"], ["/profiles/:id", "profile"],
  ["/apps", "apps"], ["/apps/:id", "app"], ["/declarations", "declarations"], ["/declarations/:id", "declaration"],
  ["/compliance", "compliance"], ["/compliance/:id", "policy"],
  ["/enrollment", "enrollment"], ["/ade", "ade"], ["/activity", "activity"], ["/settings", "settings"], ["/settings/:tab", "settings"],
];

const MODULES = {
  dashboard: "dashboard", devices: "devices", device: "device", groups: "groups", group: "groups", commands: "commands",
  profiles: "profiles", profile: "profiles", apps: "apps", app: "apps", declarations: "declarations", declaration: "declarations",
  compliance: "compliance", policy: "compliance", enrollment: "enrollment", ade: "ade", activity: "activity", settings: "settings",
};

function match(path) {
  const segs = path.split("/").filter(Boolean);
  for (const [pattern, name] of ROUTES) {
    const ps = pattern.split("/").filter(Boolean);
    if (ps.length !== segs.length) continue;
    const params = {};
    let ok = true;
    ps.forEach((p, i) => {
      if (p.startsWith(":")) params[p.slice(1)] = decodeURIComponent(segs[i]);
      else if (p !== segs[i]) ok = false;
    });
    if (ok) return { name, params };
  }
  return null;
}

export function navigate(path) {
  if (location.hash !== "#" + path) location.hash = "#" + path;
  else route();
}

function currentPath() {
  const h = location.hash.replace(/^#/, "") || "/";
  const [path, q] = h.split("?");
  return { path, query: new URLSearchParams(q || "") };
}

let cleanup = null;
let seq = 0;

async function route() {
  if (!session.me) return;
  const { path, query } = currentPath();
  const m = match(path);
  const main = $("#main");
  for (const a of $$(".nav a")) {
    const href = a.getAttribute("href").slice(1);
    a.toggleAttribute("aria-current", href === "/" ? path === "/" : path === href || path.startsWith(href + "/"));
    if (a.hasAttribute("aria-current")) a.setAttribute("aria-current", "page");
  }
  $(".side")?.classList.remove("open");
  // dialogs belong to the page that opened them
  for (const d of $$("dialog[open]")) d.close();
  if (typeof cleanup === "function") { try { cleanup(); } catch {} }
  cleanup = null;
  if (!m) {
    main.innerHTML = html`<div class="empty"><h3>Page not found</h3><p>There is nothing at ${path}.</p><a class="btn" href="#/">Go to the overview</a></div>`.s;
    return;
  }
  const my = ++seq;
  try {
    const mod = await import(`./pages/${MODULES[m.name]}.js`);
    if (my !== seq) return;
    main.innerHTML = "";
    main.scrollTop = 0;
    window.scrollTo(0, 0);
    cleanup = await mod.render({ root: main, name: m.name, params: m.params, query, navigate, refresh: route });
  } catch (e) {
    if (my !== seq) return;
    if (e instanceof ApiError && e.status === 404) {
      main.innerHTML = html`<div class="empty"><h3>Not found</h3><p>This item no longer exists.</p><a class="btn" href="#/">Go to the overview</a></div>`.s;
    } else {
      console.error(e);
      main.innerHTML = html`<div class="callout bad"><p><strong>This page couldn't load.</strong></p><p>${e.message}</p></div>`.s;
    }
  }
}

const THEMES = ["system", "light", "dark"];
function applyTheme(t) {
  if (t === "system") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", t);
}
function storedTheme() { try { return localStorage.getItem("orchard-theme") || "system"; } catch { return "system"; } }

function shell() {
  const me = session.me;
  const name = (me.user && (me.user.display_name || me.user.username)) || me.principal.name;
  document.getElementById("app").innerHTML = html`
  <div class="shell">
    <aside class="side">
      <a class="brand" href="#/"><svg viewBox="0 0 32 32" aria-hidden="true"><use href="/static/icon.svg#mark"/></svg><span><b>Orchard</b><small>${session.org}</small></span></a>
      <nav class="nav" aria-label="Main">
        ${NAV.map((g) => html`<h2>${g.title}</h2>${g.items.map(([href, label, d]) => html`<a href="#${href}"><svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round" aria-hidden="true"><path d="${d}"/></svg>${label}</a>`)}`)}
      </nav>
      <div class="side-foot">
        <div class="who">${name}</div>
        <div class="role">${me.principal.role}</div>
        <div class="row" style="gap:14px">
          <button type="button" id="theme-btn">Theme: ${storedTheme()}</button>
          <button type="button" id="logout-btn">Sign out</button>
        </div>
      </div>
    </aside>
    <div>
      <button class="btn menu-btn" id="menu-btn" aria-label="Open navigation">Menu</button>
      <main class="main" id="main" tabindex="-1"></main>
    </div>
  </div>`.s;
  $("#logout-btn").addEventListener("click", async () => {
    try { await api.post("/api/auth/logout"); } catch {}
    session.me = null; session.csrf = "";
    boot();
  });
  $("#theme-btn").addEventListener("click", (e) => {
    const next = THEMES[(THEMES.indexOf(storedTheme()) + 1) % THEMES.length];
    try { localStorage.setItem("orchard-theme", next); } catch {}
    applyTheme(next);
    e.target.textContent = "Theme: " + next;
  });
  $("#menu-btn").addEventListener("click", (e) => { e.stopPropagation(); $(".side").classList.toggle("open"); });
  // tapping outside the open mobile menu closes it
  document.addEventListener("click", (e) => { const side = $(".side"); if (side?.classList.contains("open") && !side.contains(e.target)) side.classList.remove("open"); });
  route();
}

function authPage(title, sub, body) {
  document.getElementById("app").innerHTML = html`<div class="login"><div class="login-card">
    <div class="brand"><svg viewBox="0 0 32 32" aria-hidden="true" width="40" height="40"><use href="/static/icon.svg#mark"/></svg><span><b style="font-size:22px">Orchard MDM</b>${session.org && session.org !== "Orchard MDM" ? html`<small>${session.org}</small>` : ""}</span></div>
    <div class="panel panel-pad"><h1 style="font-size:21px;margin:0 0 6px">${title}</h1><p class="hint" style="margin:0 0 16px">${sub}</p>${body}</div>
  </div></div>`.s;
}

function loginPage() {
  authPage("Sign in", "Sign in to manage your iPhone and iPad fleet.", html`<form id="login-form">
    <label class="field"><span>Username</span><input type="text" name="username" autocomplete="username" required autofocus></label>
    <label class="field"><span>Password</span><input type="password" name="password" autocomplete="current-password" required></label>
    <button class="btn btn-primary" style="width:100%" type="submit">Sign in</button></form>`);
  $("#login-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    try {
      const res = await api.post("/api/auth/login", { username: f.username.value, password: f.password.value }, { allow401: true });
      session.csrf = res.csrf;
      await loadMe();
      shell();
    } catch (err) { toastError(err); }
  });
}

function setupPage() {
  authPage("Create the administrator account", "This is a new Orchard MDM server. Enter the setup code printed in the server log, then choose the first administrator's sign-in details.", html`<form id="setup-form">
    <label class="field"><span>Setup code</span><input type="text" name="setup_code" class="code" required autocomplete="off" autofocus></label>
    <label class="field"><span>Organization name</span><input type="text" name="org_name" placeholder="Acme Inc."></label>
    <label class="field"><span>Username</span><input type="text" name="username" required autocomplete="username"></label>
    <label class="field"><span>Password</span><input type="password" name="password" required minlength="10" autocomplete="new-password"><small class="hint">At least 10 characters.</small></label>
    <button class="btn btn-primary" style="width:100%" type="submit">Create account</button></form>`);
  $("#setup-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target;
    try {
      const res = await api.post("/api/auth/setup", { setup_code: f.setup_code.value, org_name: f.org_name.value, username: f.username.value, password: f.password.value });
      session.csrf = res.csrf;
      if (f.org_name.value) session.org = f.org_name.value;
      await loadMe();
      shell();
      toast("Welcome to Orchard. Start with Settings → Apple Push to connect your push certificate.");
      navigate("/settings/push");
    } catch (err) { toastError(err); }
  });
}

async function loadMe() {
  const me = await api.get("/api/auth/me", { allow401: true });
  session.me = me;
  session.perms = me.permissions || {};
  if (me.csrf) session.csrf = me.csrf;
}

async function boot() {
  applyTheme(storedTheme());
  try {
    const st = await api.get("/api/auth/status");
    session.org = st.org_name;
    document.title = `Orchard MDM – ${st.org_name}`;
    if (st.setup_required) return setupPage();
    try { await loadMe(); } catch (e) { if (e.status === 401) return loginPage(); throw e; }
    shell();
  } catch (e) {
    document.getElementById("app").innerHTML = html`<div class="login"><div class="callout bad"><p><strong>The server can't be reached.</strong></p><p>${e.message}</p></div></div>`.s;
  }
}

window.addEventListener("hashchange", route);
window.addEventListener("orchard:unauthorized", () => { if (session.me) { session.me = null; loginPage(); } });
export { can };
boot();
