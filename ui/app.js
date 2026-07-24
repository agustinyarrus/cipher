'use strict';

// Cipher — logica de la UI. WebView2 no soporta -webkit-app-region, asi que el arrastre y el
// redimensionado de la ventana frameless se piden al host (cipherDrag/cipherResize). El resto es
// visor: muestra el HTML ya resaltado por el server (chroma) en PESTAÑAS — una por archivo, con
// dedup por ruta; el handoff del daemon, el dialogo de abrir (multi-seleccion) y el drag&drop
// abren aca — con gutter de numeros, busqueda, zoom, ajuste de linea, copiar y recarga en vivo.

const $ = (id) => document.getElementById(id);
const body = document.body;
const view = $('view');
const code = $('code');

window.__log = (m) => { if (!window.__CIPHER_DEBUG__) return; try { fetch('/log?m=' + encodeURIComponent(m)); } catch (e) {} };
window.addEventListener('error', (e) => window.__log('ERR ' + e.message + ' @' + (e.filename || '') + ':' + e.lineno));
window.addEventListener('unhandledrejection', (e) => window.__log('REJECT ' + (e.reason && (e.reason.message || e.reason))));

function bridge(name, ...args) {
  try { if (typeof window[name] === 'function') return window[name](...args); }
  catch (e) { /* dev en navegador: sin host */ }
}

// =========================================================================
// Pestañas — cada archivo vive en una pestaña con su documento YA renderizado
// (cambiar es instantaneo y conserva el scroll). Con UNA sola pestaña la barra
// muestra el caption centrado de siempre; la tira aparece recien con dos o mas.
// =========================================================================
const tabsNav = $('tabs');
const tabs = [];      // orden visual
let activeTab = null; // pestaña visible
let tabSeq = 0;

function freshMarks() { return { regions: [], idx: -1, counts: [0, 0, 0] }; }
let marks = freshMarks(); // alias del marks de la pestaña ACTIVA (lo usan focusMark/gotoMark/pill)

