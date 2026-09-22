'use strict';

// Cipher — logica de la UI. WebView2 no soporta -webkit-app-region, asi que el arrastre y el
// redimensionado de la ventana frameless se piden al host (cipherDrag/cipherResize). El resto es
// visor: muestra el HTML ya resaltado por el server (chroma) en PESTAÑAS — una por archivo, con
// dedup por ruta; el handoff del daemon, el dialogo de abrir (multi-seleccion) y el drag&drop
// abren aca — con gutter de numeros, busqueda, ir a linea, zoom, ajuste de linea, copiar,
// resaltado progresivo de los archivos grandes y recarga en vivo.

const $ = (id) => document.getElementById(id);
const body = document.body;
const view = $('view');
const code = $('code');

window.__log = (m) => { if (!window.__CIPHER_DEBUG__) return; try { fetch('/log?m=' + encodeURIComponent(m)); } catch (e) { /* sin host */ } };
window.addEventListener('error', (e) => window.__log('ERR ' + e.message + ' @' + (e.filename || '') + ':' + e.lineno));
window.addEventListener('unhandledrejection', (e) => window.__log('REJECT ' + (e.reason && (e.reason.message || e.reason))));

function bridge(name, ...args) {
  try { if (typeof window[name] === 'function') return window[name](...args); }
  catch (e) { window.__log('bridge ' + name + ' ' + e); }
}

// =========================================================================
// Pestañas — cada archivo vive en una pestaña con su documento YA renderizado
// (cambiar es instantaneo y conserva el scroll). Con UNA sola pestaña la barra
// muestra el caption centrado de siempre; la tira aparece recien con dos o mas.
// =========================================================================
const tabsNav = $('tabs');
const tabs = [];               // orden visual
const tabByKey = new Map();    // ruta normalizada -> pestaña (dedup y avisos del bus en O(1))
const tabByJob = new Map();    // trabajo de resaltado -> pestaña
let activeTab = null;          // pestaña visible
let tabSeq = 0;

function freshMarks() { return { regions: [], idx: -1, counts: [0, 0, 0] }; }
let marks = freshMarks(); // alias del marks de la pestaña ACTIVA (lo usan focusMark/gotoMark/pill)

