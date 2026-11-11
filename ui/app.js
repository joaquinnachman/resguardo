"use strict";

// ---------------------------------------------------------------------------
// Utilidades
// ---------------------------------------------------------------------------
const $ = (s, el = document) => el.querySelector(s);

// replaceChildren / append ignoran null, undefined y false (cómodo para armar vistas condicionales).
for (const m of ["replaceChildren", "append"]) {
  const orig = Element.prototype[m];
  Element.prototype[m] = function (...kids) {
    return orig.apply(this, kids.flat(Infinity).filter((k) => k !== null && k !== undefined && k !== false));
  };
}

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined || v === false) continue;
      if (k === "class") el.className = v;
      else if (k === "style" && typeof v === "object") Object.assign(el.style, v);
      else if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v);
      else if (k === "html") el.innerHTML = v;
      else if (k === "value") el.value = v;
      else if (k === "checked") el.checked = !!v;
      else if (v === true) el.setAttribute(k, "");
      else el.setAttribute(k, v);
    }
  }
  const add = (k) => {
    if (k === null || k === undefined || k === false) return;
    if (Array.isArray(k)) return k.forEach(add);
    el.append(k instanceof Node ? k : document.createTextNode(String(k)));
  };
  kids.forEach(add);
  return el;
}

const ICONS = {
  play: '<svg viewBox="0 0 16 16"><path d="M4 2.5v11l9-5.5z" fill="currentColor"/></svg>',
  pause: '<svg viewBox="0 0 16 16"><path d="M4 2.5h3v11H4zM9 2.5h3v11H9z" fill="currentColor"/></svg>',
  stop: '<svg viewBox="0 0 16 16"><rect x="3" y="3" width="10" height="10" rx="1" fill="currentColor"/></svg>',
  plus: '<svg viewBox="0 0 16 16"><path d="M7 2h2v5h5v2H9v5H7V9H2V7h5z" fill="currentColor"/></svg>',
  all: '<svg viewBox="0 0 16 16"><path d="M2 2.5v11l6-5.5zM8 2.5v11l6-5.5z" fill="currentColor"/></svg>',
  folder: '<svg viewBox="0 0 16 16"><path d="M1.5 3.5h5l1.5 1.5h6.5v8h-13z" fill="none" stroke="currentColor" stroke-width="1.3"/></svg>',
  file: '<svg viewBox="0 0 16 16"><path d="M3.5 1.5h6l3 3v10h-9z" fill="none" stroke="currentColor" stroke-width="1.3"/></svg>',
  drive: '<svg viewBox="0 0 16 16"><rect x="1.5" y="4.5" width="13" height="7" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.3"/><circle cx="11.5" cy="8" r="1" fill="currentColor"/></svg>',
  up: '<svg viewBox="0 0 16 16"><path d="M8 3l5 5H9v5H7V8H3z" fill="currentColor"/></svg>',
  down: '<svg viewBox="0 0 16 16"><path d="M8 13l5-5H9V3H7v5H3z" fill="currentColor"/></svg>',
  cal: '<svg viewBox="0 0 16 16"><rect x="2" y="3" width="12" height="11" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M2 6.5h12M5 1.5v3M11 1.5v3" stroke="currentColor" stroke-width="1.3"/></svg>',
};
const icon = (n) => h("span", { class: "ic", html: ICONS[n], "aria-hidden": "true" });

async function api(path, body) {
  const opt = { headers: { "X-Requested-With": "resguardo" } };
  if (body !== undefined) {
    opt.method = "POST";
    opt.headers["Content-Type"] = "application/json";
    opt.body = JSON.stringify(body);
  }
  const r = await fetch("/api/" + path, opt);
  const ct = r.headers.get("Content-Type") || "";
  const data = ct.includes("json") ? await r.json() : await r.text();
  if (!r.ok) throw new Error((data && data.error) || "Error " + r.status);
  return data;
}

function toast(msg, err, action) {
  const t = h("div", { class: "toast" + (err ? " err" : "") }, msg);
  if (action) t.append(" ", h("button", { class: "btn small", onclick: action.fn, style: { marginLeft: "8px" } }, action.label));
  $("#toasts").append(t);
  setTimeout(() => t.remove(), action ? 12000 : err ? 9000 : 4500);
}

async function attempt(fn, okMsg) {
  try {
    const r = await fn();
    if (okMsg) toast(okMsg);
    return r;
  } catch (e) {
    toast(e.message, true);
    return undefined;
  }
}

const ZERO = (d) => !d || d.startsWith("0001-");
const MESES = ["ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"];
const DIAS = ["dom", "lun", "mar", "mié", "jue", "vie", "sáb"];

function fmtDate(iso, opts = {}) {
  if (!iso || ZERO(iso)) return "—";
  const d = new Date(iso);
  const now = new Date();
  const hm = d.toLocaleTimeString("es-AR", { hour: "2-digit", minute: "2-digit", hour12: false });
  const day0 = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const diff = Math.round((new Date(d.getFullYear(), d.getMonth(), d.getDate()) - day0) / 86400000);
  if (diff === 0) return "hoy " + hm;
  if (diff === -1) return "ayer " + hm;
  if (diff === 1) return "mañana " + hm;
  let s = d.getDate() + " " + MESES[d.getMonth()];
  if (d.getFullYear() !== now.getFullYear()) s += " " + d.getFullYear();
  return opts.noTime ? s : s + " " + hm;
}