const norm = (p) => (p || '').replace(/\//g, '\\').toLowerCase(); // identidad de ruta en Windows
const adoc = () => (activeTab ? activeTab.doc : code);            // raiz de busqueda/copia/seleccion

const TAB_X = '<svg width="10" height="10" viewBox="0 0 10 10"><line x1="1.7" y1="1.7" x2="8.3" y2="8.3"/><line x1="8.3" y1="1.7" x2="1.7" y2="8.3"/></svg>';

function newTab(path) {
  const t = {
    id: ++tabSeq, path: path || null, key: path ? norm(path) : null,
    name: '', j: null, scroll: 0, stale: false, marks: freshMarks(),
    el: document.createElement('div'), doc: document.createElement('div'),
  };
  t.el.className = 'tab'; t.el.setAttribute('role', 'tab');
  t.el.innerHTML = '<span class="tab-dot" aria-hidden="true"></span><span class="tab-name"></span>' +
    '<span class="tab-x" title="Cerrar (Ctrl W)">' + TAB_X + '</span>';
  t.el.addEventListener('click', (e) => { if (e.target.closest('.tab-x')) closeTab(t); else activateTab(t); });
  t.el.addEventListener('auxclick', (e) => { if (e.button === 1) { e.preventDefault(); closeTab(t); } });
  t.el.addEventListener('pointerdown', (e) => { if (e.button === 1) e.preventDefault(); }); // sin autoscroll
  t.doc.className = 'doc'; t.doc.hidden = true;
  tabs.push(t);
  tabsNav.appendChild(t.el);
  code.appendChild(t.doc);
  updateTabsMode();
  syncWatch();
  return t;
}

function setTabLabel(t) {
  t.el.querySelector('.tab-name').textContent = t.name || '';
  t.el.title = t.path || t.name || '';
}

function activateTab(t, opts = {}) {
  const prev = activeTab;
  if (prev && prev !== t) {
    prev.scroll = view.scrollTop; // recordar posicion para volver
    prev.el.classList.remove('active'); prev.el.setAttribute('aria-selected', 'false');
    prev.doc.hidden = true;
  }
  activeTab = t;
  t.el.classList.add('active'); t.el.setAttribute('aria-selected', 'true');
  t.doc.hidden = false;
  marks = t.marks;
  body.classList.add('has-doc'); body.classList.remove('no-doc', 'empty');
  body.classList.toggle('live-on', !!t.path);
  syncChrome(t);
  if (prev !== t) {
    view.scrollTop = t.scroll || 0;
    updateProgress();
    t.el.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    refind(); // busqueda abierta -> re-buscar sobre esta pestaña
  }
  if (t.stale && !opts.noRefresh) liveReload(t); // cambio en disco mientras estaba de fondo
  updateStrip();
}

function closeTab(t) {
  const i = tabs.indexOf(t);
  if (i < 0) return;
  tabs.splice(i, 1);
  t.el.remove(); t.doc.remove();
  if (activeTab === t) {
    activeTab = null;
    const next = tabs[i] || tabs[i - 1];
    if (next) activateTab(next); else clearToEmpty();
  }
  updateTabsMode();
  syncWatch();
}

function cycleTab(dir) {
  if (!activeTab || tabs.length < 2) return;
  activateTab(tabs[(tabs.indexOf(activeTab) + dir + tabs.length) % tabs.length]);
}

function clearToEmpty() {
  marks = freshMarks();
  body.classList.add('no-doc', 'empty');
  body.classList.remove('has-doc', 'live-on');
  clearNotice(); closeFind();
  $('capName').textContent = ''; $('capLang').textContent = '';
  clearStatus(); updateProgress();
}

function updateTabsMode() {
  body.classList.toggle('tabs-on', tabs.length >= 2);
  updateStrip();
}

// fades laterales de la tira cuando desborda
function updateStrip() {
  tabsNav.classList.toggle('fade-l', tabsNav.scrollLeft > 2);
  tabsNav.classList.toggle('fade-r', tabsNav.scrollLeft + tabsNav.clientWidth < tabsNav.scrollWidth - 2);
}
tabsNav.addEventListener('scroll', updateStrip, { passive: true });
tabsNav.addEventListener('wheel', (e) => { // rueda sobre la tira -> scroll horizontal
  if (!e.ctrlKey && e.deltaY) { e.preventDefault(); tabsNav.scrollLeft += e.deltaY; }
}, { passive: false });

// cromo (caption, barra de estado, aviso binario, pill de marcas) segun la pestaña activa
function syncChrome(t) {
  $('capName').textContent = t.name || '';
  $('capLang').textContent = (t.j && !t.j.binary && t.j.lang) || '';
  if (t.j) {
    fillStatus(t.j);
    if (t.j.binary) showNotice('Archivo binario', 'No se puede mostrar como texto'); else clearNotice();
  }
  syncMarksPill();
}
function syncMarksPill() {
  if (marks.regions.length) { updateMarksPill(); return; }
  const pill = $('stMarks'); pill.textContent = ''; pill.classList.remove('on');
}

// =========================================================================
// Apertura / render
// =========================================================================
// Las aperturas van EN COLA: una rafaga (multi-seleccion del dialogo, varios handoffs
// seguidos) crea las pestañas en orden estable, sin carreras entre fetches.
let openChain = Promise.resolve();
const queueOpen = (p) => { openChain = openChain.then(() => openInTab(p)).catch(() => {}); };
window.__cipherOpen = (p) => { (Array.isArray(p) ? p : [p]).forEach(queueOpen); }; // host: pick / Eval

async function openInTab(path) {
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(path));
    const j = await r.json();
    if (!j.ok) { toast(j.error || 'No se pudo abrir'); return; }
    const existing = tabs.find((x) => x.key && x.key === norm(j.path));
    const t = existing || newTab(j.path);
    // reabrir la misma pestaña con la MISMA spec de marcas -> refresco silencioso (conserva el
    // scroll); spec distinta (--hl nuevo o limpiado) -> repinta y salta a la primera zona.
    const silent = !!(existing && existing.j &&
      JSON.stringify(existing.j.hl || []) === JSON.stringify(j.hl || []));
    activateTab(t, { noRefresh: true });
    const keep = view.scrollTop;
    paint(t, j, { silent });
    if (silent) { view.scrollTop = keep; updateProgress(); }
    t.stale = false; t.el.classList.remove('stale');
  } catch (e) { window.__log('open ' + e); toast('Error al abrir'); }
}