const norm = (p) => (p || '').replace(/\//g, '\\').toLowerCase(); // identidad de ruta en Windows
const adoc = () => (activeTab ? activeTab.doc : code);            // raiz de busqueda/copia/seleccion

const TAB_X = '<svg width="10" height="10" viewBox="0 0 10 10"><line x1="1.7" y1="1.7" x2="8.3" y2="8.3"/><line x1="8.3" y1="1.7" x2="1.7" y2="8.3"/></svg>';

function newTab(path) {
  const t = {
    id: ++tabSeq, path: path || null, key: path ? norm(path) : null,
    name: '', j: null, scroll: 0, stale: false, gone: false, marks: freshMarks(),
    index: null, hl: null,
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
  if (t.key) tabByKey.set(t.key, t);
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
  body.classList.toggle('live-on', !!t.path && !t.gone);
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
  if (t.key && tabByKey.get(t.key) === t) tabByKey.delete(t.key);
  if (t.hl) tabByJob.delete(t.hl.job);
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
  clearNotice(); closeFind(); closeGoto();
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
    fillStatus(t.j, t);
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
const queueOpen = (p) => { openChain = openChain.then(() => openInTab(p)).catch((e) => window.__log('cola ' + e)); };
window.__cipherOpen = (p) => { (Array.isArray(p) ? p : [p]).forEach(queueOpen); }; // host: pick / Eval

async function openInTab(path) {
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(path));
    const j = await r.json();
    if (!j.ok) { toast(j.error || 'No se pudo abrir'); return; }
    const existing = tabByKey.get(norm(j.path));
    const t = existing || newTab(j.path);
    // reabrir la misma pestaña con la MISMA spec de marcas -> refresco silencioso (conserva el
    // scroll); spec distinta (--hl nuevo o limpiado) -> repinta y salta a la primera zona.
    const silent = !!(existing && existing.j &&
      JSON.stringify(existing.j.hl || []) === JSON.stringify(j.hl || []));
    activateTab(t, { noRefresh: true });
    const anchor = silent ? lineAnchor() : null;
    paint(t, j, { silent });
    if (anchor) restoreLineAnchor(anchor);
    setStale(t, false); setGone(t, false);
  } catch (e) { window.__log('open ' + e); toast('Error al abrir'); }
}

// Arrastrar-y-soltar: se mandan los BYTES (sin ruta en disco -> sin recarga viva ni dedup). Leerlo
// como texto aca lo decodificaba como UTF-8 y un .reg en UTF-16 llegaba al server ya roto.
async function renderRawBytes(bytes, name) {
  try {
    const r = await fetch('/render-text?name=' + encodeURIComponent(name || 'snippet.txt'), {
      method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: bytes,
    });
    const j = await r.json();
    if (!j.ok) { toast('No se pudo abrir'); return; }
    const t = newTab(null);
    activateTab(t, { noRefresh: true });
    paint(t, j, {});
  } catch (e) { window.__log('drop ' + e); toast('Error al abrir'); }
}

// paint inyecta el HTML resaltado en el doc de la pestaña y, si es la activa, refresca el cromo.
function paint(t, j, opts) {
  t.j = j; t.name = j.name || '';
  t.index = null;                                   // el indice de busqueda se rearma al buscar
  setTabLabel(t);
  t.doc.innerHTML = j.binary ? '' : (j.html || '');
  t.doc.style.setProperty('--dg', String(Math.max(1, String(j.lines || 1).length)));
  applyMarks(t, j.hl, opts);
  startHighlightPull(t, j);
  if (t === activeTab) {
    syncChrome(t);
    if (!opts.silent && !t.marks.regions.length) { view.scrollTop = 0; updateProgress(); }
    refind();
  }
  window.__log('painted ' + (j.name || ''));
}

// =========================================================================
// Resaltado progresivo (ver hljob.go): los archivos grandes llegan con las primeras pantallas
// resaltadas y el resto PLANO. El server sigue tokenizando en segundo plano y avisa por el bus
// ("hl"); aca se piden esos bloques y se reemplazan en su lugar. Mismo texto y misma grilla: la
// vista no se mueve. Un bloque con una seleccion adentro se deja para despues (reemplazarlo la
// borraria).
// =========================================================================
function startHighlightPull(t, j) {
  if (t.hl) tabByJob.delete(t.hl.job);
  t.hl = null;
  if (j.hlJob) {
    t.hl = { job: j.hlJob, next: j.hlFrom, total: j.chunks, busy: false, again: false, held: [] };
    tabByJob.set(j.hlJob, t);
    pullHighlight(t);                                // lo que ya este listo, sin esperar al bus
  }
  if (t === activeTab) syncHlPill(t);
}

async function pullHighlight(t) {
  const hl = t.hl;
  if (!hl) return;
  if (hl.busy) { hl.again = true; return; }
  hl.busy = true;
  try {
    do {
      hl.again = false;
      const r = await fetch(`/api/hl?job=${hl.job}&from=${hl.next}`);
      if (t.hl !== hl) return;                       // la pestaña se re-pinto mientras tanto
      if (!r.ok) { endHighlight(t); return; }        // cancelado o ya entregado
      const j = await r.json();
      if (j.chunks.length) applyChunks(t, j.from, j.chunks);
      hl.next = j.from + j.chunks.length;
      if (t === activeTab) syncHlPill(t);
      if (j.done || hl.next >= hl.total) { endHighlight(t); return; }
    } while (hl.again);
  } catch (e) { window.__log('hl ' + e); }
  finally { hl.busy = false; }
}

function endHighlight(t) {
  if (!t.hl) return;
  if (t.hl.held.length) { setTimeout(() => flushHeld(t), 1000); return; } // quedan bloques con seleccion
  tabByJob.delete(t.hl.job);
  t.hl = null;
  if (t === activeTab) syncHlPill(t);
}

const chunksOf = (t) => t.doc.querySelector('pre.chroma > code');

function applyChunks(t, from, htmls) {
  const holder = chunksOf(t);
  if (!holder) return;
  const tpl = document.createElement('template');
  tpl.innerHTML = htmls.join('');
  const fresh = [...tpl.content.children];
  const sel = window.getSelection();
  for (let i = 0; i < fresh.length; i++) {
    const old = holder.children[from + i];
    if (!old) break;
    if (sel && !sel.isCollapsed && (old.contains(sel.anchorNode) || old.contains(sel.focusNode))) {
      t.hl.held.push({ idx: from + i, el: fresh[i] });  // no borrarle la seleccion al usuario
      continue;
    }
    old.replaceWith(fresh[i]);
  }
  afterChunksChanged(t);
}

function flushHeld(t) {
  const hl = t.hl;
  if (!hl || !hl.held.length) { endHighlight(t); return; }
  const holder = chunksOf(t);
  const sel = window.getSelection();
  hl.held = hl.held.filter(({ idx, el }) => {
    const old = holder && holder.children[idx];
    if (!old) return false;
    if (sel && !sel.isCollapsed && (old.contains(sel.anchorNode) || old.contains(sel.focusNode))) return true;
    old.replaceWith(el);
    return false;
  });
  afterChunksChanged(t);
  if (hl.held.length) setTimeout(() => flushHeld(t), 1000); else endHighlight(t);
}

// los renglones reemplazados son elementos nuevos: marcas (--hl) y busqueda se recalculan
function afterChunksChanged(t) {
  t.index = null;
  if (t.marks.regions.length) {
    const idx = t.marks.idx;
    applyMarks(t, t.j.hl, { silent: true });
    t.marks.idx = idx;
  }
  if (t === activeTab) scheduleRefind();
}

function syncHlPill(t) {
  const pill = $('stHl');
  if (t && t.hl && t.hl.total) {
    pill.textContent = 'resaltando ' + Math.floor(100 * t.hl.next / t.hl.total) + '%';
    pill.classList.add('on');
  } else { pill.textContent = ''; pill.classList.remove('on'); }
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
  if (!ranges.length) return;
  const lines = t.doc.getElementsByClassName('line');
  for (const r of ranges) {
    const from = r[0], to = Math.min(r[1], lines.length);
    const kind = (r[2] === 1 || r[2] === 2) ? r[2] : 0;
    if (!(from >= 1 && from <= lines.length)) continue;
    const els = [];
    for (let n = from; n <= to; n++) {
      const el = lines[n - 1];
      els.push(el);
      el.classList.add('hl');
      if (MK_CLS[kind]) el.classList.add(MK_CLS[kind]);
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
function pill(id, text) {
  const el = $(id); el.textContent = text || ''; el.classList.toggle('on', !!text);
}
function fillStatus(j, t) {
  $('stPath').textContent = j.path || j.name || '';
  pill('stDecomp', j.decompiled ? 'decompilado · ' + (j.tool || '') : '');
  pill('stTrunc', j.truncated ? 'recortado' : '');
  pill('stPlain', j.plain ? 'sin resaltado · ' + j.plain : '');
  pill('stGone', t && t.gone ? 'borrado del disco' : '');
  $('stLang').textContent = j.binary ? 'binario' : (j.lang || '');
  $('stLines').textContent = j.binary ? '' : (nf.format(j.lines || 0) + (j.lines === 1 ? ' línea' : ' líneas'));
  $('stChars').textContent = (j.binary || j.chars == null) ? '' : nf.format(j.chars) + ' carac.';
  $('stSize').textContent = humanSize(j.binary ? j.bytes : (j.size != null ? j.size : j.bytes));
  // codificación + fin de línea. La codificación sólo se muestra si NO es UTF-8 a secas (un .reg
  // de regedit viene en UTF-16 LE) y el fin de línea sólo si hay más de un renglón; "Mixto"
  // delata el archivo con finales mezclados (un clásico de los merges).
  const eol = j.eol !== undefined ? j.eol : (j.crlf ? 'CRLF' : 'LF');
  $('stEol').textContent = j.binary ? '' : [j.encoding, eol].filter(Boolean).join(' · ');
  $('stMod').textContent = j.mtime ? 'mod ' + fmtWhen(j.mtime) : '';
  $('stSel').textContent = '';
  syncHlPill(t);
}
function clearStatus() {
  for (const id of ['stPath', 'stSel', 'stLang', 'stLines', 'stChars', 'stSize', 'stEol', 'stMod', 'stPos']) {
    $(id).textContent = '';
  }
  for (const id of ['stMarks', 'stDecomp', 'stTrunc', 'stHl', 'stPlain', 'stGone']) pill(id, '');
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
  const lines = [...root.getElementsByClassName('cl')].map((el) => el.textContent);
  let text = lines.length ? lines.join('\n') : root.textContent;
  if (!text) { const pre = root.querySelector('pre'); text = pre ? pre.innerText : ''; }
  if (!text) return;
  try { await navigator.clipboard.writeText(text); }
  catch (e) {
    window.__log('clipboard ' + e);
    const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta);
    ta.select(); try { document.execCommand('copy'); } catch (err) { window.__log('execCommand ' + err); }
    ta.remove();
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
  fetch('/api/watch', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ paths: tabs.filter((t) => t.path).map((t) => t.path) }),
  }).catch((e) => window.__log('watch ' + e));
}
function setStale(t, on) { t.stale = on; t.el.classList.toggle('stale', on); }
function setGone(t, on) {
  t.gone = on; t.el.classList.toggle('gone', on);
  if (t === activeTab) { pill('stGone', on ? 'borrado del disco' : ''); body.classList.toggle('live-on', !!t.path && !on); }
}
function onFileChanged(path) {
  const t = tabByKey.get(norm(path));
  if (!t) return;
  setGone(t, false);
  if (t === activeTab) liveReload(t);
  else setStale(t, true);
}
function onFileGone(path) {
  const t = tabByKey.get(norm(path));
  if (t) setGone(t, true);
}

// ancla de lectura por RENGLON: el primer renglon visible y cuanto de el ya se scrolleo. Tras la
// recarga se vuelve al mismo numero de renglon (lo natural en codigo: la vista no "viaja" si se
// agregan renglones al final). Antes era la fraccion del scroll, que se corria con cada cambio.
function lineAnchor() {
  const el = topLine();
  if (!el) return null;
  return { n: lineNumberOf(el), delta: view.scrollTop - el.offsetTop };
}
function restoreLineAnchor(a) {
  const lines = adoc().getElementsByClassName('line');
  const el = lines[Math.min(a.n, lines.length) - 1];
  if (el) view.scrollTop = el.offsetTop + a.delta;
  updateProgress();
}
// el renglon que esta arriba de todo: elementFromPoint sobre el gutter (O(1), vale con y sin ajuste)
function topLine() {
  const r = view.getBoundingClientRect();
  const hit = document.elementFromPoint(r.left + 4, r.top + 2);
  return hit ? hit.closest('.line') : null;
}
const lineNumberOf = (el) => parseInt(el.querySelector('.ln').textContent, 10) || 1;

async function liveReload(t) {
  if (!t || !t.path) return;
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(t.path));
    const j = await r.json();
    if (!j.ok) return;
    setStale(t, false);
    if (t.j && j.html === t.j.html) {            // mismo contenido (se toco la fecha): no repintar
      t.j.mtime = j.mtime;
      if (t === activeTab) fillStatus(t.j, t);
      // el render nuevo cancelo el trabajo de resaltado anterior: adoptar el nuevo (re-entrega
      // bloques identicos a los ya pintados, que se reemplazan sin que nada se mueva)
      if (j.hlJob) startHighlightPull(t, j);
      return;
    }
    const anchor = t === activeTab ? lineAnchor() : null;
    paint(t, j, { silent: true });
    if (t === activeTab) {
      if (anchor) restoreLineAnchor(anchor);
      pulseLive(t);
    }
  } catch (e) { window.__log('reload ' + e); }
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
// Busqueda (CSS Custom Highlight API) sobre un INDICE DE RENGLONES del documento activo
//
// El indice es el texto de cada renglon (sus .cl), armado una vez por render y consultado por
// cada tecla sin recorrer el DOM. Lo que antes no andaba: cada token es un <span>, y la busqueda
// miraba nodo por nodo, asi que "func main" (keyword + espacio + nombre) daba 0 resultados. Ahora
// una coincidencia puede cruzar todos los tokens que quiera dentro de su renglon. Opciones:
// Aa (mayusculas), ab (palabra entera), .* (expresion regular).
// =========================================================================
const find = { matches: [], idx: -1, opts: { caseSensitive: false, word: false, regex: false } };
const supportsHL = !!(window.CSS && CSS.highlights && window.Highlight);
const MAX_HITS = 5000;          // el Highlight API sufre con decenas de miles