function fmtFull(iso) {
  return new Date(iso).toLocaleString("es-AR", { day: "numeric", month: "long", year: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}

function fmtBytes(b) {
  if (!b) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return (i === 0 ? b : b.toFixed(b >= 100 ? 0 : 1)).toString().replace(".", ",") + " " + u[i];
}

const TYPE_NAMES = {
  proyectos: "Proyectos de DAW", completo: "Completo", incremental: "Incremental",
  diferencial: "Diferencial", ficticio: "Ficticia (sólo eventos)", instantanea: "Versión",
};
const DIAS_LARGOS = ["domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"];

function scheduleText(t) {
  const s = t.schedule || {};
  let base;
  switch (s.kind) {
    case "diario": base = "Todos los días a las " + s.time; break;
    case "semanal": base = ((s.weekdays || []).map((d) => DIAS[d]).join(", ") || "—") + " a las " + s.time; break;
    case "mensual": base = "El día " + s.monthDay + " de cada mes a las " + s.time; break;
    case "anual": base = "Cada " + s.monthDay + "/" + s.month + " a las " + s.time; break;
    case "temporizador": base = "Cada " + s.everyMinutes + " minutos"; break;
    case "unavez": base = "Una vez, el " + s.date + " a las " + s.time; break;
    case "inicio": base = "Al iniciar Resguardo"; break;
    default: base = "Manual";
  }
  const extra = [];
  if (t.type === "proyectos") {
    if (t.projects.triggerOnSave) extra.push("al guardar");
    if (t.projects.triggerOnClose) extra.push("al cerrar el DAW");
  } else {
    if (s.onDawSave) extra.push("al guardar un proyecto");
    if (s.onDawClose) extra.push("al cerrar el DAW");
  }
  if (s.atStartup && s.kind !== "inicio") extra.push("al iniciar");
  return extra.length ? base + ", y " + extra.join(" y ") : base;
}

function locText(l) {
  if (!l) return "—";
  if (l.kind === "ftp" || l.kind === "sftp") return l.kind + "://" + (l.user ? l.user + "@" : "") + l.host + (l.path || "");
  return l.path || "—";
}

function clone(o) { return JSON.parse(JSON.stringify(o)); }

// ---------------------------------------------------------------------------
// Estado
// ---------------------------------------------------------------------------
const S = {
  data: null, view: "tareas", sel: null, log: [], logSince: 0,
  projects: {}, history: {}, open: null, snaps: null, snapDest: 0, selSnap: null,
  filter: "", wasRunning: false, optTab: "general",
};

const task = (id) => (S.data.tasks || []).find((t) => t.id === id);
const isWin = () => S.data && S.data.os === "windows";

function applyTheme() {
  const pref = S.data.settings.theme;
  let theme = pref;
  if (pref === "auto" || !pref) theme = matchMedia("(prefers-color-scheme: light)").matches ? "claro" : "oscuro";
  document.documentElement.dataset.theme = theme;
}

async function boot() {
  try {
    const who = await api("whoami");
    if (who.level === 1) return renderLogin();
    if (who.level === 0) {
      $("#app").replaceChildren(h("div", { class: "login" }, h("p", null, "Abrí Resguardo desde su ícono para usar esta ventana.")));
      return;
    }
  } catch (e) {
    $("#app").replaceChildren(h("div", { class: "login" }, h("p", null, "No se pudo conectar con Resguardo.")));
    return;
  }
  try {
    await reload();
  } catch (e) {
    $("#app").replaceChildren(h("div", { class: "login" }, h("p", null, "No se pudo leer el estado de Resguardo: " + e.message)));
    return;
  }
  setInterval(poll, 1000);
  showUpdateNews();
}

async function showUpdateNews() {
  let r;
  try { r = await api("update/news"); } catch { return; }
  const n = r && r.news;
  if (!n) return;
  const close = () => { api("update/news", {}); $("#layer").replaceChildren(); };
  const ok = n.estado === "ok";
  sheet({
    title: ok ? "Resguardo se actualizó a la versión " + n.to : "No se pudo instalar la versión " + n.to,
    size: "sm", onClose: close,
    body: ok
      ? h("div", null, (n.notes || []).length ? h("ul", { class: "notes" }, n.notes.map((x) => h("li", null, x))) : h("p", null, "Ya estás usando la versión nueva."))
      : h("div", null, h("p", null, "Resguardo volvió solo a la versión " + n.from + ", así que todo sigue funcionando."),
        h("p", { class: "muted" }, "Motivo: " + (n.error || "desconocido") + ". Esa versión no se va a reintentar sola; podés probar de nuevo desde Opciones → General → Buscar ahora.")),
    foot: [h("button", { class: "btn primary", onclick: close }, "Listo")],
  });
}

// Normaliza lo que llega del motor para que nunca falte una lista.
function normData(d) {
  d.tasks = d.tasks || [];
  d.lists = d.lists || [];
  d.daws = d.daws || [];
  d.status = d.status || {};
  d.update = d.update || {};
  d.status.queue = d.status.queue || [];
  d.status.daws = d.status.daws || [];
  d.settings = d.settings || {};
  d.settings.sampleSearchFolders = d.settings.sampleSearchFolders || [];
  d.settings.smtp = d.settings.smtp || {};
  d.settings.smtp.to = d.settings.smtp.to || [];
  d.tasks.forEach(normTask);
  return d;
}

function normTask(t) {
  for (const k of ["sources", "destinations", "include", "exclude", "preEvents", "postEvents"]) t[k] = t[k] || [];
  t.schedule = t.schedule || { kind: "manual" };
  t.schedule.weekdays = t.schedule.weekdays || [];
  t.projects = t.projects || {};
  t.projects.daws = t.projects.daws || [];
  t.projects.overrides = t.projects.overrides || {};
  t.runAs = t.runAs || {};
  t.dateFilter = t.dateFilter || {};
  t.sizeFilter = t.sizeFilter || {};
  return t;
}

async function reload() {
  S.data = normData(await api("state"));
  applyTheme();
  if (!S.sel || !task(S.sel)) S.sel = (S.data.tasks[0] || {}).id || null;
  render();
}

function renderLogin() {
  const pw = h("input", { type: "password", autofocus: true, "aria-label": "Contraseña" });
  const form = h("form", {
    onsubmit: async (e) => {
      e.preventDefault();
      try { await api("login", { password: pw.value }); boot(); } catch (err) { toast(err.message, true); }
    },
  },
  h("div", { class: "brand" }, h("img", { src: "icono.png", alt: "" }), h("b", null, "Resguardo")),
  h("p", { class: "muted" }, "Esta ventana está protegida con contraseña."),
  pw, h("button", { class: "btn primary", type: "submit" }, "Entrar"));
  $("#app").replaceChildren(h("div", { class: "login" }, form));
}

async function poll() {
  let r;
  try { r = await api("status?log=" + S.logSince); } catch {
    S.fails = (S.fails || 0) + 1;
    const updating = S.data.update && S.data.update.state === "instalando";
    if (updating || S.fails === 3) $("#statusbar") && $("#statusbar").replaceChildren(h("div", { class: "st-line" },
      h("span", { class: "led " + (updating ? "work" : "error") }),
      updating ? "Resguardo se está actualizando a la versión " + S.data.update.latest + ". Vuelve en unos segundos…"
        : "No hay conexión con Resguardo. ¿Se cerró? Abrilo de nuevo para seguir."));
    return;
  }
  S.fails = 0;
  if (r.version && r.version !== S.data.version) { location.reload(); return; }
  if (r.update) S.data.update = r.update;
  r.status.queue = r.status.queue || [];
  r.status.daws = r.status.daws || [];
  S.data.status = r.status;
  S.data.tasks = (r.tasks || []).map(normTask);
  if (r.log && r.log.length) {
    S.log.push(...r.log);
    if (S.log.length > 3000) S.log = S.log.slice(-3000);
    S.logSince = r.log[r.log.length - 1].n;
    if (S.view === "registro") renderLogLines(r.log);
  }
  if (S.lastDone !== undefined && r.status.done !== S.lastDone) onJobFinished();
  S.lastDone = r.status.done;
  renderStatus();
  renderTransport();
  if (S.view === "tareas") renderRail();
  if (S.view === "registro") renderQueue();
}

async function onJobFinished() {
  if (S.restoreWatch) {
    S.restoreWatch = false;
    const r = await api("lastrestore");
    if (r.path) toast("Restauración lista en " + r.path, false, { label: "Abrir carpeta", fn: () => api("fs/open", { path: r.path }) });
  }
  if (S.view === "tareas") {
    const t = task(S.sel);
    if (t) {
      if (t.type === "proyectos") await loadProjects(t.id, true);
      else await loadHistory(t.id, true);
      renderContent();
    }
  }
}

// ---------------------------------------------------------------------------
// Armazón
// ---------------------------------------------------------------------------
function render() {
  const app = $("#app");
  app.replaceChildren(
    h("header", { class: "topbar", id: "topbar" }),
    h("main", { class: "main" + (S.view === "tareas" ? "" : " single"), id: "main" }),
    h("footer", { class: "statusbar", id: "statusbar" }),
  );
  renderTop();
  renderMain();
  renderStatus();
}

function renderTop() {
  const nav = h("nav", { class: "nav", "aria-label": "Secciones" },
    ["tareas", "registro", "herramientas", "opciones"].map((v) =>
      h("button", { "aria-current": S.view === v ? "page" : null, onclick: () => { S.view = v; render(); } },
        { tareas: "Tareas", registro: "Registro", herramientas: "Herramientas", opciones: "Opciones" }[v])));
  const lists = S.data.lists.length ? S.data.lists : [S.data.settings.currentList];
  const listSel = h("select", {
    "aria-label": "Lista de tareas", title: "Lista de tareas",
    onchange: async (e) => {
      const v = e.target.value;
      if (v === "__nueva") {
        const name = await askText("Nueva lista de tareas", "Nombre de la lista", "");
        if (name) await attempt(() => api("lists/new", { name }));
      } else if (v === "__guardar") {
        const name = await askText("Guardar la lista con otro nombre", "Nombre", S.data.settings.currentList + " (copia)");
        if (name) await attempt(() => api("lists/saveas", { name }));
      } else {
        await attempt(() => api("lists/switch", { name: v }));
      }
      S.sel = null; S.projects = {}; S.history = {};
      await reload();
    },
  }, lists.map((n) => h("option", { value: n, selected: n === S.data.settings.currentList }, n)),
  h("option", { value: "__nueva" }, "Nueva lista…"), h("option", { value: "__guardar" }, "Guardar como…"));

  $("#topbar").replaceChildren(
    h("div", { class: "brand" }, h("img", { src: "icono.png", alt: "" }), h("b", null, "Resguardo"),
      S.data.instance ? h("span", { class: "tag" }, S.data.instance) : null),
    nav, listSel, h("div", { class: "transport", id: "transport" }));
  renderTransport();
}

function renderTransport() {
  const st = S.data.status || {};
  const el = $("#transport");
  if (!el) return;
  const kids = [];
  if (st.daws && st.daws.length) kids.push(h("span", { class: "daw-on", title: "Mientras el DAW está abierto, los respaldos van más despacio para no molestar" }, h("span", { class: "led work" }), st.daws.join(", ") + " abierto"));
  if (st.schedOff) kids.push(h("button", { class: "btn small", onclick: () => api("scheduler", { off: false }).then(poll) }, "Programador en pausa · reactivar"));
  const chip = updateChip();
  if (chip) kids.push(chip);
  kids.push(
    h("button", { class: "btn", onclick: () => attempt(() => api("run", { all: true }), "Todas las tareas activas quedaron en la cola"), title: "Ejecutar todas las tareas activas" }, icon("all"), "Ejecutar todas"),
    h("button", { class: "btn", disabled: !st.running && !(st.queue || []).length, onclick: () => api("pause", {}).then(poll), title: st.paused ? "Reanudar" : "Pausar" }, icon(st.paused ? "play" : "pause"), st.paused ? "Reanudar" : "Pausar"),
    h("button", { class: "btn", disabled: !st.running, onclick: () => api("cancel", {}).then(poll), title: "Cancelar el respaldo en curso" }, icon("stop"), "Cancelar"),
  );
  el.replaceChildren(...kids);
}

// Aviso de versión nueva en la barra superior (sólo cuando hay algo que contar).
function updateChip() {
  const u = S.data.update || {};
  const auto = S.data.settings.updateInstall;
  const v = u.latest;
  switch (u.state) {
    case "disponible":
      if (!u.canSelf) return h("button", { class: "btn small update-chip", onclick: () => openUpdateOptions() }, "Versión " + v + " disponible");
      if (auto) return null;
      return h("button", { class: "btn small update-chip", onclick: () => installNow() }, "Instalar la versión " + v);
    case "bajando":
      return h("span", { class: "daw-on" }, h("span", { class: "led work" }), "Bajando la versión " + v + (u.total ? " · " + Math.round((u.done / u.total) * 100) + "%" : ""));
    case "lista":
      return auto ? null : h("button", { class: "btn small update-chip", onclick: () => installNow() }, "Instalar la versión " + v);
    case "esperando":
      return h("button", { class: "btn small update-chip", title: "Se instala sola cuando " + u.waitingFor.replace(/^que /, "") + ". Tocá para instalarla ya.", onclick: () => installNow() }, "Versión " + v + " lista · instalar ya");
    case "instalando":
      return h("span", { class: "daw-on" }, h("span", { class: "led work" }), "Actualizando a la versión " + v + "…");
  }
  return null;
}

async function installNow() {
  const u = S.data.update || {};
  const ok = await confirmSheet("Instalar la versión " + u.latest, h("div", null,
    (u.notes || []).length ? h("ul", { class: "notes" }, u.notes.map((x) => h("li", null, x))) : null,
    h("p", null, "Resguardo se cierra y se vuelve a abrir solo en unos segundos. Tus tareas y respaldos no se tocan."),
    u.state === "esperando" && u.waitingFor.includes("respaldo") ? h("p", { class: "muted" }, "Hay un respaldo en curso: la instalación arranca apenas termine.") : null),
  "Instalar");
  if (!ok) return;
  S.data.update = await api("update/check", { install: true });
  renderTransport();
}

function openUpdateOptions() { S.view = "opciones"; S.optTab = "general"; render(); }

function renderStatus() {
  const st = S.data.status || {};
  const bar = $("#statusbar");
  if (!bar) return;
  if (st.running) {
    const p = st.progress || {};
    const pct = p.totalBytes ? Math.min(100, (p.doneBytes / p.totalBytes) * 100) : 0;
    const speed = p.speed ? " · " + fmtBytes(p.speed) + "/s" : "";
    const files = p.totalFiles ? `${p.doneFiles} de ${p.totalFiles} archivos · ${fmtBytes(p.doneBytes)} de ${fmtBytes(p.totalBytes)}` : (p.phase || "");
    bar.replaceChildren(
      h("div", { class: "grow", style: { minWidth: 0 } },
        h("div", { class: "st-line" }, h("span", { class: "led work" }),
          h("b", null, st.paused ? "En pausa: " + st.running.taskName : st.running.taskName),
          h("span", { class: "muted st-text" }, files + speed),
          h("span", { class: "faint st-text grow small" }, p.current ? "· " + p.current : (p.phase ? "· " + p.phase : ""))),
        h("div", { class: "meter" }, h("i", { style: { width: pct.toFixed(1) + "%" } }))),
      h("span", { class: "muted small" }, (st.queue || []).length ? (st.queue.length + " en cola") : ""));
  } else {
    let led = "ok", msg = "Todo al día";
    if (st.lastResult === "errores") { led = "error"; msg = "El último respaldo tuvo errores"; }
    if ((st.queue || []).length) { led = "work"; msg = st.queue.length + " en cola" + (st.paused ? " (en pausa)" : ""); }
    const next = nextScheduled();
    bar.replaceChildren(
      h("div", { class: "st-line" }, h("span", { class: "led " + led }), h("span", null, msg),
        next ? h("span", { class: "muted" }, "Próximo programado: " + next) : null),
      st.lastResult === "errores" ? h("div", { class: "row" },
        h("button", { class: "btn small", onclick: () => { S.view = "registro"; render(); } }, "Ver el registro"),
        h("button", { class: "btn small ghost", onclick: () => api("ack", {}).then(poll) }, "Marcar como visto")) : h("span"));
  }
}

function nextScheduled() {
  let best = null, name = "";
  for (const t of S.data.tasks || []) {
    if (!t.enabled || ZERO(t.nextRun)) continue;
    if (!best || t.nextRun < best) { best = t.nextRun; name = t.name; }
  }
  return best ? name + ", " + fmtDate(best) : "";
}

function renderMain() {
  const main = $("#main");
  if (S.view === "tareas") {
    main.replaceChildren(h("aside", { class: "rail", id: "rail" }), h("section", { class: "content", id: "content" }));
    renderRail();
    renderContent();
  } else if (S.view === "registro") {
    main.replaceChildren(h("section", { class: "content" }, h("div", { class: "content-narrow", id: "regview", style: { maxWidth: "1200px" } })));
    renderRegistro();
  } else if (S.view === "herramientas") {
    main.replaceChildren(h("section", { class: "content" }, h("div", { class: "content-narrow", id: "toolsview" })));
    renderTools();
  } else {
    main.replaceChildren(h("section", { class: "content" }, h("div", { id: "optview" })));
    renderOptions();
  }
}

// ---------------------------------------------------------------------------
// Lista de tareas
// ---------------------------------------------------------------------------
function taskLed(t) {
  if (t.running) return "work";
  if (!t.enabled) return "off";
  if (t.queued) return "dirty";
  if (t.last && (t.last.status === "errores" || t.last.status === "fallido")) return "error";
  if (t.last) return "ok";
  return "off";
}

let railSig = "";
function renderRail() {
  const rail = $("#rail");
  if (!rail) return;
  const tasks = S.data.tasks || [];
  const sig = JSON.stringify(tasks.map((t) => [t.id, t.name, t.enabled, t.group, taskLed(t), t.nextRun, t.type])) + S.sel;
  if (sig === railSig && rail.childElementCount) return;
  railSig = sig;
  const groups = {};
  tasks.forEach((t) => (groups[t.group || ""] = groups[t.group || ""] || []).push(t));
  const names = Object.keys(groups).sort((a, b) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)));
  const list = h("div", { class: "rail-list", role: "listbox", "aria-label": "Tareas" });
  for (const g of names) {
    if (g) list.append(h("div", { class: "group-name row" }, h("span", { class: "grow" }, g),
      h("button", { class: "btn small ghost", title: "Ejecutar todas las tareas del grupo", onclick: () => attempt(() => api("run", { group: g }), "En la cola: grupo " + g) }, icon("play"), "Grupo")));
    for (const t of groups[g]) {
      const sub = t.type === "proyectos" ? "Proyectos" : TYPE_NAMES[t.type];
      const when = !t.enabled ? "desactivada" : !ZERO(t.nextRun) ? "próximo " + fmtDate(t.nextRun) : t.type === "proyectos" && (t.projects.triggerOnSave || t.projects.triggerOnClose) ? "al guardar / cerrar" : "manual";
      list.append(h("button", {
        class: "task-item" + (t.enabled ? "" : " disabled"), role: "option", "aria-selected": t.id === S.sel ? "true" : "false",
        onclick: () => { S.sel = t.id; S.open = null; renderRail(); renderContent(); },
      }, h("span", { class: "led " + taskLed(t) }),
      h("span", { style: { minWidth: 0 } }, h("div", { class: "t-name" }, t.name), h("div", { class: "t-sub" }, sub + " · " + when)),
      t.group && false ? null : h("span")));
    }
  }
  if (!tasks.length) list.append(h("p", { class: "muted small", style: { padding: "8px" } }, "Todavía no hay tareas."));
  rail.replaceChildren(
    h("div", { class: "rail-head" }, h("h2", { class: "grow" }, "Tareas"),
      h("button", { class: "btn small", onclick: newTaskDialog }, icon("plus"), "Nueva")),
    list);
}

// ---------------------------------------------------------------------------
// Detalle de tarea
// ---------------------------------------------------------------------------
async function loadProjects(id, force) {
  if (!force && S.projects[id]) return S.projects[id];
  S.projects[id] = (await api("projects?task=" + id).catch(() => [])) || [];
  return S.projects[id];
}
async function loadHistory(id, force) {
  if (!force && S.history[id]) return S.history[id];
  S.history[id] = (await api("history?task=" + id).catch(() => [])) || [];
  return S.history[id];
}

async function renderContent() {
  const c = $("#content");
  if (!c) return;
  const t = task(S.sel);
  if (!t) {
    c.replaceChildren(h("div", { class: "empty", style: { marginTop: "40px" } },
      h("h3", null, "Creá tu primera tarea"),
      h("p", null, "Una tarea dice qué carpetas respaldar, a dónde y cuándo."),
      h("div", { class: "row", style: { justifyContent: "center", marginTop: "14px" } },
        h("button", { class: "btn primary", onclick: () => openEditorNew("proyectos") }, "Proyectos de DAW"),
        h("button", { class: "btn", onclick: () => openEditorNew("incremental") }, "Respaldo clásico"))));
    return;
  }
  const facts = [
    h("span", null, "Cuándo: ", h("b", null, scheduleText(t))),
    h("span", null, "Destino: ", h("b", null, (t.destinations || []).map(locText).join("  +  ") || "sin destino")),
    h("span", null, "Último: ", h("b", null, t.last ? fmtDate(t.last.finished || t.last.started) : "nunca")),
  ];
  if (!ZERO(t.nextRun) && t.enabled) facts.push(h("span", null, "Próximo: ", h("b", null, fmtDate(t.nextRun))));
  const head = h("div", { class: "task-head" },
    h("div", { style: { minWidth: 0 } },
      h("div", { class: "row wrap" }, h("h1", null, t.name), h("span", { class: "tag amber" }, TYPE_NAMES[t.type]),
        !t.enabled ? h("span", { class: "tag" }, "Desactivada") : null, t.group ? h("span", { class: "tag" }, t.group) : null),
      h("div", { class: "facts" }, facts)),
    h("div", { class: "task-actions" },
      h("button", { class: "btn primary", onclick: () => attempt(() => api("run", { ids: [t.id] }), "En la cola: " + t.name) }, icon("play"), "Ejecutar ahora"),
      (t.type === "incremental" || t.type === "diferencial") ? h("button", { class: "btn", onclick: () => attempt(() => api("run", { ids: [t.id], force: true }), "En la cola un respaldo completo de " + t.name) }, "Forzar completo") : null,
      h("button", { class: "btn", onclick: () => openEditor(t) }, "Editar"),
      h("button", { class: "btn", onclick: () => taskMenu(t) }, "Más…")));
  const body = h("div", { id: "taskbody" });
  c.replaceChildren(h("div", { style: { maxWidth: "1200px" } }, head, body));
  if (!(t.destinations || []).length && t.type !== "ficticio") {
    body.append(h("div", { class: "note-box" }, "Esta tarea todavía no tiene destino. ", h("button", { class: "btn small", onclick: () => openEditor(t, "archivos") }, "Elegir destino")));
  }
  if (t.type === "proyectos") return renderProjects(t, body);
  if (t.type === "ficticio") {
    body.append(h("p", { class: "muted" }, "Esta tarea no copia archivos: sólo ejecuta sus eventos en el horario elegido."));
    return;
  }
  return renderHistory(t, body);
}