// Arrastrar-y-soltar: resalta texto crudo (sin ruta en disco -> sin recarga viva ni dedup).
async function renderRawText(text, name) {
  try {
    const r = await fetch('/render-text?name=' + encodeURIComponent(name || 'snippet.txt'), {
      method: 'POST', headers: { 'Content-Type': 'text/plain; charset=utf-8' }, body: text,
    });
    const j = await r.json();
    if (!j.ok) { toast('No se pudo abrir'); return; }
    const t = newTab(null);
    activateTab(t, { noRefresh: true });
    paint(t, j, {});
  } catch (e) { toast('Error al abrir'); }
}

// paint inyecta el HTML resaltado en el doc de la pestaña y, si es la activa, refresca el cromo.
function paint(t, j, opts) {
  t.j = j; t.name = j.name || '';
  setTabLabel(t);
  t.doc.innerHTML = j.binary ? '' : (j.html || '');
  applyMarks(t, j.hl, opts);
  if (t === activeTab) {
    syncChrome(t);
    if (!opts.silent && !t.marks.regions.length) { view.scrollTop = 0; updateProgress(); }
    refind();
  }
  window.__log('painted ' + (j.name || ''));
}

// =========================================================================
// Zonas marcadas (--hl): resalta rangos de líneas (dónde se modificó un archivo), salta a la
// primera zona al abrir y permite navegar entre zonas con n/p (o click en la pill de estado).
// =========================================================================
// kind (semántica de diff): 0 = neutral crema (modificado), 1 = verde (agregado), 2 = rojo (borrado)
const MK_CLS = ['', 'hl-add', 'hl-del'];
const MK_SYM = ['~', '+', '−'];

function applyMarks(t, hl, opts) {
  const mk = t.marks;
  mk.regions = []; mk.idx = -1; mk.counts = [0, 0, 0];
  const ranges = Array.isArray(hl) ? hl : [];
  const lines = ranges.length ? t.doc.querySelectorAll('.chroma .line') : [];
  for (const r of ranges) {
    const from = r[0], to = Math.min(r[1], lines.length);
    const kind = (r[2] === 1 || r[2] === 2) ? r[2] : 0;
    if (!(from >= 1 && from <= lines.length)) continue;
    const els = [];
    for (let n = from; n <= to; n++) {
      els.push(lines[n - 1]);
      lines[n - 1].classList.add('hl');
      if (MK_CLS[kind]) lines[n - 1].classList.add(MK_CLS[kind]);
    }
    els[0].classList.add('hl-start');
    els[els.length - 1].classList.add('hl-end');
    mk.regions.push({ els, from, to, kind });
    mk.counts[kind] += els.length;
  }
  if (!mk.regions.length) return; // la pill la sincroniza syncChrome
  // al abrir, llevar la vista a la primera zona (en refresco silencioso se conserva la posición)
  if (!opts.silent && t === activeTab) {
    mk.idx = 0;
    requestAnimationFrame(() => { if (t === activeTab) { focusMark(false); updateProgress(); } });
  }
}
function updateMarksPill() {
  const n = marks.regions.length, c = marks.counts;
  let html;
  if (n === 1) {
    const rg = marks.regions[0];
    const label = rg.from === rg.to ? 'línea ' + rg.from : 'líneas ' + rg.from + '–' + rg.to;
    html = '§ <span class="mk-' + rg.kind + '">' + (rg.kind ? MK_SYM[rg.kind] + ' ' : '') + label + '</span>';
  } else {
    html = '§ ' + (marks.idx >= 0 ? (marks.idx + 1) + '/' : '') + n + ' zonas';
    if (c[1]) html += ' <span class="mk-1">+' + c[1] + '</span>';
    if (c[2]) html += ' <span class="mk-2">−' + c[2] + '</span>';
    if (c[0]) html += ' <span class="mk-0">~' + c[0] + '</span>';
  }
  $('stMarks').innerHTML = html; // sólo números/labels propios, sin contenido del archivo
  $('stMarks').classList.add('on');
}
function focusMark(smooth) {
  const rg = marks.regions[marks.idx];
  if (!rg) return;
  rg.els[0].scrollIntoView({ block: 'center', behavior: smooth ? 'smooth' : 'auto' });
  for (const el of rg.els) {
    el.classList.remove('hl-flash'); void el.offsetWidth; el.classList.add('hl-flash');
  }
  updateMarksPill();
}
function gotoMark(dir) {
  if (!marks.regions.length) return;
  marks.idx = (marks.idx + dir + marks.regions.length) % marks.regions.length;
  focusMark(true);
}