function docIndex(t) {
  if (t.index) return t.index;
  const els = [...t.doc.getElementsByClassName('cl')];
  const text = new Array(els.length);
  for (let i = 0; i < els.length; i++) text[i] = els[i].textContent;
  t.index = { els, text, lower: null };
  return t.index;
}

// matcher: a partir de la consulta y las opciones, una funcion renglon -> [[ini, fin], …]
const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
function buildMatcher(q, o) {
  if (!o.regex && !o.word) {
    const needle = o.caseSensitive ? q : q.toLowerCase();
    return { plain: needle };
  }
  const src = o.regex ? q : escapeRe(q);
  const ci = o.caseSensitive ? '' : 'i';
  // palabra entera con bordes Unicode (ñ, á: \b de JS solo entiende ASCII) -> hace falta la
  // bandera u. Sin "palabra entera", la regex del usuario va SIN u: en modo u, cosas comunes
  // como \- o \: son errores de sintaxis y la busqueda quedaba en rojo sin razon.
  const re = o.word
    ? new RegExp(`(?<![\\p{L}\\p{N}_])(?:${src})(?![\\p{L}\\p{N}_])`, 'gu' + ci)
    : new RegExp(src, 'g' + ci);
  return { re };
}
function matchLine(m, text, lowerText, out, line) {
  if (m.plain !== undefined) {
    const hay = lowerText !== null ? lowerText : text;
    for (let i = hay.indexOf(m.plain); i >= 0 && out.length < MAX_HITS; i = hay.indexOf(m.plain, i + m.plain.length)) {
      out.push([line, i, i + m.plain.length]);
    }
    return;
  }
  m.re.lastIndex = 0;
  let r;
  while ((r = m.re.exec(text)) && out.length < MAX_HITS) {
    if (r[0].length === 0) { m.re.lastIndex++; continue; }   // coincidencia vacia: avanzar
    out.push([line, r.index, r.index + r[0].length]);
  }
}