function taskMenu(t) {
  const close = () => $("#layer").replaceChildren();
  const item = (label, fn, cls) => h("button", { class: "btn " + (cls || ""), style: { justifyContent: "flex-start" }, onclick: async () => { close(); await fn(); } }, label);
  sheet({
    title: t.name, size: "sm",
    body: h("div", { style: { display: "grid", gap: "8px" } },
      item(t.enabled ? "Desactivar la tarea" : "Activar la tarea", async () => { await attempt(() => api("task/toggle", { ids: [t.id] })); await reload(); }),
      item("Clonar", async () => { const c = await attempt(() => api("task/clone", { id: t.id })); if (c) { S.sel = c.id; await reload(); } }),
      t.type !== "proyectos" && t.type !== "ficticio" ? item("Reiniciar (el próximo respaldo será completo)", async () => attempt(() => api("task/reset", { id: t.id }), "El próximo respaldo será completo")) : null,
      item("Subir en la lista", async () => { await api("task/move", { id: t.id, delta: -1 }); await reload(); }),
      item("Bajar en la lista", async () => { await api("task/move", { id: t.id, delta: 1 }); await reload(); }),
      item("Borrar la tarea…", () => deleteTask(t), "danger")),
    foot: [h("button", { class: "btn", onclick: close }, "Cerrar")],
  });
}

async function deleteTask(t) {
  const files = h("input", { type: "checkbox" });
  const ok = await confirmSheet("Borrar " + t.name, h("div", null,
    h("p", null, "La tarea se quita de la lista. Los respaldos que ya hizo quedan en el destino, salvo que marques lo de abajo."),
    t.type !== "proyectos" ? h("label", { class: "check" }, files, h("span", null, "Borrar también los archivos de sus respaldos", h("small", null, "No se puede deshacer."))) : null),
  "Borrar tarea", true);
  if (!ok) return;
  await attempt(() => api("task/delete", { id: t.id, files: files.checked }), "Tarea borrada");
  S.sel = null;
  await reload();
}

// ----- Proyectos -----
async function renderProjects(t, body) {
  const projects = await loadProjects(t.id);
  if (S.sel !== t.id) return;
  const search = h("input", { type: "text", placeholder: "Buscar proyecto", value: S.filter, "aria-label": "Buscar proyecto", style: { width: "220px" },
    oninput: (e) => { S.filter = e.target.value; fillRows(); } });
  const tbody = h("tbody");
  const table = h("table", { class: "table" },
    h("thead", null, h("tr", null, h("th", { style: { width: "34px" } }), h("th", null, "Proyecto"), h("th", { style: { width: "150px" } }, "Programa"),
      h("th", { style: { width: "140px" } }, "Última versión"), h("th", { class: "r", style: { width: "90px" } }, "Versiones"), h("th", { style: { width: "230px" } }, "Estado"))), tbody);
  const dirty = projects.filter((p) => p.dirty && !p.excluded).length;
  body.append(
    h("div", { class: "section-title" }, h("h2", null, "Proyectos"),
      h("span", { class: "muted small" }, projects.length + " encontrados" + (dirty ? " · " + dirty + " con cambios sin respaldar" : "")),
      h("span", { class: "grow" }), search,
      h("button", { class: "btn", onclick: async () => { S.projects[t.id] = await attempt(() => api("projects/rescan", { task: t.id })) || []; renderContent(); } }, "Buscar de nuevo")),
    projects.length ? table : h("div", { class: "empty" },
      h("h3", null, "No se encontraron proyectos"),
      h("p", null, (t.sources || []).length ? "Resguardo busca archivos .als, .cpr, .ptx y similares dentro de " + (t.sources || []).map(locText).join(", ") + "." : "Agregá las carpetas donde guardás tus proyectos."),
      h("button", { class: "btn", onclick: () => openEditor(t, "archivos") }, "Elegir carpetas")));

  function fillRows() {
    const f = S.filter.trim().toLowerCase();
    tbody.replaceChildren();
    for (const p of projects) {
      if (f && !(p.name + " " + p.key).toLowerCase().includes(f)) continue;
      const led = p.excluded ? "off" : p.pending ? "error" : p.dirty ? "dirty" : p.lastSnap && !ZERO(p.lastSnap) ? "ok" : "off";
      const state = p.excluded ? "Excluido" : p.pending ? "Pendiente (archivo en uso)" : p.dirty ? "Cambios sin respaldar" : "Al día";
      const open = S.open === p.key;
      tbody.append(h("tr", { class: "clickable" + (open ? " open" : ""), onclick: () => { S.open = open ? null : p.key; S.snaps = null; S.selSnap = null; S.snapDest = 0; fillRows(); } },
        h("td", null, h("span", { class: "led " + led })),
        h("td", null, h("div", { class: "p-name" }, p.name), h("div", { class: "p-path" }, p.key.split("/").slice(0, -1).join(" / "))),
        h("td", null, p.dawName),
        h("td", null, fmtDate(p.lastSnap)),
        h("td", { class: "r" }, p.snapshots || "—"),
        h("td", null, state, p.custom ? h("span", { class: "tag", style: { marginLeft: "6px" } }, "destino propio") : null)));
      if (open) tbody.append(h("tr", { class: "detail" }, h("td", { colspan: 6, style: { padding: 0 } }, projectDetail(t, p))));
    }
  }
  fillRows();
}

function projectDetail(t, p) {
  const box = h("div", { class: "detail-inner" });
  const dests = p.dests || [];
  const destSel = dests.length > 1 ? h("select", { "aria-label": "Destino", onchange: (e) => { S.snapDest = +e.target.value; S.snaps = null; S.selSnap = null; loadSnaps(); } },
    dests.map((d, i) => h("option", { value: i, selected: i === S.snapDest }, locText(d)))) : null;
  const actions = h("div", { class: "row wrap", style: { padding: "12px 0 4px" } },
    h("button", { class: "btn primary", onclick: () => attempt(() => api("run", { ids: [t.id], project: p.key }), "En la cola: " + p.name) }, icon("play"), "Respaldar ahora"),
    h("button", { class: "btn", onclick: () => overrideDialog(t, p) }, p.custom ? "Destino propio…" : "Elegir otro destino…"),
    h("button", { class: "btn", onclick: () => analyzeDialog(t, p) }, "Archivos que usa…"),
    h("button", { class: "btn", onclick: async () => { await attempt(() => api("projects/override", { task: t.id, key: p.key, override: { ...(t.projects.overrides[p.key] || {}), excluded: !p.excluded } })); await reload(); S.projects[t.id] = null; renderContent(); } }, p.excluded ? "Volver a respaldarlo" : "No respaldar este proyecto"),
    h("button", { class: "btn ghost", onclick: () => api("fs/open", { path: p.dir }) }, "Abrir carpeta del proyecto"),
    h("span", { class: "grow" }), destSel);
  const laneBox = h("div");
  box.append(actions, laneBox);

  async function loadSnaps() {
    laneBox.replaceChildren(h("p", { class: "muted", style: { padding: "12px 0" } }, "Leyendo las versiones…"));
    try {
      S.snaps = await api(`projects/snapshots?task=${t.id}&key=${encodeURIComponent(p.key)}&dest=${S.snapDest}`);
    } catch (e) {
      laneBox.replaceChildren(h("p", { class: "muted", style: { padding: "12px 0" } }, "No se pudo leer el destino: " + e.message));
      return;
    }
    drawLane();
  }

  function drawLane() {
    const snaps = S.snaps || [];
    if (!snaps.length) {
      laneBox.replaceChildren(h("p", { class: "muted", style: { padding: "12px 0" } }, "Este proyecto todavía no tiene versiones guardadas en este destino."));
      return;
    }
    const now = Date.now();
    const times = snaps.map((s) => new Date(s.time).getTime());
    const oldest = Math.min(...times);
    const span = Math.max(now - oldest, 3600e3) * 1.08;
    const min = now - span;
    const unit = span <= 2 * 86400e3 ? 3600e3 : 86400e3;
    const units = span / unit;
    const lane = h("div", { class: "lane", style: { "--day": (100 / units).toFixed(4) + "%" }, role: "group", "aria-label": "Línea de versiones" });
    snaps.forEach((s) => {
      const x = ((new Date(s.time).getTime() - min) / span) * 100;
      lane.append(h("button", {
        class: "marker" + (S.selSnap === s.name ? " sel" : "") + (s.parked ? " parked" : ""),
        style: { left: Math.min(99.6, Math.max(0.4, x)) + "%" }, title: fmtDate(s.time) + " · " + s.changed + " archivos cambiados",
        "aria-label": "Versión del " + fmtDate(s.time), onclick: () => { S.selSnap = s.name; drawLane(); },
      }));
    });
    const list = h("div", { class: "snap-list" });
    snaps.forEach((s, i) => {
      const reason = s.reason ? " · " + s.reason : "";
      const what = i === snaps.length - 1 ? "Primera copia: " + s.files + " archivos, " + fmtBytes(s.total) :
        s.changed + " archivos nuevos o cambiados" + (s.removed ? ", " + s.removed + " quitados" : "") + " · " + fmtBytes(s.bytes);
      list.append(h("div", { class: "snap" + (S.selSnap === s.name ? " sel" : ""), onclick: () => { S.selSnap = s.name; drawLane(); } },
        h("div", null, h("div", { class: "when" }, fmtDate(s.time)), h("div", { class: "faint small" }, s.parked ? "Fijada: no se borra" : (i === 0 ? "La más reciente" : ""))),
        h("div", { class: "muted small" }, what + reason),
        h("div", { class: "row" },
          h("button", { class: "btn small", onclick: (e) => { e.stopPropagation(); restoreProjectDialog(t, p, s, false); } }, "Restaurar…"),
          h("button", { class: "btn small", title: "Pone el archivo de proyecto de esta versión al lado del actual, con otro nombre", onclick: (e) => { e.stopPropagation(); restoreProjectDialog(t, p, s, true); } }, "Sólo el " + (p.main ? p.main.split(".").pop() : "proyecto")),
          h("button", { class: "btn small ghost", onclick: async (e) => { e.stopPropagation(); await attempt(() => api("projects/park", { task: t.id, key: p.key, dest: S.snapDest, snap: s.name, parked: !s.parked })); s.parked = !s.parked; drawLane(); } }, s.parked ? "Soltar" : "Fijar"))));
    });
    const first = new Date(min);
    laneBox.replaceChildren(
      h("div", { class: "lane-wrap" }, lane,
        h("div", { class: "lane-scale" }, h("span", null, fmtDate(first.toISOString(), { noTime: unit !== 3600e3 })),
          h("span", null, unit === 3600e3 ? "una línea por hora" : "una línea por día"), h("span", null, "ahora"))),
      list);
    const sel = list.querySelector(".snap.sel");
    if (sel) sel.scrollIntoView({ block: "nearest" });
  }
  if (dests.length) loadSnaps(); else laneBox.append(h("p", { class: "muted" }, "Sin destino."));
  return box;
}

async function restoreProjectDialog(t, p, s, onlyProject) {
  const target = h("input", { type: "text", placeholder: "Escritorio / Resguardo - restaurados", value: "" });
  const ext = h("input", { type: "checkbox" });
  const body = onlyProject ? h("div", null,
    h("p", null, "Se copia el archivo de proyecto de la versión del ", h("b", null, fmtDate(s.time)), " a la carpeta original, con la fecha en el nombre (por ejemplo, “" + (p.main || "Tema.als").replace(/(\.[^.]+)$/, " (resguardo " + s.name + ")$1") + "”)."),
    h("p", { class: "muted" }, "No pisa nada: el archivo actual queda como está. Abrilo desde el DAW y comparalo."))
    : h("div", null,
      h("p", null, "Se arma una copia completa del proyecto tal como estaba el ", h("b", null, fmtDate(s.time)), ". No se toca el proyecto original."),
      h("div", { class: "field" }, h("span", null, "Carpeta donde dejarlo"), pathPick(target, { title: "Dónde restaurar" })),
      h("label", { class: "check" }, ext, h("span", null, "Devolver los samples externos a su lugar original si ya no están ahí")));
  const ok = await confirmSheet("Restaurar " + p.name, body, onlyProject ? "Recuperar el archivo" : "Restaurar");
  if (!ok) return;
  S.restoreWatch = true;
  await attempt(() => api("projects/restore", { task: t.id, key: p.key, dest: S.snapDest, snap: s.name, opt: { target: target.value.trim(), onlyProjectFiles: onlyProject, externalsToOrigin: ext.checked } }),
    "Restaurando… te aviso cuando termine");
}