// ---- barra de estado ----------------------------------------------------
const nf = new Intl.NumberFormat('es-AR');
function humanSize(n) {
  if (n == null) return '';
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1024 / 1024).toFixed(2) + ' MB';
}
// fecha de modificación compacta: "hoy 13:24", "22/07 13:24" o "22/07/25 13:24" si es de otro año
function fmtWhen(ms) {
  if (!ms) return '';
  const d = new Date(ms), now = new Date(), p = (x) => String(x).padStart(2, '0');
  const hm = p(d.getHours()) + ':' + p(d.getMinutes());
  if (d.toDateString() === now.toDateString()) return 'hoy ' + hm;
  const dm = p(d.getDate()) + '/' + p(d.getMonth() + 1);
  return (d.getFullYear() === now.getFullYear() ? dm : dm + '/' + String(d.getFullYear()).slice(-2)) + ' ' + hm;
}
function fillStatus(j) {
  $('stPath').textContent = j.path || j.name || '';
  const dec = $('stDecomp');
  if (j.decompiled) { dec.textContent = 'decompilado · ' + (j.tool || ''); dec.classList.add('on'); }
  else { dec.textContent = ''; dec.classList.remove('on'); }
  const tr = $('stTrunc');
  if (j.truncated) { tr.textContent = 'recortado'; tr.classList.add('on'); }
  else { tr.textContent = ''; tr.classList.remove('on'); }
  $('stLang').textContent = j.binary ? 'binario' : (j.lang || '');
  $('stLines').textContent = j.binary ? '' : ((j.lines || 0) + (j.lines === 1 ? ' línea' : ' líneas'));
  $('stChars').textContent = (j.binary || j.chars == null) ? '' : nf.format(j.chars) + ' carac.';
  $('stSize').textContent = humanSize(j.binary ? j.bytes : (j.size != null ? j.size : j.bytes));
  $('stEol').textContent = j.binary ? '' : (j.crlf ? 'CRLF' : 'LF');
  $('stMod').textContent = j.mtime ? 'mod ' + fmtWhen(j.mtime) : '';
  $('stSel').textContent = '';
}
function clearStatus() {
  for (const id of ['stPath', 'stSel', 'stLang', 'stLines', 'stChars', 'stSize', 'stEol', 'stMod', 'stPos']) {
    $(id).textContent = '';
  }
  for (const id of ['stMarks', 'stDecomp', 'stTrunc']) {
    const el = $(id); el.textContent = ''; el.classList.remove('on');
  }
}

// ---- aviso central (binario / vacío) ------------------------------------
let noticeEl = null;
function showNotice(title, sub) {
  clearNotice();
  noticeEl = document.createElement('div');
  noticeEl.className = 'notice';
  noticeEl.innerHTML =
    '<svg class="notice-ico" width="50" height="50" viewBox="0 0 100 100" style="stroke:currentColor;stroke-width:7;fill:none;stroke-linecap:round;stroke-linejoin:round">' +
    '<polyline points="38,28 20,50 38,72"/><polyline points="62,28 80,50 62,72"/><line x1="58" y1="22" x2="42" y2="78"/></svg>' +
    '<div>' + title + '</div>' + (sub ? '<div class="notice-sub">' + sub + '</div>' : '');
  view.appendChild(noticeEl);
}
function clearNotice() { if (noticeEl) { noticeEl.remove(); noticeEl = null; } }