// (renglon, offset) -> (nodo de texto, offset): se camina el .cl de ese renglon una sola vez
function rangesForLine(el, hits) {
  const out = [];
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  let node = walker.nextNode(), base = 0, h = 0;
  const pos = [];                       // extremos ordenados a resolver
  for (const [, s, e] of hits) pos.push(s, e);
  const res = new Array(pos.length);
  const order = pos.map((v, i) => i).sort((a, b) => pos[a] - pos[b]);
  for (const i of order) {
    const want = pos[i];
    while (node && base + node.nodeValue.length < want) { base += node.nodeValue.length; node = walker.nextNode(); }
    // un extremo justo en el borde entre dos nodos: el comienzo va al siguiente, el fin al anterior
    res[i] = node ? [node, want - base] : null;
  }
  for (let k = 0; k < hits.length; k++, h += 2) {
    const a = res[h], b = res[h + 1];
    if (!a || !b) continue;
    const r = document.createRange();
    try { r.setStart(a[0], a[1]); r.setEnd(b[0], b[1]); out.push(r); } catch (e) { window.__log('range ' + e); }
  }
  return out;
}

function openFind() {
  closeGoto();
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
  if (v) runFind(v, { keepPlace: true }); else updateFindCount();
}
let refindTimer = 0;
function scheduleRefind() { clearTimeout(refindTimer); refindTimer = setTimeout(refind, 200); }