async function analyzeDialog(t, p) {
  const body = h("div", null, h("p", { class: "muted" }, "Leyendo " + (p.main || "el proyecto") + "…"));
  sheet({ title: "Archivos que usa " + p.name, size: "md", body, foot: [h("button", { class: "btn", onclick: () => $("#layer").replaceChildren() }, "Cerrar")] });
  let r;
  try { r = await api(`projects/analyze?task=${t.id}&key=${encodeURIComponent(p.key)}`); } catch (e) { body.replaceChildren(h("p", null, e.message)); return; }
  const kids = [];
  if (!r.supported) kids.push(h("p", null, r.note || "Para este programa se respalda la carpeta completa."));
  else {
    kids.push(h("dl", { class: "kv" },
      h("dt", null, "Archivos que usa"), h("dd", null, r.total),
      h("dt", null, "Dentro de la carpeta"), h("dd", null, r.internal),
      h("dt", null, "Fuera de la carpeta"), h("dd", null, (r.external || []).length + (r.library ? " (" + r.library + " de librerías de fábrica)" : "")),
      h("dt", null, "No encontrados"), h("dd", null, (r.missing || []).length)));
    if (r.note) kids.push(h("p", { class: "muted small", style: { marginTop: "10px" } }, r.note));
    if ((r.external || []).length) {
      kids.push(h("h3", { style: { margin: "16px 0 6px" } }, "Fuera de la carpeta del proyecto"),
        h("p", { class: "muted small" }, t.projects.includeExternal ? "Se copian dentro del respaldo, en “_Samples externos”." + (t.projects.includeFactory ? "" : " Los de librerías de fábrica no, porque se pueden reinstalar.") : "Esta tarea no copia los samples externos (se cambia en Editar → Música)."),
        h("div", { class: "items" }, r.external.map((x) => h("div", { class: "it" }, h("span", { class: "txt", title: x.path }, x.path), x.library ? h("span", { class: "tag" }, "fábrica") : null, h("span", { class: "faint small" }, fmtBytes(x.size))))));
    }
    if ((r.missing || []).length) {
      kids.push(h("h3", { style: { margin: "16px 0 6px" } }, "No se encuentran"),
        h("p", { class: "muted small" }, "El proyecto los usa pero no están en el disco. Si los tenés en otra carpeta, sumala en Opciones → Música → Carpetas donde buscar audio."),
        h("div", { class: "items" }, r.missing.slice(0, 200).map((m) => h("div", { class: "it" }, h("span", { class: "txt" }, m)))));
    }
  }
  body.replaceChildren(...kids);
}

async function overrideDialog(t, p) {
  const ov = clone(t.projects.overrides[p.key] || { destinations: [], excluded: false, note: "" });
  const list = h("div");
  const note = h("textarea", { rows: 2, value: ov.note || "", placeholder: "Por ejemplo: cliente, disco, entrega…" });
  const draw = () => list.replaceChildren(locList(ov.destinations, draw, { dest: true }));
  draw();
  const ok = await confirmSheet("Destino de " + p.name, h("div", null,
    h("p", { class: "muted" }, "Si elegís destinos acá, este proyecto se guarda sólo en ellos (en lugar de los de la tarea). Dejalo vacío para usar los de la tarea: " + ((t.destinations || []).map(locText).join(", ") || "ninguno") + "."),
    list,
    h("div", { class: "field", style: { marginTop: "12px" } }, h("span", null, "Nota"), note)), "Guardar");
  if (!ok) return;
  ov.note = note.value;
  await attempt(() => api("projects/override", { task: t.id, key: p.key, override: ov }), "Guardado");
  await reload();
  S.projects[t.id] = null;
  S.snaps = null;
  renderContent();
}

// ----- Historial (tareas clásicas) -----
async function renderHistory(t, body) {
  const hist = await loadHistory(t.id);
  if (S.sel !== t.id) return;
  const tbody = h("tbody");
  body.append(h("div", { class: "section-title" }, h("h2", null, "Respaldos"), h("span", { class: "muted small" }, hist.length + " en el historial"),
    h("span", { class: "grow" }),
    hist.length ? h("button", { class: "btn", onclick: async () => {
      const files = h("input", { type: "checkbox" });
      const ok = await confirmSheet("Vaciar el historial", h("div", null, h("p", null, "Se quitan todos los respaldos no fijados del historial."), h("label", { class: "check" }, files, h("span", null, "Borrar también sus archivos del destino"))), "Vaciar", true);
      if (ok) { await attempt(() => api("history/delete", { task: t.id, ids: [], files: files.checked })); await loadHistory(t.id, true); renderContent(); }
    } }, "Vaciar historial") : null));
  if (!hist.length) {
    body.append(h("div", { class: "empty" }, h("h3", null, "Sin respaldos todavía"), h("p", null, "Ejecutá la tarea para hacer el primero.")));
    return;
  }
  body.append(h("table", { class: "table" },
    h("thead", null, h("tr", null, h("th", { style: { width: "34px" } }), h("th", null, "Fecha"), h("th", null, "Tipo"), h("th", { class: "r", style: { width: "100px" } }, "Archivos"), h("th", { class: "r", style: { width: "110px" } }, "Tamaño"), h("th", { style: { width: "150px" } }, "Estado"), h("th", { style: { width: "90px" } }, ""))), tbody));
  const fill = () => {
    tbody.replaceChildren();
    for (const r of hist) {
      const led = r.status === "ok" ? "ok" : r.status === "cancelado" ? "off" : "error";
      const open = S.open === r.id;
      tbody.append(h("tr", { class: "clickable" + (open ? " open" : ""), onclick: () => { S.open = open ? null : r.id; fill(); } },
        h("td", null, h("span", { class: "led " + led })),
        h("td", null, fmtDate(r.started)),
        h("td", null, TYPE_NAMES[r.type] || r.type, r.encrypted ? h("span", { class: "tag", style: { marginLeft: "6px" } }, "cifrado") : null),
        h("td", { class: "r" }, r.files),
        h("td", { class: "r" }, fmtBytes(r.bytes)),
        h("td", null, { ok: "Correcto", errores: r.errors + " errores", cancelado: "Cancelado", fallido: "Falló" }[r.status] || r.status),
        h("td", null, r.parked ? h("span", { class: "tag", title: "No se borra" }, "Fijado") : null)));
      if (open) {
        const outs = (r.outputs || []).map((o) => h("div", { class: "it" }, h("span", { class: "txt", title: o.path }, o.path),
          (t.destinations[o.dest] || {}).kind === "local" || !(t.destinations[o.dest] || {}).kind ? h("button", { class: "btn small ghost", onclick: () => api("fs/open", { path: o.kind === "carpeta" || o.kind === "individual" ? o.path : o.path.replace(/[\\/][^\\/]*$/, "") }) }, "Abrir") : null));
        tbody.append(h("tr", { class: "detail" }, h("td", { colspan: 7 }, h("div", { class: "detail-inner" },
          h("dl", { class: "kv", style: { margin: "6px 0 10px" } },
            h("dt", null, "Empezó"), h("dd", null, fmtFull(r.started)),
            h("dt", null, "Terminó"), h("dd", null, ZERO(r.finished) ? "—" : fmtFull(r.finished)),
            h("dt", null, "Motivo"), h("dd", null, r.reason || "—"),
            r.parent ? h("dt", null, "Depende de") : null, r.parent ? h("dd", null, "el completo del " + fmtDate((hist.find((x) => x.id === r.parent) || {}).started)) : null),
          h("div", { class: "items" }, outs),
          h("div", { class: "row wrap" },
            h("button", { class: "btn primary", onclick: () => restoreRecordDialog(t, r) }, "Restaurar…"),
            h("button", { class: "btn", onclick: async () => { await api("history/park", { task: t.id, ids: [r.id], parked: !r.parked }); await loadHistory(t.id, true); renderContent(); } }, r.parked ? "Soltar" : "Fijar (no borrar)"),
            !r.parked ? h("button", { class: "btn danger", onclick: async () => {
              const files = h("input", { type: "checkbox", checked: true });
              const ok = await confirmSheet("Borrar el respaldo del " + fmtDate(r.started), h("label", { class: "check" }, files, h("span", null, "Borrar también sus archivos del destino")), "Borrar", true);
              if (ok) { await attempt(() => api("history/delete", { task: t.id, ids: [r.id], files: files.checked })); await loadHistory(t.id, true); renderContent(); }
            } }, "Borrar…") : null)))));
      }
    }
  };
  fill();
}

async function restoreRecordDialog(t, r) {
  const target = h("input", { type: "text", value: S.data.home ? S.data.home + (isWin() ? "\\Desktop\\Resguardo - restaurados\\" : "/Desktop/Resguardo - restaurados/") + t.name : "" });
  const dests = [...new Set((r.outputs || []).map((o) => o.dest))];
  const destSel = dests.length > 1 ? h("select", null, dests.map((d) => h("option", { value: d }, locText(t.destinations[d])))) : null;
  const pw = h("input", { type: "password", placeholder: "Sólo si cambiaste la contraseña de la tarea" });
  const chain = r.type === "completo" ? "Se restaura este respaldo completo." : r.type === "incremental" ? "Se restaura el completo base y todos los incrementales hasta éste." : "Se restaura el completo base y este diferencial.";
  const ok = await confirmSheet("Restaurar el respaldo del " + fmtDate(r.started), h("div", null,
    h("p", null, chain + " Los archivos que ya existan en la carpeta elegida se reemplazan."),
    h("div", { class: "field" }, h("span", null, "Carpeta donde restaurar"), pathPick(target, { title: "Dónde restaurar" })),
    destSel ? h("div", { class: "field" }, h("span", null, "Desde"), destSel) : null,
    r.encrypted ? h("div", { class: "field" }, h("span", null, "Contraseña"), pw) : null), "Restaurar");
  if (!ok) return;
  if (!target.value.trim()) return toast("Elegí una carpeta", true);
  S.restoreWatch = true;
  await attempt(() => api("history/restore", { task: t.id, id: r.id, dest: destSel ? +destSel.value : dests[0] || 0, target: target.value.trim(), password: pw.value }), "Restaurando… te aviso cuando termine");
}

// ---------------------------------------------------------------------------
// Nueva tarea / editor
// ---------------------------------------------------------------------------
function newTaskDialog() {
  const close = () => $("#layer").replaceChildren();
  const opt = (type, title, text) => h("button", { class: "type-opt", style: { textAlign: "left" }, onclick: () => { close(); openEditorNew(type); } }, h("b", null, title), h("small", null, text));
  sheet({
    title: "Nueva tarea", size: "md",
    body: h("div", { class: "types", style: { gridTemplateColumns: "1fr" } },
      opt("proyectos", "Proyectos de DAW", "Busca tus proyectos de Ableton, Cubase y Pro Tools, guarda una versión cada vez que los guardás o cerrás el programa, y te deja volver a cualquier versión."),
      opt("incremental", "Respaldo clásico", "Como en Cobian: copia carpetas completas, incrementales o diferenciales, con o sin compresión, en el horario que elijas."),
      opt("ficticio", "Tarea de eventos", "No copia nada: sirve para programar acciones, como cerrar un programa o apagar el equipo.")),
    foot: [h("button", { class: "btn", onclick: close }, "Cancelar")],
  });
}

async function openEditorNew(type) {
  const t = await api("task/template?type=" + type);
  openEditor(t, "general", true);
}

function openEditor(orig, startTab, isNew) {
  const t = normTask(clone(orig));
  delete t.nextRun; delete t.lastRun; delete t.last; delete t.backups; delete t.running; delete t.queued;
  t.projects = t.projects || {};
  t.projects.overrides = t.projects.overrides || {};
  let tab = startTab || "general";
  const tabsFor = () => {
    if (t.type === "proyectos") return [["general", "General"], ["archivos", "Carpetas y destino"], ["musica", "Música"], ["horario", "Horario"], ["filtros", "Filtros"], ["eventos", "Eventos"], ["avanzado", "Avanzado"]];
    if (t.type === "ficticio") return [["general", "General"], ["horario", "Horario"], ["eventos", "Eventos"]];
    return [["general", "General"], ["archivos", "Archivos"], ["horario", "Horario"], ["dinamica", "Dinámica"], ["compresion", "Compresión"], ["filtros", "Filtros"], ["eventos", "Eventos"], ["avanzado", "Avanzado"]];
  };
  const side = h("div", { class: "sidetabs", role: "tablist" });
  const pane = h("div", { class: "pane", role: "tabpanel" });
  const drawTabs = () => {
    side.replaceChildren(...tabsFor().map(([k, l]) => h("button", { role: "tab", "aria-selected": tab === k ? "true" : "false", onclick: () => { tab = k; drawTabs(); drawPane(); } }, l)));
  };
  const drawPane = () => { pane.replaceChildren(...editorPane(t, tab, () => { drawTabs(); drawPane(); })); pane.scrollTop = 0; };
  drawTabs(); drawPane();
  const save = async () => {
    if (!t.name.trim()) { toast("Poné un nombre a la tarea", true); return; }
    const saved = await attempt(() => api("task/save", t), "Tarea guardada");
    if (!saved) return;
    $("#layer").replaceChildren();
    S.sel = saved.id;
    S.projects[saved.id] = null;
    S.history[saved.id] = null;
    await reload();
    if (saved.type === "proyectos") setTimeout(async () => { await loadProjects(saved.id, true); renderContent(); }, 1500);
  };
  sheet({
    title: isNew ? "Nueva tarea" : "Editar " + orig.name,
    body: h("div", { class: "editor" }, side, pane), flush: true,
    foot: [h("button", { class: "btn", onclick: () => $("#layer").replaceChildren() }, "Cancelar"), h("button", { class: "btn primary", onclick: save }, "Guardar tarea")],
  });
}