// ---- copiar todo (la pestaña activa) ------------------------------------
async function copyAll() {
  const root = adoc();
  const lines = [...root.querySelectorAll('.chroma .cl')].map((el) => el.textContent);
  let text = lines.length ? lines.join('\n') : root.textContent;
  if (!text) { const pre = root.querySelector('pre'); text = pre ? pre.innerText : ''; }
  if (!text) return;
  try { await navigator.clipboard.writeText(text); }
  catch (e) {
    const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta);
    ta.select(); try { document.execCommand('copy'); } catch (_) {} ta.remove();
  }
  toast('Copiado', true);
}

// =========================================================================
// Recarga en vivo — la página declara al server TODAS las rutas abiertas
// (/api/watch) y el server avisa los cambios por el bus (UN solo SSE: Chromium
// corta en 6 conexiones por host, así que acá no se abre nada por-pestaña).
// Si el archivo cambió y su pestaña está activa, repinta al instante; si está
// de fondo, marca el punto verde y refresca recién al activarla.
// =========================================================================
function syncWatch() {
  try {
    fetch('/api/watch', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paths: tabs.filter((t) => t.path).map((t) => t.path) }),
    });
  } catch (e) {}
}
function onFileChanged(path) {
  const t = tabs.find((x) => x.key && x.key === norm(path));
  if (!t) return;
  if (t === activeTab) liveReload(t);
  else { t.stale = true; t.el.classList.add('stale'); }
}
async function liveReload(t) {
  if (!t || !t.path) return;
  const denom = Math.max(1, view.scrollHeight - view.clientHeight);
  const keep = view.scrollTop / denom;
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(t.path));
    const j = await r.json();
    if (!j.ok) return;
    paint(t, j, { silent: true });
    t.stale = false; t.el.classList.remove('stale');
    if (t === activeTab) {
      requestAnimationFrame(() => {
        view.scrollTop = keep * Math.max(1, view.scrollHeight - view.clientHeight);
        updateProgress();
      });
      pulseLive(t);
    }
  } catch (e) {}
}
function pulseLive(t) {
  const d = $('capLive'); d.classList.remove('pulse'); void d.offsetWidth; d.classList.add('pulse');
  const dot = t && t.el.querySelector('.tab-dot');
  if (dot) { dot.classList.remove('pulse'); void dot.offsetWidth; dot.classList.add('pulse'); }
}

// =========================================================================
// Progreso de scroll
// =========================================================================
function updateProgress() {
  const denom = Math.max(1, view.scrollHeight - view.clientHeight);
  const p = Math.min(1, Math.max(0, view.scrollTop / denom));
  $('progressBar').style.width = (p * 100) + '%';
  // % de lectura en la barra de estado (sólo si hay documento y da para scrollear)
  const scrollable = view.scrollHeight > view.clientHeight + 2;
  $('stPos').textContent = (body.classList.contains('has-doc') && scrollable) ? Math.round(p * 100) + '%' : '';
}
view.addEventListener('scroll', updateProgress, { passive: true });

// =========================================================================
// Busqueda (CSS Custom Highlight API), sobre el código ACTIVO, excluyendo el gutter
// =========================================================================
const find = { matches: [], idx: -1 };
const supportsHL = !!(window.CSS && CSS.highlights && window.Highlight);