function runFind(term, { keepPlace = false } = {}) {
  if (!supportsHL) return;
  const prevLine = keepPlace && find.matches[find.idx] ? find.matches[find.idx].line : -1;
  CSS.highlights.delete('cipher-find'); CSS.highlights.delete('cipher-find-current');
  find.matches = []; find.idx = -1;
  $('findbar').classList.remove('bad');
  const t = activeTab;
  const q = find.opts.regex ? term : term.trim();
  if (!q || !t || !t.j || t.j.binary) { updateFindCount(); return; }
  let m;
  try { m = buildMatcher(q, find.opts); }
  catch (e) { $('findbar').classList.add('bad'); updateFindCount(); return; }  // regex a medio escribir
  const ix = docIndex(t);
  if (m.plain !== undefined && !find.opts.caseSensitive && !ix.lower) ix.lower = ix.text.map((s) => s.toLowerCase());
  const hits = [];
  for (let i = 0; i < ix.text.length && hits.length < MAX_HITS; i++) {
    matchLine(m, ix.text[i], m.plain !== undefined && !find.opts.caseSensitive ? ix.lower[i] : null, hits, i);
  }
  // rangos agrupados por renglon (el DOM de cada renglon se camina una sola vez)
  const ranges = [];
  for (let a = 0; a < hits.length;) {
    let b = a;
    while (b < hits.length && hits[b][0] === hits[a][0]) b++;
    for (const r of rangesForLine(ix.els[hits[a][0]], hits.slice(a, b))) {
      r.line = hits[a][0];
      ranges.push(r);
    }
    a = b;
  }
  find.matches = ranges;
  if (ranges.length) {
    const hl = new Highlight(...ranges); hl.priority = 1;
    CSS.highlights.set('cipher-find', hl);
    // arrancar por la coincidencia mas cercana a donde se esta leyendo (o donde estaba)
    const top = topLine();
    const from = prevLine >= 0 ? prevLine : (top ? lineNumberOf(top) - 1 : 0);
    const k = ranges.findIndex((r) => r.line >= from);
    find.idx = k >= 0 ? k : 0;
    markCurrent(!keepPlace);
  }
  updateFindCount();
}
function markCurrent(scroll = true) {
  if (!supportsHL) return;
  CSS.highlights.delete('cipher-find-current');
  const rr = find.matches[find.idx];
  if (!rr) return;
  const cur = new Highlight(rr); cur.priority = 2;
  CSS.highlights.set('cipher-find-current', cur);
  if (scroll) {
    // solo si no esta ya comoda a la vista (con content-visibility el renglon puede no estar medido)
    const line = rr.startContainer.parentElement.closest('.line');
    const b = rr.getBoundingClientRect(), v = view.getBoundingClientRect();
    const margin = v.height * 0.12;
    if (b.top < v.top + margin || b.bottom > v.bottom - margin || b.left < v.left || b.right > v.right) {
      (line || rr.startContainer.parentElement).scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'smooth' });
    }
  }
  updateFindCount();
}
function findStep(dir) {
  if (!find.matches.length) return;
  find.idx = (find.idx + dir + find.matches.length) % find.matches.length;
  markCurrent();
}
function updateFindCount() {
  const c = $('findCount');
  const n = find.matches.length;
  c.textContent = n ? (find.idx + 1) + '/' + (n >= MAX_HITS ? MAX_HITS + '+' : n) : ($('findInput').value ? '0/0' : '');
}
function toggleFindOpt(name, btn) {
  find.opts[name] = !find.opts[name];
  btn.classList.toggle('on', find.opts[name]);
  const v = $('findInput').value;
  if (v) runFind(v);
  $('findInput').focus();
}
const FIND_OPTS = { c: ['caseSensitive', 'findCase'], w: ['word', 'findWord'], r: ['regex', 'findRegex'] };
for (const [name, id] of Object.values(FIND_OPTS)) {
  $(id).addEventListener('click', () => toggleFindOpt(name, $(id)));
}
let findFrame = 0;
$('findInput').addEventListener('input', (e) => {    // una rafaga de teclas = una busqueda por frame
  cancelAnimationFrame(findFrame);
  findFrame = requestAnimationFrame(() => runFind(e.target.value));
});
$('findInput').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') { e.preventDefault(); findStep(e.shiftKey ? -1 : 1); }
  else if (e.key === 'Escape') { e.preventDefault(); closeFind(); }
  else if (e.altKey && FIND_OPTS[e.key.toLowerCase()]) {
    e.preventDefault();
    const [name, id] = FIND_OPTS[e.key.toLowerCase()];
    toggleFindOpt(name, $(id));
  }
});
$('findPrev').addEventListener('click', () => findStep(-1));
$('findNext').addEventListener('click', () => findStep(1));
$('findClose').addEventListener('click', closeFind);