// Campos ligados a un objeto
function fText(obj, key, label, attrs = {}) {
  const i = h("input", { type: attrs.type || "text", value: obj[key] ?? "", oninput: (e) => (obj[key] = attrs.type === "number" ? +e.target.value : e.target.value), ...attrs });
  return label ? h("label", { class: "field" }, h("span", null, label), i) : i;
}
function fNum(obj, key, label, attrs = {}) { return fText(obj, key, label, { type: "number", min: 0, ...attrs }); }
function fCheck(obj, key, label, help, onchange) {
  return h("label", { class: "check" }, h("input", { type: "checkbox", checked: !!obj[key], onchange: (e) => { obj[key] = e.target.checked; onchange && onchange(); } }),
    h("span", null, label, help ? h("small", null, help) : null));
}
function fSelect(obj, key, label, options, onchange, numeric) {
  const s = h("select", { onchange: (e) => { obj[key] = numeric ? +e.target.value : e.target.value; onchange && onchange(); } },
    options.map(([v, l]) => h("option", { value: v, selected: String(obj[key]) === String(v) }, l)));
  return label ? h("label", { class: "field" }, h("span", null, label), s) : s;
}

function editorPane(t, tab, redraw) {
  const P = t.projects;
  switch (tab) {
    case "general": {
      const typeOpt = (v, title, text) => h("label", { class: "type-opt" },
        h("input", { type: "radio", name: "ttype", checked: t.type === v, onchange: () => { t.type = v; if (v === "proyectos") t.separated = false; redraw(); } }),
        h("b", null, title), h("small", null, text));
      return [
        h("h2", null, "General"), h("p", { class: "muted" }, "El tipo define cómo se guardan las copias."),
        fText(t, "name", "Nombre"),
        h("div", { class: "types" },
          typeOpt("proyectos", "Proyectos de DAW", "Versiones por proyecto, con samples externos."),
          typeOpt("completo", "Completo", "Copia todo cada vez."),
          typeOpt("incremental", "Incremental", "Sólo lo cambiado desde el último respaldo."),
          typeOpt("diferencial", "Diferencial", "Lo cambiado desde el último completo."),
          typeOpt("ficticio", "Sólo eventos", "No copia: ejecuta acciones.")),
        h("div", { class: "inline-fields" },
          fCheck(t, "enabled", "Tarea activa"),
          h("label", { class: "field" }, h("span", null, "Grupo (opcional)"),
            h("input", { type: "text", value: t.group || "", list: "groups", oninput: (e) => (t.group = e.target.value) }),
            h("datalist", { id: "groups" }, [...new Set((S.data.tasks || []).map((x) => x.group).filter(Boolean))].map((g) => h("option", { value: g }))))),
        t.type !== "ficticio" && t.type !== "proyectos" ? h("div", { class: "block" },
          fCheck(t, "includeSubdirs", "Incluir subcarpetas"),
          fCheck(t, "separated", "Crear un respaldo nuevo cada vez, con la fecha en el nombre", "Si lo desmarcás, cada respaldo pisa al anterior en la misma carpeta."),
          isWin() ? fCheck(t, "useArchiveAttr", "Detectar cambios con el atributo de archivo de Windows", "Si no, se comparan fecha y tamaño (recomendado).") : null) : null,
        t.type !== "ficticio" && isWin() ? fCheck(t, "useVSS", "Usar instantáneas de volumen (VSS) para copiar archivos abiertos", "Necesita ejecutar Resguardo como administrador.") : null,
      ];
    }
    case "archivos": {
      const srcBox = h("div"), dstBox = h("div");
      const drawS = () => srcBox.replaceChildren(locList(t.sources, drawS, { files: t.type !== "proyectos", remote: t.type !== "proyectos" }));
      const drawD = () => dstBox.replaceChildren(locList(t.destinations, drawD, { dest: true }));
      drawS(); drawD();
      return [
        h("h2", null, t.type === "proyectos" ? "Carpetas y destino" : "Archivos"),
        h("p", { class: "muted" }, t.type === "proyectos" ? "Elegí las carpetas donde guardás tus proyectos (por ejemplo, la carpeta de Ableton o la de sesiones). Resguardo encuentra cada proyecto adentro." : "Qué copiar y a dónde. Podés tener varios destinos: cada uno recibe una copia completa."),
        h("h3", { style: { margin: "14px 0 8px" } }, t.type === "proyectos" ? "Carpetas de proyectos" : "Orígenes"), srcBox,
        h("h3", { style: { margin: "18px 0 8px" } }, "Destinos"), dstBox,
        h("p", { class: "muted small" }, "En las rutas de destino podés usar {tarea}, {fecha}, {año}, {mes}, {dia}, {equipo} y {usuario}."),
        t.type === "proyectos" ? h("p", { class: "muted small" }, "Para mandar un proyecto puntual a otro destino, abrilo en la lista de proyectos y elegí “Elegir otro destino”.") : null,
      ];
    }
    case "musica": {
      const daws = (S.data.daws || []).map((d) => h("label", { class: "check" },
        h("input", { type: "checkbox", checked: (P.daws || []).includes(d.id), onchange: (e) => { P.daws = (P.daws || []).filter((x) => x !== d.id); if (e.target.checked) P.daws.push(d.id); } }),
        h("span", null, d.name, h("small", null, d.exts + (["ableton", "cubase", "protools"].includes(d.id) ? "" : " · se respalda la carpeta completa")))));
      const keepMode = P.keepVersions > 0 ? "n" : P.keepDays > 0 ? "d" : "all";
      const keepBox = h("div", { class: "inline-fields" });
      const drawKeep = (mode) => {
        keepBox.replaceChildren(
          h("label", { class: "field" }, h("span", null, "Versiones a conservar"),
            h("select", { onchange: (e) => { const m = e.target.value; P.keepVersions = m === "n" ? (P.keepVersions || 30) : 0; P.keepDays = m === "d" ? (P.keepDays || 90) : 0; drawKeep(m); } },
              h("option", { value: "all", selected: mode === "all" }, "Todas"), h("option", { value: "n", selected: mode === "n" }, "Las últimas…"), h("option", { value: "d", selected: mode === "d" }, "Las de los últimos… días"))),
          mode === "n" ? fNum(P, "keepVersions", "Cantidad", { min: 1 }) : null,
          mode === "d" ? fNum(P, "keepDays", "Días", { min: 1 }) : null);
      };
      drawKeep(keepMode);
      return [
        h("h2", null, "Música"), h("p", { class: "muted" }, "Qué programas buscar y cuándo guardar una versión nueva."),
        h("div", { class: "block" }, h("h3", null, "Programas"), h("div", { class: "daw-grid" }, daws)),
        h("div", { class: "block" }, h("h3", null, "Cuándo guardar una versión"),
          fCheck(P, "triggerOnSave", "Cuando guardás el proyecto", "Espera a que dejes de guardar por un momento (se ajusta en Opciones → Música)."),
          fCheck(P, "triggerOnClose", "Cuando cerrás el DAW", "Hace un último repaso de los proyectos que cambiaron."),
          h("p", { class: "muted small" }, "Además corre en el horario de la pestaña Horario, como red de seguridad.")),
        h("div", { class: "block" }, h("h3", null, "Qué incluir"),
          fCheck(P, "includeExternal", "Samples que están fuera de la carpeta del proyecto", "Se guardan dentro del respaldo, en “_Samples externos”, así el proyecto abre completo aunque se pierda la librería."),
          fCheck(P, "includeFactory", "También los de librerías de fábrica (Core Library, packs, VST Sound)", "Ocupan mucho y se pueden reinstalar."),
          fCheck(P, "smartExclusions", "Saltear cachés que el DAW regenera solo", ".asd de Ableton, WaveCache.wfm de Pro Tools, imágenes de onda de Cubase, picos de REAPER."),
          fCheck(P, "verify", "Verificar cada archivo después de copiarlo", "Más lento, pero detecta discos con problemas.")),
        h("div", { class: "block" }, h("h3", null, "Versiones"), keepBox,
          fCheck(P, "thinning", "Afinar las versiones viejas", "Guarda todas las del último día, una por día durante un mes y una por semana después. Las versiones fijadas no se tocan.")),
        h("div", { class: "block" }, fNum(P, "maxDepth", "Profundidad de búsqueda de proyectos (niveles de carpetas)", { min: 1, max: 20 })),
      ];
    }
    case "horario": {
      const s = t.schedule;
      const kind = fSelect(s, "kind", "Ejecutar", [["manual", "Sólo cuando lo pida"], ["diario", "Todos los días"], ["semanal", "Algunos días de la semana"], ["mensual", "Una vez por mes"], ["anual", "Una vez por año"], ["temporizador", "Cada cierta cantidad de minutos"], ["unavez", "Una sola vez"], ["inicio", "Al iniciar Resguardo"]], redraw);
      const fields = [];
      if (["diario", "semanal", "mensual", "anual", "unavez"].includes(s.kind)) fields.push(fText(s, "time", "Hora", { type: "time" }));
      if (s.kind === "unavez") fields.push(fText(s, "date", "Fecha", { type: "date" }));
      if (s.kind === "mensual" || s.kind === "anual") fields.push(fNum(s, "monthDay", "Día del mes", { min: 1, max: 31 }));
      if (s.kind === "anual") fields.push(fSelect(s, "month", "Mes", MESES.map((m, i) => [i + 1, m]), null, true));
      if (s.kind === "temporizador") fields.push(fNum(s, "everyMinutes", "Cada (minutos)", { min: 1 }));
      const week = s.kind === "semanal" ? h("div", { class: "row wrap", style: { margin: "4px 0 12px" } }, [1, 2, 3, 4, 5, 6, 0].map((d) => h("label", { class: "check", style: { marginRight: "10px" } },
        h("input", { type: "checkbox", checked: (s.weekdays || []).includes(d), onchange: (e) => { s.weekdays = (s.weekdays || []).filter((x) => x !== d); if (e.target.checked) s.weekdays.push(d); } }), h("span", null, DIAS_LARGOS[d])))) : null;
      return [
        h("h2", null, "Horario"), h("p", { class: "muted" }, "Cuándo se ejecuta la tarea."),
        h("div", { class: "inline-fields" }, kind, ...fields), week,
        h("div", { class: "block" }, h("h3", null, "Además"),
          s.kind !== "inicio" ? fCheck(s, "atStartup", "Al iniciar Resguardo") : null,
          t.type !== "proyectos" ? fCheck(s, "onDawClose", "Cuando se cierra un DAW (Ableton, Cubase, Pro Tools…)") : null,
          t.type !== "proyectos" ? fCheck(s, "onDawSave", "Cuando se guarda un proyecto dentro de los orígenes") : null,
          t.type === "proyectos" ? h("p", { class: "muted small" }, "Los disparadores al guardar y al cerrar el DAW están en la pestaña Música.") : null),
        h("div", { class: "block" }, h("h3", null, "Franja horaria (opcional)"),
          h("p", { class: "muted small" }, "Si la ejecución programada cae fuera de esta franja, se saltea."),
          h("div", { class: "inline-fields" }, fText(s, "windowFrom", "Desde", { type: "time" }), fText(s, "windowTo", "Hasta", { type: "time" }))),
        h("div", { class: "block" }, fSelect(t, "priority", "Prioridad en la cola", [[1, "Alta"], [0, "Normal"], [-1, "Baja"]], null, true)),
      ];
    }
    case "dinamica":
      return [
        h("h2", null, "Dinámica"), h("p", { class: "muted" }, "Cuántas copias guardar y cada cuánto hacer un completo."),
        h("div", { class: "inline-fields" },
          fNum(t, "fullCopiesToKeep", "Respaldos completos a conservar (0 = todos)"),
          t.type !== "completo" ? fNum(t, "fullEvery", "Hacer un completo cada (0 = nunca)") : null,
          t.type === "diferencial" ? fNum(t, "diffCopiesToKeep", "Diferenciales a conservar (0 = todos)") : null),
        t.type !== "completo" ? h("div", { class: "block" }, fSelect(t, "fixedFullWeekday", "Día fijo para el completo", [[-1, "Ninguno"], ...[1, 2, 3, 4, 5, 6, 0].map((d) => [d, DIAS_LARGOS[d]])], null, true)) : null,
        h("p", { class: "muted small" }, "Al borrar un completo viejo también se borran los incrementales o diferenciales que dependen de él. Los respaldos fijados nunca se borran."),
        !t.separated ? h("div", { class: "note-box" }, "Esta tarea pisa siempre la misma copia (General → crear un respaldo nuevo cada vez está desmarcado), así que estas opciones no aplican.") : null,
      ];
    case "compresion": {
      const enc = h("div", { class: "inline-fields" }, fCheck(t, "encrypt", "Cifrar con contraseña", t.compression === "ninguna" ? "Sin compresión, cada archivo se guarda cifrado como .rsg (se abre con Herramientas → Descifrar)." : "AES-256: se abre con 7-Zip, WinRAR o Resguardo.", redraw),
        t.encrypt ? fText(t, "password", "Contraseña", { type: "password", autocomplete: "new-password" }) : null);
      return [
        h("h2", null, "Compresión"), h("p", { class: "muted" }, "El audio casi no se achica al comprimirlo; sirve más para agrupar y cifrar."),
        fSelect(t, "compression", "Cómo guardar", [["ninguna", "Sin comprimir (una copia normal de las carpetas)"], ["individual", "Comprimir cada archivo por separado"], ["separado", "Un archivo comprimido por cada origen"], ["global", "Un solo archivo comprimido con todo"]], redraw),
        t.compression !== "ninguna" ? h("div", { class: "inline-fields" },
          fSelect(t, "format", "Formato", [["zip", "ZIP"], ["7z", "7z"]]),
          h("label", { class: "field" }, h("span", null, "Nivel de compresión: " + t.level), h("input", { type: "range", min: 0, max: 9, value: t.level, oninput: (e) => { t.level = +e.target.value; e.target.previousSibling.textContent = "Nivel de compresión: " + t.level; } })),
          fNum(t, "splitMB", "Partir en archivos de (MB, 0 = no partir)")) : null,
        h("div", { class: "block" }, enc),
        t.compression !== "ninguna" ? fText(t, "comment", "Comentario dentro del archivo") : null,
      ];
    }
    case "filtros": {
      const inc = h("div"), exc = h("div");
      const di = () => inc.replaceChildren(filterList(t.include = t.include || [], di));
      const de = () => exc.replaceChildren(filterList(t.exclude = t.exclude || [], de));
      di(); de();
      t.dateFilter = t.dateFilter || {}; t.sizeFilter = t.sizeFilter || {};
      return [
        h("h2", null, "Filtros"), h("p", { class: "muted" }, "Qué entra y qué no. Las máscaras aceptan comodines y varias separadas por coma, por ejemplo: *.wav, *.als"),
        fCheck(P, "smartExclusions", "Saltear cachés de DAW (.asd, WaveCache.wfm, imágenes de onda, picos)"),
        h("h3", { style: { margin: "14px 0 8px" } }, "Incluir sólo"), inc,
        h("h3", { style: { margin: "14px 0 8px" } }, "Excluir"), exc,
        h("div", { class: "block" }, h("h3", null, "Por fecha"),
          h("div", { class: "inline-fields" },
            fSelect(t.dateFilter, "mode", null, [["", "Sin filtro de fecha"], ["incluir", "Incluir sólo los"], ["excluir", "Excluir los"]]),
            fSelect(t.dateFilter, "op", null, [["mas_nuevos", "más nuevos que"], ["mas_viejos", "más viejos que"]]),
            fText(t.dateFilter, "date", null, { placeholder: "AAAA-MM-DD, hoy, ayer o -30" }))),
        h("div", { class: "block" }, h("h3", null, "Por tamaño"),
          h("div", { class: "inline-fields" },
            fSelect(t.sizeFilter, "mode", null, [["", "Sin filtro de tamaño"], ["incluir", "Incluir sólo los"], ["excluir", "Excluir los"]]),
            fSelect(t.sizeFilter, "op", null, [["mayores", "mayores a"], ["menores", "menores a"]]),
            h("span", { class: "row" }, fNum(t.sizeFilter, "mb", null, { step: "any" }), "MB"))),
      ];
    }
    case "eventos": {
      const pre = h("div"), post = h("div");
      const dp = () => pre.replaceChildren(eventList(t.preEvents = t.preEvents || [], dp));
      const dq = () => post.replaceChildren(eventList(t.postEvents = t.postEvents || [], dq));
      dp(); dq();
      return [
        h("h2", null, "Eventos"), h("p", { class: "muted" }, "Acciones antes y después del respaldo. En los parámetros podés usar {origen}, {destino} y {tarea}."),
        h("h3", { style: { margin: "14px 0 8px" } }, "Antes del respaldo"), pre,
        h("h3", { style: { margin: "14px 0 8px" } }, "Después del respaldo"), post,
        h("div", { class: "block" },
          fCheck(t, "cancelIfPreFails", "Cancelar la tarea si falla un evento previo"),
          fCheck(t, "skipPostIfPreFailed", "No ejecutar los eventos posteriores si falló uno previo")),
      ];
    }
    case "avanzado":
      t.runAs = t.runAs || {};
      return [
        h("h2", null, "Avanzado"),
        t.type !== "proyectos" ? h("div", { class: "block" },
          isWin() ? fCheck(t, "clearArchiveAttr", "Limpiar el atributo de archivo después de copiar") : null,
          fCheck(t, "mirror", "Espejo: quitar del destino lo que se borró en el origen", "Sólo cuando cada respaldo pisa al anterior. Lo quitado se aparta en una carpeta “_Espejo seguro” (se cambia en Opciones).") ,
          fCheck(t, "absolutePaths", "Usar rutas completas (C\\Usuarios\\…) dentro del respaldo"),
          fCheck(t, "topParentDir", "Guardar todos los respaldos de la tarea dentro de una carpeta con su nombre"),
          fCheck(t, "typeInName", "Agregar el tipo (Completo, Incremental…) al nombre del respaldo"),
          fCheck(t, "ignoreEmptyDirs", "Ignorar carpetas vacías")) : null,
        isWin() ? h("div", { class: "block" }, h("h3", null, "Ejecutar como otro usuario"),
          h("p", { class: "muted small" }, "Útil para destinos en otra computadora de la red que piden usuario y contraseña."),
          fCheck(t.runAs, "enabled", "Usar otras credenciales", null, redraw),
          t.runAs.enabled ? h("div", { class: "inline-fields" }, fText(t.runAs, "user", "Usuario"), fText(t.runAs, "domain", "Dominio (. = local)"), fText(t.runAs, "password", "Contraseña", { type: "password" })) : null,
          t.runAs.enabled ? fCheck(t.runAs, "cancelIfFails", "Cancelar si no se puede iniciar sesión") : null) : h("p", { class: "muted" }, "No hay más opciones para este sistema."),
      ];
  }
  return [];
}