function openFind() {
  body.classList.add('find-open');
  const inp = $('findInput'); inp.focus(); inp.select();
  if (inp.value) runFind(inp.value);
}
function closeFind() {
  body.classList.remove('find-open');
  if (supportsHL) { CSS.highlights.delete('cipher-find'); CSS.highlights.delete('cipher-find-current'); }
  find.matches = []; find.idx = -1;
  $('findInput').blur();
}
// al cambiar de pestaña con la busqueda abierta, re-buscar sobre el doc nuevo
function refind() {
  if (!body.classList.contains('find-open')) return;
  const v = $('findInput').value;
  if (v) runFind(v); else updateFindCount();
}
function runFind(term) {
  if (!supportsHL) return;
  CSS.highlights.delete('cipher-find'); CSS.highlights.delete('cipher-find-current');
  find.matches = []; find.idx = -1;
  const q = term.trim().toLowerCase();
  if (!q) { updateFindCount(); return; }

  const root = adoc();
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
      let p = node.parentElement;
      while (p && p !== root) {
        if (p.classList && p.classList.contains('ln')) return NodeFilter.FILTER_REJECT; // gutter
        const tag = p.tagName;
        if (tag === 'STYLE' || tag === 'SCRIPT' || tag === 'svg') return NodeFilter.FILTER_REJECT;
        p = p.parentElement;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  const ranges = [];
  let node;
  while ((node = walker.nextNode())) {
    const hay = node.nodeValue.toLowerCase();
    let i = hay.indexOf(q);
    while (i >= 0) {
      const rr = document.createRange();
      rr.setStart(node, i); rr.setEnd(node, i + q.length);
      ranges.push(rr);
      i = hay.indexOf(q, i + q.length);
    }
  }
  find.matches = ranges;
  if (ranges.length) {
    const hl = new Highlight(...ranges); hl.priority = 1;
    CSS.highlights.set('cipher-find', hl);
    find.idx = 0; markCurrent();
  }
  updateFindCount();
}
function markCurrent() {
  if (!supportsHL) return;
  CSS.highlights.delete('cipher-find-current');
  const rr = find.matches[find.idx];
  if (!rr) return;
  const cur = new Highlight(rr); cur.priority = 2;
  CSS.highlights.set('cipher-find-current', cur);
  const el = rr.startContainer.parentElement;
  if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  updateFindCount();
}
function findStep(dir) {
  if (!find.matches.length) return;
  find.idx = (find.idx + dir + find.matches.length) % find.matches.length;
  markCurrent();
}
function updateFindCount() {
  const c = $('findCount');
  c.textContent = find.matches.length ? (find.idx + 1) + '/' + find.matches.length : (($('findInput').value ? '0/0' : ''));
}
$('findInput').addEventListener('input', (e) => runFind(e.target.value));
$('findInput').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') { e.preventDefault(); findStep(e.shiftKey ? -1 : 1); }
  else if (e.key === 'Escape') { e.preventDefault(); closeFind(); }
});
$('findPrev').addEventListener('click', () => findStep(-1));
$('findNext').addEventListener('click', () => findStep(1));
$('findClose').addEventListener('click', closeFind);

// =========================================================================
// Zoom + ajuste de linea + persistencia (server-side; ver config.go)
// =========================================================================
let rscale = (typeof window.__CIPHER_RSCALE__ === 'number' && window.__CIPHER_RSCALE__ > 0) ? window.__CIPHER_RSCALE__ : 1;
let wrap = window.__CIPHER_WRAP__ === true;

function applyScale() {
  rscale = Math.min(2.2, Math.max(0.6, rscale));
  document.documentElement.style.setProperty('--rscale', rscale.toFixed(3));
  // indicador de zoom en la barra de estado (sólo cuando no está al 100%)
  $('stZoom').textContent = Math.abs(rscale - 1) < 0.005 ? '' : Math.round(rscale * 100) + '%';
}
function applyWrap() {
  body.classList.toggle('wrap', wrap);
  $('btnWrap').classList.toggle('on', wrap);
}
applyScale(); applyWrap();

let saveTimer = null;
function postSettings() {
  try {
    fetch('/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ rscale, wrap }),
    });
  } catch (e) {}
}
function saveSettings() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => { saveTimer = null; postSettings(); }, 180);
}
window.addEventListener('pagehide', () => {
  if (!saveTimer) return;
  clearTimeout(saveTimer); saveTimer = null;
  try { navigator.sendBeacon('/api/settings', JSON.stringify({ rscale, wrap })); } catch (e) { postSettings(); }
});
function toggleWrap() { wrap = !wrap; applyWrap(); saveSettings(); }

// =========================================================================
// Pantalla completa
// =========================================================================
let isFs = false;
function setFullscreen(on) {
  isFs = on; body.classList.toggle('fullscreen', on);
  bridge('cipherFullscreen', on);
}