// =========================================================================
// Ir a linea (Ctrl G): "120" salta al renglon 120 y lo destella. Tambien acepta "120:8"
// (renglon:columna, lo que escupen los compiladores) y numeros negativos (desde el final).
// =========================================================================
function openGoto() {
  if (!activeTab || !activeTab.j || activeTab.j.binary) return;
  closeFind();
  body.classList.add('goto-open');
  const inp = $('gotoInput');
  const total = activeTab.j.lines || 0;
  $('gotoHint').textContent = '1–' + nf.format(total);
  inp.value = ''; inp.focus();
}
function closeGoto() { body.classList.remove('goto-open'); $('gotoInput').blur(); }
function gotoLine(raw) {
  const t = activeTab;
  if (!t) return;
  const m = /^\s*(-?\d+)\s*(?::\s*\d+)?\s*$/.exec(raw);
  if (!m) { toast('Número de línea inválido'); return; }
  const total = t.j.lines || 0;
  let n = parseInt(m[1], 10);
  if (n < 0) n = total + 1 + n;
  n = Math.max(1, Math.min(total, n));
  const el = t.doc.getElementsByClassName('line')[n - 1];
  if (!el) return;
  el.scrollIntoView({ block: 'center', behavior: 'auto' });
  el.classList.remove('goto-flash'); void el.offsetWidth; el.classList.add('goto-flash');
  updateProgress();
  closeGoto();
}
$('gotoInput').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') { e.preventDefault(); gotoLine(e.target.value); }
  else if (e.key === 'Escape') { e.preventDefault(); closeGoto(); }
});
$('gotoInput').addEventListener('blur', () => setTimeout(() => { if (document.activeElement !== $('gotoInput')) body.classList.remove('goto-open'); }, 120));

// =========================================================================
// Zoom + ajuste de linea + persistencia (server-side; ver config.go)
// =========================================================================
let rscale = (typeof window.__CIPHER_RSCALE__ === 'number' && window.__CIPHER_RSCALE__ > 0) ? window.__CIPHER_RSCALE__ : 1;
let wrap = window.__CIPHER_WRAP__ === true;
const RSCALE_MIN = 0.6, RSCALE_MAX = 2.2, RSCALE_STEP_KEY = 0.08, RSCALE_STEP_WHEEL = 0.07;

// ---- escala tipografica --------------------------------------------------------------------
// El cuerpo del codigo se eligio para caer en 16 px FISICOS exactos a 150 % de DPI, pero eso vale
// solo a zoom 1: apenas se toca el zoom, el tamaño cae en pixeles fraccionarios y el trazo
// hairline de la ExtraLight se ablanda. Aca se recalcula para que SIEMPRE aterrice en la grilla
// fisica, y el interlineado tambien: si el renglon mide 18,6 px reales, cada linea apoya en un
// subpixel distinto y el bloque va cambiando de nitidez de arriba a abajo.

const PT = 0.75;             // 1 px CSS = 0,75 pt
const PISO_PX = 5 / PT;      // piso de 5 pt
const BASE_PX = 10.6667;     // em de 16 px FISICOS a 150 % (el cuerpo del look Notepad++)
const LH = 1.25;             // 20 px fisicos exactos a zoom 1
const CROMO = [10, 11, 11.5, 12, 12.5, 13, 14];