function locList(arr, redraw, opts) {
  const rows = arr.map((l, i) => h("div", { class: "it" },
    icon(l.kind === "ftp" || l.kind === "sftp" ? "drive" : l.forceFile ? "file" : "folder"),
    h("span", { class: "txt", title: locText(l) }, locText(l)),
    l.kind === "ftp" || l.kind === "sftp" ? h("button", { class: "btn small ghost", onclick: async () => { const r = await remoteDialog(l); if (r) { arr[i] = r; redraw(); } } }, "Editar") : null,
    h("button", { class: "btn small ghost", onclick: () => testLoc(l) }, "Probar"),
    h("button", { class: "btn small ghost danger", onclick: () => { arr.splice(i, 1); redraw(); }, "aria-label": "Quitar" }, "Quitar")));
  return h("div", null,
    h("div", { class: "items" }, rows.length ? rows : h("div", { class: "none" }, opts.dest ? "Sin destinos." : "Sin carpetas.")),
    h("div", { class: "row wrap" },
      h("button", { class: "btn small", onclick: async () => { const p = await pickPath({ title: opts.dest ? "Elegí la carpeta de destino" : "Elegí una carpeta" }); if (p) { arr.push({ kind: "local", path: p }); redraw(); } } }, icon("folder"), "Carpeta…"),
      opts.files ? h("button", { class: "btn small", onclick: async () => { const p = await pickPath({ title: "Elegí un archivo", files: true }); if (p) { arr.push({ kind: "local", path: p, forceFile: true }); redraw(); } } }, icon("file"), "Archivo…") : null,
      h("button", { class: "btn small", onclick: async () => { const p = await askText("Escribir una ruta", "Ruta (también de red, como \\\\Servidor\\Carpeta)", ""); if (p) { arr.push({ kind: "local", path: p }); redraw(); } } }, "Escribir ruta…"),
      opts.remote !== false ? h("button", { class: "btn small", onclick: async () => { const r = await remoteDialog({ kind: "ftp", port: 0, path: "/" }); if (r) { arr.push(r); redraw(); } } }, icon("drive"), "FTP / SFTP…") : null));
}

async function testLoc(l) {
  toast("Probando " + locText(l) + "…");
  await attempt(async () => { const r = await api("fs/test", l); toast(r.msg); });
}

async function remoteDialog(l) {
  const o = clone(l);
  const box = h("div");
  const draw = () => box.replaceChildren(
    h("div", { class: "inline-fields", style: { marginBottom: "12px" } },
      fSelect(o, "kind", "Tipo", [["ftp", "FTP / FTPS"], ["sftp", "SFTP (SSH)"]], draw),
      fText(o, "host", "Servidor"), fNum(o, "port", "Puerto (0 = el habitual)")),
    h("div", { class: "inline-fields", style: { marginBottom: "12px" } }, fText(o, "user", "Usuario"), fText(o, "pass", o.kind === "sftp" ? "Contraseña o frase de la clave" : "Contraseña", { type: "password" })),
    fText(o, "path", "Carpeta en el servidor"),
    o.kind === "ftp" ? h("div", { class: "inline-fields" }, fSelect(o, "tls", "Cifrado", [["", "Sin cifrar"], ["explicito", "FTPS explícito (AUTH TLS)"], ["implicito", "FTPS implícito"]]), fCheck(o, "ignoreCert", "Aceptar certificados no verificados")) : null,
    o.kind === "sftp" ? h("label", { class: "field" }, h("span", null, "Clave privada (opcional)"), pathPick(Object.assign(h("input", { type: "text", value: o.keyFile || "", oninput: (e) => (o.keyFile = e.target.value) })), { files: true, title: "Clave privada" })) : null,
    h("button", { class: "btn small", onclick: () => testLoc(o) }, "Probar la conexión"));
  draw();
  const ok = await confirmSheet("Servidor remoto", box, "Listo");
  return ok ? o : null;
}

function filterList(arr, redraw) {
  const names = { mascara: "Máscara", archivo: "Archivo", carpeta: "Carpeta", regex: "Expresión regular" };
  const rows = arr.map((f, i) => h("div", { class: "it" }, h("span", { class: "tag" }, names[f.kind] || f.kind), h("span", { class: "txt" }, f.value),
    h("button", { class: "btn small ghost danger", onclick: () => { arr.splice(i, 1); redraw(); } }, "Quitar")));
  const kind = h("select", null, Object.entries(names).map(([k, v]) => h("option", { value: k }, v)));
  const val = h("input", { type: "text", placeholder: "*.wav, *Bounce*", style: { flex: 1 } });
  return h("div", null, h("div", { class: "items" }, rows.length ? rows : h("div", { class: "none" }, "Nada.")),
    h("div", { class: "row" }, kind, val,
      h("button", { class: "btn small", onclick: async () => { if (kind.value === "archivo" || kind.value === "carpeta") { const p = await pickPath({ files: kind.value === "archivo", title: "Elegir" }); if (p) val.value = p; } } }, "Buscar…"),
      h("button", { class: "btn small", onclick: () => { if (val.value.trim()) { arr.push({ kind: kind.value, value: val.value.trim() }); redraw(); } } }, "Agregar")));
}

const EVENT_NAMES = { ejecutar: "Ejecutar un programa", ejecutar_esperar: "Ejecutar y esperar a que termine", cerrar_programa: "Cerrar un programa", iniciar_servicio: "Iniciar un servicio", detener_servicio: "Detener un servicio", pausa: "Esperar unos segundos", apagar: "Apagar el equipo", reiniciar: "Reiniciar el equipo", hibernar: "Hibernar", suspender: "Suspender", cerrar_sesion: "Cerrar la sesión" };
function eventList(arr, redraw) {
  const rows = arr.map((ev, i) => h("div", { class: "it" }, h("span", { class: "txt" }, EVENT_NAMES[ev.kind] + (ev.target ? ": " + ev.target : "") + (ev.args ? " " + ev.args : "") + (ev.kind === "pausa" ? ": " + ev.seconds + " s" : "")),
    i > 0 ? h("button", { class: "btn small ghost", onclick: () => { [arr[i - 1], arr[i]] = [arr[i], arr[i - 1]]; redraw(); } }, "Subir") : null,
    h("button", { class: "btn small ghost danger", onclick: () => { arr.splice(i, 1); redraw(); } }, "Quitar")));
  const ev = { kind: "cerrar_programa", target: "", args: "", seconds: 10 };
  const form = h("div");
  const drawForm = () => form.replaceChildren(h("div", { class: "inline-fields" },
    fSelect(ev, "kind", null, Object.entries(EVENT_NAMES), drawForm),
    ["ejecutar", "ejecutar_esperar"].includes(ev.kind) ? h("span", { class: "pathpick" }, fText(ev, "target", null, { placeholder: "Programa" }), h("button", { class: "btn small", onclick: async () => { const p = await pickPath({ files: true, title: "Programa" }); if (p) { ev.target = p; drawForm(); } } }, "Buscar…")) : null,
    ["ejecutar", "ejecutar_esperar"].includes(ev.kind) ? fText(ev, "args", null, { placeholder: "Parámetros" }) : null,
    ev.kind === "cerrar_programa" ? fText(ev, "target", null, { placeholder: isWin() ? "Ableton Live 12 Suite.exe o parte del título" : "Nombre de la app" }) : null,
    ev.kind.endsWith("_servicio") ? fText(ev, "target", null, { placeholder: "Nombre del servicio" }) : null,
    ev.kind === "pausa" ? fNum(ev, "seconds", null) : null,
    h("button", { class: "btn small", onclick: () => { arr.push({ ...ev }); redraw(); } }, "Agregar")));
  drawForm();
  return h("div", null, h("div", { class: "items" }, rows.length ? rows : h("div", { class: "none" }, "Ninguno.")), form,
    h("p", { class: "muted small" }, "Cerrar un programa equivale a tocar la X: si tiene cambios sin guardar, el programa los va a preguntar y Resguardo no lo fuerza."));
}

// ---------------------------------------------------------------------------
// Registro, herramientas, opciones
// ---------------------------------------------------------------------------
function logRow(l) {
  const sep = l.m.startsWith("──");
  return h("div", { class: l.l + (sep ? " sep" : "") }, h("time", null, l.t), h("span", null, l.m));
}