// =========================================================================
// Controles de ventana + arrastre/redimension frameless
// =========================================================================
$('btnMin').addEventListener('click', () => bridge('cipherMin'));
$('btnClose').addEventListener('click', () => bridge('cipherClose'));
$('btnMax').addEventListener('click', () => { bridge('cipherMaxToggle'); body.classList.toggle('maximized'); });
$('btnWrap').addEventListener('click', toggleWrap);
$('btnCopy').addEventListener('click', copyAll);
$('btnFind').addEventListener('click', openFind);
$('btnOpen').addEventListener('click', () => bridge('cipherPick'));
$('emptyOpen').addEventListener('click', () => bridge('cipherPick'));

let lastTbDown = 0;
$('titlebar').addEventListener('pointerdown', (e) => {
  if (e.button !== 0 || e.target.closest('.winbtn') || e.target.closest('.tbtn') || e.target.closest('.tab')) return;
  const now = Date.now();
  if (now - lastTbDown < 300) { lastTbDown = 0; bridge('cipherMaxToggle'); body.classList.toggle('maximized'); return; }
  lastTbDown = now;
  bridge('cipherDrag');
});
document.querySelectorAll('.rsz').forEach((el) => {
  el.addEventListener('pointerdown', (e) => { if (e.button === 0) bridge('cipherResize', el.dataset.dir); });
});

// =========================================================================
// Teclado
// =========================================================================
function typing() {
  const a = document.activeElement;
  return a && (a.tagName === 'INPUT' || a.tagName === 'TEXTAREA' || a.isContentEditable);
}
window.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey) {
    const k = e.key.toLowerCase();
    if (k === 'o') { e.preventDefault(); bridge('cipherPick'); return; }
    if (k === 'f') { e.preventDefault(); openFind(); return; }
    if (k === 'c' && !window.getSelection().toString()) { e.preventDefault(); copyAll(); return; }
    if (k === '=' || k === '+') { e.preventDefault(); rscale += 0.08; applyScale(); saveSettings(); return; }
    if (k === '-' || k === '_') { e.preventDefault(); rscale -= 0.08; applyScale(); saveSettings(); return; }
    if (k === '0') { e.preventDefault(); rscale = 1; applyScale(); saveSettings(); return; }
    // pestañas
    if (k === 'tab') { e.preventDefault(); cycleTab(e.shiftKey ? -1 : 1); return; }
    if (k === 'pagedown') { e.preventDefault(); cycleTab(1); return; }
    if (k === 'pageup') { e.preventDefault(); cycleTab(-1); return; }
    if (k === 'w') { e.preventDefault(); if (activeTab) closeTab(activeTab); return; }
    if (k >= '1' && k <= '9') {
      e.preventDefault();
      const t = tabs[Math.min(tabs.length, +k) - 1];
      if (t) activateTab(t);
      return;
    }
    return;
  }
  if (typing()) return;

  switch (e.key) {
    case 'w': case 'W': e.preventDefault(); toggleWrap(); break;
    case 'f': case 'F': case 'F11': e.preventDefault(); setFullscreen(!isFs); break;
    case 'Escape': if (isFs) { e.preventDefault(); setFullscreen(false); } break;
    case 'g': e.preventDefault(); view.scrollTo({ top: 0, behavior: 'smooth' }); break;
    case 'G': e.preventDefault(); view.scrollTo({ top: view.scrollHeight, behavior: 'smooth' }); break;
    case 'Home': e.preventDefault(); view.scrollTo({ top: 0, behavior: 'smooth' }); break;
    case 'End': e.preventDefault(); view.scrollTo({ top: view.scrollHeight, behavior: 'smooth' }); break;
    case ' ': case 'PageDown': e.preventDefault(); view.scrollBy({ top: view.clientHeight * 0.86, behavior: 'smooth' }); break;
    case 'PageUp': e.preventDefault(); view.scrollBy({ top: -view.clientHeight * 0.86, behavior: 'smooth' }); break;
    case 'j': view.scrollBy({ top: 90, behavior: 'smooth' }); break;
    case 'k': view.scrollBy({ top: -90, behavior: 'smooth' }); break;
    case 'n': e.preventDefault(); gotoMark(1); break;   // siguiente zona marcada (--hl)
    case 'p': case 'N': e.preventDefault(); gotoMark(-1); break; // zona anterior
  }
});
$('stMarks').addEventListener('click', () => gotoMark(1));