// La ExtraLight (200) es el corte mas fino que existe y es el que queremos SIEMPRE. Solo se
// rescata cuando el tamaño final ya no da para dibujarla: por debajo de ~6 pt el trazo mide menos
// de un pixel y, por mas subpixel que haya, sale gris sucio en vez de fino. Ahi, y solo ahi, sube
// un escalon a Light. De 0,8 de zoom para arriba nunca se toca.
const pesoCodigo = (px) => (px * PT < 6 ? 300 : 200);

let scaledFor = null;        // "rscale@dpr" ya aplicado: re-aplicar lo mismo es trabajo tirado
function applyScale() {
  rscale = Math.min(RSCALE_MAX, Math.max(RSCALE_MIN, rscale));
  const dpr = window.devicePixelRatio || 1;
  const key = rscale.toFixed(3) + '@' + dpr;
  if (key === scaledFor) return;
  scaledFor = key;
  const alPixel = (px) => Math.max(1, Math.round(Math.max(px, PISO_PX) * dpr)) / dpr;
  const raiz = document.documentElement.style;

  raiz.setProperty('--rscale', rscale.toFixed(3));

  const fs = alPixel(BASE_PX * rscale);
  raiz.setProperty('--fs-code', fs.toFixed(4) + 'px');
  raiz.setProperty('--lh-code', (Math.max(1, Math.round(fs * LH * dpr)) / dpr).toFixed(4) + 'px');
  raiz.setProperty('--w-code', String(pesoCodigo(fs)));
  for (const px of CROMO) raiz.setProperty('--px-' + String(px).replace('.', '_'), alPixel(px).toFixed(4) + 'px');

  // indicador de zoom en la barra de estado (sólo cuando no está al 100%)
  $('stZoom').textContent = Math.abs(rscale - 1) < 0.005 ? '' : Math.round(rscale * 100) + '%';
}
function applyWrap() {
  body.classList.toggle('wrap', wrap);
  $('btnWrap').classList.toggle('on', wrap);
}
applyScale(); applyWrap();

// El DPI puede cambiar sin que haya resize (arrastrar la ventana a un monitor con otra escala):
// hay que volver a redondear ahi tambien, o el codigo queda apoyado en la grilla del monitor viejo
// y la ExtraLight se ablanda. (Folio ya lo hacia; Cipher no.)
let mqDpr = null;
function watchDpr() {
  if (mqDpr) mqDpr.removeEventListener('change', onDprChange);
  mqDpr = window.matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
  mqDpr.addEventListener('change', onDprChange);
}
function onDprChange() { applyScale(); watchDpr(); }
watchDpr();

function zoomTo(next) {
  const anchor = activeTab ? lineAnchor() : null;   // el zoom no te cambia de renglon
  rscale = Math.min(RSCALE_MAX, Math.max(RSCALE_MIN, next));
  applyScale(); saveSettings();
  if (anchor) { anchor.delta = 0; restoreLineAnchor(anchor); }
}

let saveTimer = null;
function postSettings() {
  fetch('/api/settings', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ rscale, wrap }),
  }).catch((e) => window.__log('settings ' + e));
}
function saveSettings() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => { saveTimer = null; postSettings(); }, 180);
}
window.addEventListener('pagehide', () => {
  if (!saveTimer) return;
  clearTimeout(saveTimer); saveTimer = null;
  if (!navigator.sendBeacon('/api/settings', JSON.stringify({ rscale, wrap }))) postSettings();
});
function toggleWrap() {
  const anchor = activeTab ? lineAnchor() : null;   // con ajuste los renglones cambian de alto
  wrap = !wrap; applyWrap(); saveSettings();
  if (anchor) { anchor.delta = 0; restoreLineAnchor(anchor); }
}

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