function renderRegistro() {
  const v = $("#regview");
  v.replaceChildren(
    h("div", { class: "section-title", style: { marginTop: 0 } }, h("h1", null, "Registro"), h("span", { class: "grow" }),
      h("button", { class: "btn", onclick: async () => { const t = await askText("Agregar una nota al registro", "Nota", ""); if (t) api("log/add", { text: t }); } }, "Agregar nota"),
      h("button", { class: "btn", onclick: () => { api("log/clear", {}); S.log = []; $("#log").replaceChildren(); } }, "Limpiar la vista"),
      h("button", { class: "btn", onclick: () => api("log/open", {}) }, "Abrir la carpeta de registros"),
      h("button", { class: "btn danger", onclick: async () => { if (await confirmSheet("Borrar los registros", h("p", null, "Se borran todos los archivos de registro. No se puede deshacer."), "Borrar", true)) { await api("log/deleteall", {}); S.log = []; renderRegistro(); } } }, "Borrar registros")),
    h("div", { id: "queue" }),
    h("div", { class: "log", id: "log" }, S.log.map(logRow)));
  renderQueue();
  const lg = $("#log");
  lg.scrollTop = lg.scrollHeight;
}

function renderLogLines(lines) {
  const lg = $("#log");
  if (!lg) return;
  const atBottom = lg.scrollHeight - lg.scrollTop - lg.clientHeight < 40;
  lines.forEach((l) => lg.append(logRow(l)));
  while (lg.childElementCount > 3000) lg.firstChild.remove();
  if (atBottom) lg.scrollTop = lg.scrollHeight;
}

let queueSig = "";
function renderQueue() {
  const q = $("#queue");
  if (!q) return;
  const st = S.data.status || {};
  const sig = JSON.stringify([st.running && st.running.id, (st.queue || []).map((j) => j.id)]);
  if (sig === queueSig && q.childElementCount) return;
  queueSig = sig;
  if (!st.running && !(st.queue || []).length) { q.replaceChildren(); return; }
  q.replaceChildren(h("div", { class: "items", style: { marginBottom: "14px" } },
    st.running ? h("div", { class: "it" }, h("span", { class: "led work" }), h("span", { class: "txt" }, "Ahora: " + st.running.taskName + (st.running.project ? " → " + st.running.project.split("/").pop() : "") + " (" + st.running.reason + ")")) : null,
    (st.queue || []).map((j) => h("div", { class: "it" }, h("span", { class: "led dirty" }), h("span", { class: "txt" }, "En cola: " + j.taskName + (j.project ? " → " + j.project.split("/").pop() : "") + " (" + j.reason + ")"),
      h("button", { class: "btn small ghost", onclick: () => api("queue/remove", { id: j.id }).then(poll) }, "Sacar de la cola")))));
}

function pathPick(input, opts) {
  return h("span", { class: "pathpick" }, input, h("button", { class: "btn", type: "button", onclick: async () => { const p = await pickPath({ ...opts, start: input.value }); if (p) { input.value = p; input.dispatchEvent(new Event("input")); } } }, "Elegir…"));
}

function renderTools() {
  const v = $("#toolsview");
  const tool = (title, text, ...kids) => h("div", { class: "tool" }, h("div", null, h("h2", null, title), h("p", { class: "muted", style: { marginTop: "4px" } }, text)), ...kids);
  const exA = h("input", { type: "text", placeholder: "Archivo .zip, .7z o la primera parte (.001)" });
  const exD = h("input", { type: "text", placeholder: "Carpeta de destino" });
  const exP = h("input", { type: "password", placeholder: "Contraseña (si tiene)" });
  const deS = h("input", { type: "text", placeholder: "Archivo .rsg o carpeta con archivos .rsg" });
  const deD = h("input", { type: "text", placeholder: "Carpeta de destino" });
  const deP = h("input", { type: "password", placeholder: "Contraseña" });
  const delP = h("input", { type: "text", placeholder: "Carpeta a borrar" });
  const notes = h("textarea", { rows: 6, value: S.data.settings.notes || "" });
  const imp = h("input", { type: "file", accept: ".json", class: "hidden", onchange: async (e) => {
    const f = e.target.files[0]; if (!f) return;
    try { const tasks = JSON.parse(await f.text()); await attempt(() => api("import", tasks), "Tareas importadas"); await reload(); } catch { toast("El archivo no es una lista de tareas de Resguardo", true); }
  } });
  v.replaceChildren(h("h1", null, "Herramientas"),
    tool("Descomprimir", "Abre los archivos ZIP y 7z que crea Resguardo, también los partidos en varias partes y los cifrados.",
      pathPick(exA, { files: true, title: "Archivo comprimido" }), pathPick(exD, { title: "Destino" }), exP,
      h("div", null, h("button", { class: "btn primary", onclick: () => attempt(() => api("tools/extract", { archive: exA.value, dst: exD.value, password: exP.value }), "Descomprimiendo… mirá el avance abajo") }, "Descomprimir"))),
    tool("Descifrar", "Recupera los archivos .rsg (copias cifradas sin comprimir).",
      pathPick(deS, { files: true, title: "Archivo o carpeta" }), pathPick(deD, { title: "Destino" }), deP,
      h("div", null, h("button", { class: "btn primary", onclick: () => attempt(() => api("tools/decrypt", { src: deS.value, dst: deD.value, password: deP.value }), "Descifrando…") }, "Descifrar"))),
    tool("Borrar carpetas con rutas largas", "Borra carpetas que el Explorador no puede borrar porque sus rutas son demasiado largas.",
      pathPick(delP, { title: "Carpeta a borrar" }),
      h("div", null, h("button", { class: "btn danger", onclick: async () => { if (delP.value && await confirmSheet("Borrar " + delP.value, h("p", null, "Se borra la carpeta con todo su contenido. No se puede deshacer."), "Borrar", true)) attempt(() => api("tools/delete", { path: delP.value }), "Borrando…"); } }, "Borrar"))),
    tool("Exportar e importar tareas", "Para pasar tus tareas a otra computadora o guardarlas aparte.",
      h("div", { class: "row" }, h("a", { class: "btn", href: "/api/export", download: "resguardo-tareas.json" }, "Exportar la lista actual"), h("button", { class: "btn", onclick: () => imp.click() }, "Importar…"), imp)),
    tool("Optimizar el historial", "Quita del historial los respaldos cuyos archivos ya no existen en el destino.",
      h("div", null, h("button", { class: "btn", onclick: async () => { const r = await attempt(() => api("history/optimize", {})); if (r) { toast(r.removed + " registros quitados"); S.history = {}; } } }, "Optimizar"))),
    tool("Notas", "Un lugar para anotar lo que quieras: discos, claves de recuperación, recordatorios.",
      notes, h("div", null, h("button", { class: "btn", onclick: async () => { const s = clone(S.data.settings); s.notes = notes.value; await attempt(() => api("settings", s), "Notas guardadas"); S.data.settings.notes = notes.value; } }, "Guardar notas"))));
}

function renderOptions() {
  const v = $("#optview");
  const s = clone(S.data.settings);
  const tabs = [["general", "General"], ["musica", "Música"], ["registro", "Registro"], ["correo", "Correo"], ["comprimidos", "Archivos comprimidos"], ["motor", "Motor"], ["seguridad", "Seguridad"], ["avanzado", "Avanzado"]];
  const side = h("div", { class: "sidetabs", role: "tablist" });
  const pane = h("div", { class: "pane" });
  const draw = () => {
    side.replaceChildren(...tabs.map(([k, l]) => h("button", { role: "tab", "aria-selected": S.optTab === k ? "true" : "false", onclick: () => { S.optTab = k; draw(); } }, l)));
    pane.replaceChildren(...optionsPane(s, S.optTab, draw),
      h("div", { class: "row", style: { marginTop: "18px" } }, h("button", { class: "btn primary", onclick: async () => {
        const r = await attempt(() => api("settings", s), "Opciones guardadas");
        if (r) { S.data.settings = { ...s, uiPasswordHash: S.data.settings.uiPasswordHash }; applyTheme(); if (r.restart) toast("El cambio de puerto o de acceso remoto se aplica al reiniciar Resguardo."); }
      } }, "Guardar opciones")));
  };
  draw();
  v.replaceChildren(h("h1", { style: { marginBottom: "18px" } }, "Opciones"), h("div", { class: "split" }, side, pane));
}

function updateText(u) {
  const when = u.checked && !ZERO(u.checked) ? " (revisado " + fmtDate(u.checked) + ")" : "";
  switch (u.state) {
    case "buscando": return "Buscando…";
    case "al_dia": return "Tenés la última versión" + when + ".";
    case "disponible": return "Hay una versión nueva: " + u.latest + when + ".";
    case "bajando": return "Bajando la versión " + u.latest + (u.total ? " (" + Math.round((u.done / u.total) * 100) + "%)" : "") + "…";
    case "lista": return "La versión " + u.latest + " está bajada y lista para instalar.";
    case "esperando": return "La versión " + u.latest + " se instala cuando " + (u.waitingFor || "").replace(/^que /, "") + ".";
    case "instalando": return "Instalando la versión " + u.latest + "…";
    case "error": return "No se pudo revisar: " + u.error + when + ".";
  }
  return "Todavía no se revisó.";
}

function updateBlock(s) {
  const u = S.data.update || {};
  const status = h("p", { class: "muted", id: "upd-status" }, updateText(u));
  const newer = ["disponible", "lista", "esperando"].includes(u.state);
  return h("div", { class: "block" }, h("h3", null, "Actualizaciones"),
    fCheck(s, "updateAuto", "Buscar versiones nuevas solo", "Revisa al abrir Resguardo y cada 6 horas."),
    fCheck(s, "updateInstall", "Bajarlas e instalarlas solas", "Se instalan cuando no hay un respaldo en curso ni un DAW abierto. Resguardo se reinicia en unos segundos y vuelve solo."),
    !u.canSelf && u.why ? h("p", { class: "note-box" }, u.why) : null,
    status,
    newer && (u.notes || []).length ? h("ul", { class: "notes" }, u.notes.map((x) => h("li", null, x))) : null,
    h("div", { class: "row wrap" },
      h("button", { class: "btn", onclick: async () => {
        status.textContent = "Buscando…";
        await api("update/check", {});
        for (let i = 0; i < 30; i++) {
          await new Promise((r) => setTimeout(r, 500));
          const r = await api("status?log=999999999");
          S.data.update = r.update;
          if (r.update.state !== "buscando") break;
        }
        status.textContent = updateText(S.data.update);
        if (["disponible", "lista", "esperando"].includes(S.data.update.state)) renderOptions();
      } }, "Buscar ahora"),
      newer && u.canSelf ? h("button", { class: "btn primary", onclick: installNow }, "Instalar la versión " + u.latest) : null,
      newer && !u.canSelf && u.download ? h("a", { class: "btn", href: u.download }, "Bajar la versión " + u.latest) : null));
}