// =========================================================================
// Arrastrar y soltar (uno o varios archivos: una pestaña por cada uno)
// =========================================================================
window.addEventListener('dragover', (e) => { e.preventDefault(); body.classList.add('dragover'); });
window.addEventListener('dragleave', (e) => { if (!e.relatedTarget) body.classList.remove('dragover'); });
window.addEventListener('drop', async (e) => {
  e.preventDefault(); body.classList.remove('dragover');
  const files = e.dataTransfer ? [...e.dataTransfer.files] : [];
  for (const f of files) {
    try { await renderRawText(await f.text(), f.name); } catch (_) { toast('No se pudo leer'); }
  }
});

// =========================================================================
// Varios
// =========================================================================
let toastTimer;
function toast(msg, ok) {
  const t = $('toast'); t.textContent = msg; t.classList.toggle('ok', !!ok); t.classList.add('show');
  clearTimeout(toastTimer); toastTimer = setTimeout(() => t.classList.remove('show'), 2000);
}
window.addEventListener('contextmenu', (e) => { if (!typing() && !window.getSelection().toString()) e.preventDefault(); });
// contador de selección vivo en la barra de estado (sólo selecciones dentro del código activo)
document.addEventListener('selectionchange', () => {
  const s = window.getSelection();
  let n = 0;
  if (s && s.rangeCount && !s.isCollapsed && adoc().contains(s.anchorNode)) n = s.toString().length;
  $('stSel').textContent = n > 0 ? 'sel ' + nf.format(n) : '';
});
window.addEventListener('resize', () => {
  body.classList.toggle('maximized', !isFs && window.innerWidth >= screen.availWidth - 6);
  updateProgress(); updateStrip();
});
// Zoom con Ctrl+rueda -> tamaño del código (persistido); preventDefault corta el zoom nativo.
window.addEventListener('wheel', (e) => {
  if (!e.ctrlKey) return;
  e.preventDefault();
  rscale = Math.min(2.2, Math.max(0.6, rscale + (e.deltaY < 0 ? 0.07 : -0.07)));
  applyScale(); saveSettings();
}, { passive: false });

// =========================================================================
// Arranque — la ventana se muestra enseguida (con el SPLASH); el contenido se renderiza por debajo
// y, cuando está listo, el splash se funde. Avisamos al host por load/timeout, NUNCA por rAF (con la
// ventana aún oculta el navegador PAUSA rAF y se colgaría el aviso).
// =========================================================================
let readySent = false;
function sendReady(why) {
  if (readySent) return; readySent = true;
  window.__log('ready via ' + why);
  bridge('cipherReady');
}
let revealed = false;
function reveal() { if (revealed) return; revealed = true; body.classList.add('ready'); }
function boot() {
  if (document.readyState === 'complete') sendReady('load');
  else window.addEventListener('load', () => sendReady('load'));
  setTimeout(() => sendReady('timeout'), 400);
  setTimeout(reveal, 4000); // rescate: si el render se cuelga, revelar igual
  // bus del daemon caliente (único SSE): "open" = qué archivo mostrar, "change" = recarga viva.
  // En una RECONEXIÓN el server reenvía la última apertura (pendingOpen): si ya está en una
  // pestaña, ignorarla — es un replay, no un pedido nuevo (no robar el foco de la pestaña actual).
  try {
    const oe = new EventSource('/bus');
    let oeFirst = true, oeReplay = false;
    oe.onopen = () => { oeReplay = !oeFirst; oeFirst = false; };
    oe.onmessage = (ev) => {
      const replay = oeReplay; oeReplay = false;
      if (!ev.data) return;
      const cut = ev.data.indexOf('\t');
      if (cut < 0) return;
      const kind = ev.data.slice(0, cut), path = ev.data.slice(cut + 1);
      if (!path) return;
      if (kind === 'change') { onFileChanged(path); return; }
      if (kind !== 'open') return;
      if (replay && tabs.some((t) => t.key === norm(path))) return;
      reveal(); queueOpen(path);
    };
  } catch (e) {}
  fetch('/api/initial').then((r) => r.json()).then((j) => {
    ((j && j.paths) || []).forEach(queueOpen);
    return openChain;
  }).then(reveal).catch(() => reveal());
}
boot();