const DBLCLICK_MS = 300;
let lastTbDown = 0;
$('titlebar').addEventListener('pointerdown', (e) => {
  if (e.button !== 0 || e.target.closest('.winbtn') || e.target.closest('.tbtn') || e.target.closest('.tab')) return;
  const now = Date.now();
  if (now - lastTbDown < DBLCLICK_MS) { lastTbDown = 0; bridge('cipherMaxToggle'); body.classList.toggle('maximized'); return; }
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
const SCROLL_PAGE = 0.86, SCROLL_LINE = 90;
window.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey) {
    const k = e.key.toLowerCase();
    if (k === 'o') { e.preventDefault(); bridge('cipherPick'); return; }
    if (k === 'f') { e.preventDefault(); openFind(); return; }
    if (k === 'g') { e.preventDefault(); openGoto(); return; }
    if (k === 'c' && !window.getSelection().toString() && !typing()) { e.preventDefault(); copyAll(); return; }
    if (k === '=' || k === '+') { e.preventDefault(); zoomTo(rscale + RSCALE_STEP_KEY); return; }
    if (k === '-' || k === '_') { e.preventDefault(); zoomTo(rscale - RSCALE_STEP_KEY); return; }
    if (k === '0') { e.preventDefault(); zoomTo(1); return; }
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
    case '/': e.preventDefault(); openFind(); break;
    case 'Escape': if (isFs) { e.preventDefault(); setFullscreen(false); } break;
    case 'g': case 'Home': e.preventDefault(); view.scrollTo({ top: 0, behavior: 'smooth' }); break;
    case 'G': case 'End': e.preventDefault(); view.scrollTo({ top: view.scrollHeight, behavior: 'smooth' }); break;
    case ' ': case 'PageDown': e.preventDefault(); view.scrollBy({ top: view.clientHeight * SCROLL_PAGE * (e.shiftKey ? -1 : 1), behavior: 'smooth' }); break;
    case 'PageUp': e.preventDefault(); view.scrollBy({ top: -view.clientHeight * SCROLL_PAGE, behavior: 'smooth' }); break;
    case 'j': view.scrollBy({ top: SCROLL_LINE, behavior: 'smooth' }); break;
    case 'k': view.scrollBy({ top: -SCROLL_LINE, behavior: 'smooth' }); break;
    case 'n': e.preventDefault(); if (find.matches.length) findStep(1); else gotoMark(1); break;   // siguiente coincidencia / zona
    case 'p': case 'N': e.preventDefault(); if (find.matches.length) findStep(-1); else gotoMark(-1); break;
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
    try { await renderRawBytes(await f.arrayBuffer(), f.name); } catch (err) { window.__log('drop ' + err); toast('No se pudo leer'); }
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
// contador de selección vivo en la barra de estado (sólo selecciones dentro del código activo).
// Coalescido a un frame: arrastrando para seleccionar llegan decenas de eventos, y toString() de
// una selección grande recorre todo lo seleccionado.
let selFrame = 0;
document.addEventListener('selectionchange', () => {
  cancelAnimationFrame(selFrame);
  selFrame = requestAnimationFrame(() => {
    const s = window.getSelection();
    let n = 0;
    if (s && s.rangeCount && !s.isCollapsed && adoc().contains(s.anchorNode)) n = s.toString().length;
    $('stSel').textContent = n > 0 ? 'sel ' + nf.format(n) : '';
  });
});
let resizeFrame = 0;
window.addEventListener('resize', () => {
  cancelAnimationFrame(resizeFrame);
  resizeFrame = requestAnimationFrame(() => {
    body.classList.toggle('maximized', !isFs && window.innerWidth >= screen.availWidth - 6);
    updateProgress(); updateStrip();
    applyScale();    // no hace nada salvo que haya cambiado el DPI (ver scaledFor)
  });
});
// Zoom con Ctrl+rueda -> tamaño del código (persistido); preventDefault corta el zoom nativo.
window.addEventListener('wheel', (e) => {
  if (!e.ctrlKey) return;
  e.preventDefault();
  zoomTo(rscale + (e.deltaY < 0 ? RSCALE_STEP_WHEEL : -RSCALE_STEP_WHEEL));
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
  // bus del daemon caliente (único SSE): "open" = qué archivo mostrar, "change"/"gone" = recarga
  // viva, "hl" = bloques recién resaltados. En una RECONEXIÓN el server reenvía la última apertura
  // (pendingOpen): si ya está en una pestaña, ignorarla — es un replay, no un pedido nuevo.
  try {
    const oe = new EventSource('/bus');
    let oeFirst = true, oeReplay = false;
    oe.onopen = () => { oeReplay = !oeFirst; oeFirst = false; };
    oe.onmessage = (ev) => {
      const replay = oeReplay; oeReplay = false;
      if (!ev.data) return;
      const parts = ev.data.split('\t');
      const kind = parts[0];
      if (kind === 'hl') {                     // hl \t job \t desde \t hasta
        const t = tabByJob.get(Number(parts[1]));
        if (t) pullHighlight(t);
        return;
      }
      const path = parts.slice(1).join('\t');
      if (!path) return;
      if (kind === 'change') { onFileChanged(path); return; }
      if (kind === 'gone') { onFileGone(path); return; }
      if (kind !== 'open') return;
      if (replay && tabByKey.has(norm(path))) return;
      reveal(); queueOpen(path);
    };
  } catch (e) { window.__log('bus ' + e); }
  fetch('/api/initial').then((r) => r.json()).then((j) => {
    ((j && j.paths) || []).forEach(queueOpen);
    return openChain;
  }).then(reveal).catch(() => reveal());
}
boot();