function optionsPane(s, tab, redraw) {
  switch (tab) {
    case "general":
      return [h("h2", null, "General"), h("p", { class: "muted" }, "Resguardo " + S.data.version + " · datos guardados en " + S.data.dataDir),
        updateBlock(s),
        fCheck(s, "autostart", "Abrir Resguardo al iniciar sesión", "Arranca en segundo plano para que los respaldos programados y al guardar funcionen siempre."),
        fCheck(s, "runAllAtStartup", "Ejecutar todas las tareas al iniciar"),
        fCheck(s, "runMissed", "Ejecutar los respaldos que se perdieron con la computadora apagada"),
        fSelect(s, "theme", "Tema", [["auto", "Según el sistema"], ["oscuro", "Oscuro"], ["claro", "Claro"]]),
        h("div", { class: "block" }, fCheck(s, "playSound", "Sonar al terminar un respaldo"),
          s.playSound ? h("label", { class: "field" }, h("span", null, "Sonido (archivo .wav, opcional)"), pathPick(fText(s, "soundFile"), { files: true, title: "Sonido" })) : null),
        h("div", { class: "block" }, h("label", { class: "field" }, h("span", null, "Carpeta temporal (vacío = la de Resguardo)"), pathPick(fText(s, "tempDir"), { title: "Carpeta temporal" }))),
        h("div", { class: "block" }, h("button", { class: "btn danger", onclick: async () => { if (await confirmSheet("Salir de Resguardo", h("p", null, "Mientras esté cerrado no se hacen respaldos programados ni al guardar."), "Salir", true)) { await api("quit", {}); document.body.replaceChildren(h("div", { class: "login" }, h("p", null, "Resguardo se cerró."))); } } }, "Salir de Resguardo"))];
    case "musica": {
      const folders = h("div");
      s.sampleSearchFolders = s.sampleSearchFolders || [];
      const drawF = () => folders.replaceChildren(h("div", { class: "items" }, s.sampleSearchFolders.length ? s.sampleSearchFolders.map((f, i) => h("div", { class: "it" }, icon("folder"), h("span", { class: "txt" }, f), h("button", { class: "btn small ghost danger", onclick: () => { s.sampleSearchFolders.splice(i, 1); drawF(); } }, "Quitar"))) : h("div", { class: "none" }, "Ninguna.")),
        h("button", { class: "btn small", onclick: async () => { const p = await pickPath({ title: "Carpeta de samples" }); if (p) { s.sampleSearchFolders.push(p); drawF(); } } }, icon("folder"), "Agregar carpeta…"));
      drawF();
      return [h("h2", null, "Música"), h("p", { class: "muted" }, "Cómo se comporta Resguardo mientras trabajás."),
        fCheck(s, "dawWatch", "Vigilar los DAW abiertos y los proyectos guardados", "Necesario para los respaldos al guardar y al cerrar el programa."),
        h("div", { class: "inline-fields", style: { margin: "10px 0" } },
          fNum(s, "saveQuietSeconds", "Esperar después de guardar (segundos)"),
          fNum(s, "closeDelaySeconds", "Esperar después de cerrar el DAW (segundos)"),
          fNum(s, "rescanMinutes", "Buscar proyectos nuevos cada (minutos)")),
        h("div", { class: "block" }, fCheck(s, "throttleWhileDAW", "Copiar más despacio mientras hay un DAW abierto", "Para no competir con el disco mientras grabás."),
          s.throttleWhileDAW ? fNum(s, "throttleMBps", "Velocidad máxima con el DAW abierto (MB/s)") : null),
        h("div", { class: "block" }, h("h3", null, "Carpetas donde buscar audio"),
          h("p", { class: "muted small" }, "Pro Tools y Cubase guardan sólo el nombre de algunos archivos. Si el audio que usan está fuera de la carpeta del proyecto, Resguardo lo busca en estas carpetas (por ejemplo, tu librería de samples)."), folders)];
    }
    case "registro":
      return [h("h2", null, "Registro"),
        fCheck(s, "logPerDay", "Un archivo de registro por día"),
        fCheck(s, "logDetailed", "Registro detallado"),
        fCheck(s, "logIndividualFiles", "Anotar cada archivo copiado", "Hace los registros mucho más largos."),
        fCheck(s, "clearLogOnStart", "Limpiar la vista del registro al empezar cada respaldo"),
        fNum(s, "deleteLogsDays", "Borrar registros de más de (días, 0 = nunca)"),
        fNum(s, "historyItems", "Respaldos a mostrar en el historial de cada tarea")];
    case "correo": {
      const m = s.smtp;
      return [h("h2", null, "Correo"), h("p", { class: "muted" }, "Recibí el registro por correo, por ejemplo sólo cuando algo falla."),
        fCheck(s, "mailEnabled", "Enviar el registro por correo", null, redraw),
        s.mailEnabled ? h("div", null,
          h("div", { class: "inline-fields", style: { margin: "10px 0" } },
            fSelect(s, "mailWhen", "Cuándo", [["tarea", "Al terminar cada tarea"], ["respaldo", "Al vaciarse la cola de respaldos"], ["diario", "Una vez por día"]], redraw),
            s.mailWhen === "diario" ? fText(s, "mailTime", "Hora", { type: "time" }) : null),
          fCheck(s, "mailOnlyErrors", "Sólo si hubo errores"),
          fCheck(s, "mailAttach", "Mandar el registro como adjunto (si no, va en el texto)"),
          fCheck(s, "mailDeleteIfSent", "Borrar el registro después de enviarlo"),
          h("div", { class: "block" }, h("h3", null, "Servidor de correo"),
            h("div", { class: "inline-fields", style: { marginBottom: "12px" } }, fText(m, "host", "Servidor SMTP", { placeholder: "smtp.gmail.com" }), fNum(m, "port", "Puerto"),
              fSelect(m, "security", "Seguridad", [["starttls", "STARTTLS"], ["ssl", "SSL/TLS"], ["ninguna", "Ninguna"]])),
            fCheck(m, "auth", "El servidor pide usuario y contraseña", null, redraw),
            m.auth ? h("div", { class: "inline-fields", style: { marginBottom: "12px" } }, fText(m, "user", "Usuario"), fText(m, "pass", "Contraseña", { type: "password" })) : null,
            h("div", { class: "inline-fields", style: { marginBottom: "12px" } }, fText(m, "from", "Remitente"), fText(m, "fromName", "Nombre del remitente")),
            h("label", { class: "field" }, h("span", null, "Destinatarios (separados por coma)"), h("input", { type: "text", value: (m.to || []).join(", "), oninput: (e) => (m.to = e.target.value.split(",").map((x) => x.trim()).filter(Boolean)) })),
            fText(m, "subject", "Asunto"),
            h("label", { class: "field" }, h("span", null, "Texto"), h("textarea", { rows: 3, value: m.body || "", oninput: (e) => (m.body = e.target.value) })),
            fCheck(m, "ignoreCert", "Aceptar certificados no verificados"),
            fNum(m, "timeoutMs", "Tiempo de espera (ms)"),
            h("p", { class: "muted small" }, "Con Gmail hace falta una contraseña de aplicación (no la de tu cuenta)."),
            h("button", { class: "btn", onclick: () => attempt(() => api("mail/test", m), "Correo de prueba enviado") }, "Enviar un correo de prueba"))) : null];
    }
    case "comprimidos":
      return [h("h2", null, "Archivos comprimidos"),
        fSelect(s, "zipEncryption", "Cifrado de los ZIP", [["aes256", "AES-256 (recomendado)"], ["aes128", "AES-128"], ["zipcrypto", "ZipCrypto (débil, para programas viejos)"]]),
        h("label", { class: "field" }, h("span", null, "Extensiones que se guardan sin comprimir"), fText(s, "storedExtensions")),
        h("p", { class: "muted small" }, "El audio ya comprimido o que no se achica (wav, mp3, flac…) se guarda tal cual: es mucho más rápido."),
        fCheck(s, "testArchives", "Probar cada archivo comprimido después de crearlo"),
        h("p", { class: "muted small" }, "ZIP64 y nombres con acentos se usan siempre que hace falta.")];
    case "motor":
      return [h("h2", null, "Motor"),
        fCheck(s, "firstBackupFull", "El primer respaldo de cada tarea es completo"),
        fCheck(s, "noDuplicateQueue", "No poner dos veces la misma tarea en la cola"),
        fCheck(s, "preventSleep", "No dejar que el equipo se suspenda durante un respaldo"),
        fCheck(s, "lowPriority", "Copiar con prioridad baja", "La computadora sigue fluida mientras se respalda."),
        fCheck(s, "noRunOnBattery", "No ejecutar tareas programadas con la notebook a batería"),
        fCheck(s, "cancelIfVSSFails", "Cancelar si no se puede crear la instantánea de volumen (VSS)"),
        h("div", { class: "block" },
          fCheck(s, "copyAttributes", "Copiar los atributos de los archivos"),
          isWin() ? fCheck(s, "copyPermissions", "Copiar los permisos NTFS") : null,
          fCheck(s, "ignoreAttrErrors", "No informar como error si no se pueden copiar atributos o permisos"),
          fCheck(s, "ignoreReparsePoints", "No seguir accesos directos de carpetas (enlaces y junctions)"),
          fCheck(s, "saveEmptyBackups", "Guardar en el historial los respaldos sin archivos"),
          fCheck(s, "parkFirstBackup", "Fijar automáticamente el primer respaldo de cada tarea"),
          fCheck(s, "neverIgnoreEmptyFull", "En los completos, conservar siempre las carpetas vacías")),
        h("div", { class: "block" }, fNum(s, "maxSpeedMBps", "Velocidad máxima de copia (MB/s, 0 = sin límite)"))];
    case "seguridad": {
      const old = h("input", { type: "password", placeholder: "Contraseña actual" });
      const nw = h("input", { type: "password", placeholder: "Contraseña nueva (vacía = quitarla)" });
      const has = S.data.settings.uiPasswordHash === "set";
      return [h("h2", null, "Seguridad"),
        h("div", { class: "block" }, h("h3", null, has ? "Cambiar la contraseña" : "Poner una contraseña"),
          h("div", { class: "inline-fields" }, has ? old : null, nw,
            h("button", { class: "btn", onclick: async () => { const r = await attempt(() => api("settings/password", { old: old.value, new: nw.value }), "Contraseña actualizada"); if (r) await reload(); } }, "Guardar contraseña"))),
        fCheck(s, "protectUI", "Pedir la contraseña para abrir esta ventana", has ? null : "Primero poné una contraseña."),
        fCheck(s, "remoteAccess", "Permitir el acceso desde otras computadoras de la red", "Se entra desde el navegador con http://<esta computadora>:" + s.port + " y la contraseña. La conexión no va cifrada: usalo sólo en tu red."),
        fNum(s, "port", "Puerto")];
    }
    case "avanzado":
      return [h("h2", null, "Avanzado"),
        h("label", { class: "field" }, h("span", null, "Formato de la fecha en los nombres de los respaldos"), fText(s, "timestampFormat")),
        h("p", { class: "muted small" }, "Códigos: yyyy año, MM mes, MMM mes abreviado, dd día, dddd día de la semana, HH hora, mm minutos, ss segundos."),
        fNum(s, "processExitTimeout", "Esperar a que un programa se cierre (segundos)"),
        fCheck(s, "ftpReconnect", "Reconectar y reintentar si se corta un FTP o SFTP"),
        h("div", { class: "block" }, h("h3", null, "Espejo"),
          fCheck(s, "safeMirror", "En vez de borrar, apartar lo que el espejo quita", "Queda en “_Espejo seguro” al lado del respaldo, o en la carpeta que elijas."),
          s.safeMirror ? h("label", { class: "field" }, h("span", null, "Carpeta del espejo seguro (opcional)"), pathPick(fText(s, "safeMirrorDir"), { title: "Espejo seguro" })) : null)];
  }
  return [];
}

// ---------------------------------------------------------------------------
// Hojas modales
// ---------------------------------------------------------------------------
function sheet({ title, body, foot, size, flush, onClose }) {
  const prev = document.activeElement;
  const close = onClose || (() => { $("#layer").replaceChildren(); prev && prev.focus && prev.focus(); });
  const el = h("div", { class: "scrim", onmousedown: (e) => { if (e.target === e.currentTarget) close(); } },
    h("div", { class: "sheet " + (size || ""), role: "dialog", "aria-modal": "true", "aria-label": title },
      h("div", { class: "sheet-head" }, h("h2", { class: "grow" }, title), h("button", { class: "btn ghost small", onclick: close, "aria-label": "Cerrar" }, "Cerrar")),
      h("div", { class: "sheet-body" + (flush ? " flush" : "") }, body),
      foot ? h("div", { class: "sheet-foot" }, foot) : null));
  el.addEventListener("keydown", (e) => { if (e.key === "Escape") close(); });
  $("#layer").replaceChildren(el);
  const f = el.querySelector("input, select, textarea, .sheet-body button");
  if (f) f.focus();
  return close;
}

// Las hojas se apilan: guardamos la anterior para devolverla al cerrar.
function stacked(fn) {
  const layer = $("#layer");
  const saved = [...layer.childNodes];
  return new Promise((resolve) => {
    fn((v) => { layer.replaceChildren(...saved); resolve(v); });
  });
}

function confirmSheet(title, body, okLabel, danger) {
  return stacked((done) => {
    sheet({ title, body, size: "sm", onClose: () => done(false),
      foot: [h("button", { class: "btn", onclick: () => done(false) }, "Cancelar"), h("button", { class: "btn " + (danger ? "danger" : "primary"), onclick: () => done(true) }, okLabel)] });
  });
}

function askText(title, label, value) {
  const i = h("input", { type: "text", value: value || "", style: { width: "100%" } });
  return stacked((done) => {
    sheet({ title, size: "sm", onClose: () => done(null), body: h("label", { class: "field" }, h("span", null, label), i),
      foot: [h("button", { class: "btn", onclick: () => done(null) }, "Cancelar"), h("button", { class: "btn primary", onclick: () => done(i.value.trim() || null) }, "Aceptar")] });
    i.addEventListener("keydown", (e) => { if (e.key === "Enter") done(i.value.trim() || null); });
    i.focus(); i.select();
  });
}

function pickPath({ title, files, start }) {
  return stacked((done) => {
    let cur = start || "";
    let selected = null;
    const pathIn = h("input", { type: "text", style: { flex: 1 }, "aria-label": "Ruta" });
    const list = h("div", { class: "picker-list" });
    const crumbs = h("div", { class: "crumbs" });
    let seq = 0;
    const go = async (p) => {
      const mine = ++seq;
      let r;
      try { r = await api("fs/list?path=" + encodeURIComponent(p) + (files ? "&files=1" : "")); } catch (e) { toast(e.message, true); return; }
      if (mine !== seq) return; // llegó tarde: ya se pidió otra carpeta
      if (r.error && p) { toast("Esa carpeta no existe todavía o no se puede abrir. Si es un destino, elegila igual: se crea en el primer respaldo.", true); return; }
      cur = r.path; selected = null;
      pathIn.value = cur;
      crumbs.replaceChildren(...(r.drives || []).map((d) => h("button", { onclick: () => go(d) }, d)));
      list.replaceChildren(
        r.parent || r.path ? h("button", { onclick: () => go(r.parent || "") }, icon("up"), "Subir un nivel") : null,
        ...(r.items || []).map((it) => h("button", {
          class: it.dir ? "" : "file",
          ondblclick: () => { if (it.dir) go(it.path); else done(it.path); },
          onclick: (e) => {
            if (it.dir) return go(it.path);
            selected = it.path; pathIn.value = it.path;
            list.querySelectorAll(".file").forEach((b) => b.classList.remove("sel"));
            e.currentTarget.classList.add("sel");
          },
        }, icon(it.dir ? (r.path ? "folder" : "drive") : "file"), h("span", { class: "grow" }, it.name), it.dir ? null : h("span", { class: "faint small" }, fmtBytes(it.size)))));
      if (!(r.items || []).length) list.append(h("p", { class: "muted", style: { padding: "12px" } }, files ? "Carpeta vacía." : "No hay subcarpetas."));
    };
    sheet({ title: title || "Elegir", size: "md", onClose: () => done(null),
      body: h("div", null, crumbs, h("div", { class: "row", style: { marginBottom: "8px" } }, pathIn, h("button", { class: "btn", onclick: () => go(pathIn.value) }, "Ir")), list),
      foot: [h("button", { class: "btn", onclick: () => done(null) }, "Cancelar"),
        h("button", { class: "btn primary", onclick: () => done(files ? (selected || pathIn.value.trim() || null) : (pathIn.value.trim() || cur || null)) }, files ? "Elegir el archivo" : "Elegir esta carpeta")] });
    pathIn.addEventListener("keydown", (e) => { if (e.key === "Enter") go(pathIn.value); });
    const prevStart = start && start.trim();
    go(prevStart ? prevStart.replace(/[\\/][^\\/]*\.[^\\/]+$/, "") : (S.data.home || ""));
  });
}

window.addEventListener("keydown", (e) => {
  if (e.key === "F5" && !$("#layer").childElementCount) { e.preventDefault(); reload(); }
});
matchMedia("(prefers-color-scheme: light)").addEventListener("change", () => S.data && applyTheme());
boot();
