'use strict';

// Cipher — logica de la UI. WebView2 no soporta -webkit-app-region, asi que el arrastre y el
// redimensionado de la ventana frameless se piden al host (cipherDrag/cipherResize). El resto es
// visor: muestra el HTML ya resaltado por el server (chroma), con gutter de numeros, busqueda,
// zoom, ajuste de linea, copiar y recarga en vivo.

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

// ---- estado -------------------------------------------------------------
const current = { path: null, name: '', dir: '' };
let es = null; // EventSource de recarga en vivo

// =========================================================================
// Apertura / render
// =========================================================================
async function openPath(path, opts = {}) {
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(path));
    const j = await r.json();
    if (!j.ok) { toast(j.error || 'No se pudo abrir'); return; }
    current.path = j.path; current.name = j.name; current.dir = j.dir;
    paint(j, opts);
    watch(j.path);
    body.classList.add('live-on');
  } catch (e) { window.__log('open ' + e); toast('Error al abrir'); }
}
window.__cipherOpen = (p) => openPath(p); // el host lo llama por Eval

// Arrastrar-y-soltar: resalta texto crudo (sin ruta -> sin recarga viva).
async function renderRawText(text, name) {
  try {
    const r = await fetch('/render-text?name=' + encodeURIComponent(name || 'snippet.txt'), {
      method: 'POST', headers: { 'Content-Type': 'text/plain; charset=utf-8' }, body: text,
    });
    const j = await r.json();
    if (!j.ok) { toast('No se pudo abrir'); return; }
    current.path = null; current.name = j.name; current.dir = '';
    if (es) { es.close(); es = null; }
    body.classList.remove('live-on');
    paint(j, {});
  } catch (e) { toast('Error al abrir'); }
}

// paint inyecta el HTML resaltado y actualiza barra de titulo + barra de estado.
function paint(j, opts) {
  clearNotice();
  if (j.binary) {
    code.innerHTML = '';
    showNotice('Archivo binario', 'No se puede mostrar como texto');
  } else {
    code.innerHTML = j.html || '';
  }
  body.classList.add('has-doc');
  body.classList.remove('no-doc', 'empty');
  $('capName').textContent = j.name || '';
  $('capLang').textContent = j.binary ? '' : (j.lang || '');
  fillStatus(j);
  applyMarks(j.hl, opts);
  if (!opts.silent && !marks.regions.length) { view.scrollTop = 0; updateProgress(); }
  window.__log('painted ' + (j.name || ''));
}

// =========================================================================
// Zonas marcadas (--hl): resalta rangos de líneas (dónde se modificó un archivo), salta a la
// primera zona al abrir y permite navegar entre zonas con n/p (o click en la pill de estado).
// =========================================================================
const marks = { regions: [], idx: -1, counts: [0, 0, 0] }; // regions = [{els, from, to, kind}]
// kind (semántica de diff): 0 = neutral crema (modificado), 1 = verde (agregado), 2 = rojo (borrado)
const MK_CLS = ['', 'hl-add', 'hl-del'];
const MK_SYM = ['~', '+', '−'];

function applyMarks(hl, opts) {
  marks.regions = []; marks.idx = -1; marks.counts = [0, 0, 0];
  const pill = $('stMarks');
  const ranges = Array.isArray(hl) ? hl : [];
  const lines = ranges.length ? code.querySelectorAll('.chroma .line') : [];
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
    marks.regions.push({ els, from, to, kind });
    marks.counts[kind] += els.length;
  }
  if (!marks.regions.length) { pill.textContent = ''; pill.classList.remove('on'); return; }
  updateMarksPill();
  // al abrir, llevar la vista a la primera zona (en recarga viva se conserva la posición)
  if (!opts || !opts.silent) {
    marks.idx = 0;
    requestAnimationFrame(() => { focusMark(false); updateProgress(); });
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

// ---- copiar todo --------------------------------------------------------
async function copyAll() {
  const lines = [...code.querySelectorAll('.chroma .cl')].map((el) => el.textContent);
  let text = lines.length ? lines.join('\n') : code.textContent;
  if (!text) { const pre = code.querySelector('pre'); text = pre ? pre.innerText : ''; }
  if (!text) return;
  try { await navigator.clipboard.writeText(text); }
  catch (e) {
    const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta);
    ta.select(); try { document.execCommand('copy'); } catch (_) {} ta.remove();
  }
  toast('Copiado', true);
}

// =========================================================================
// Recarga en vivo (SSE)
// =========================================================================
function watch(path) {
  if (es) { es.close(); es = null; }
  if (!path) return;
  try {
    es = new EventSource('/events?path=' + encodeURIComponent(path));
    es.onmessage = (ev) => { if (ev.data === 'reload') liveReload(); };
    es.onerror = () => {};
  } catch (e) {}
}
async function liveReload() {
  if (!current.path) return;
  const denom = Math.max(1, view.scrollHeight - view.clientHeight);
  const keep = view.scrollTop / denom;
  try {
    const r = await fetch('/render?path=' + encodeURIComponent(current.path));
    const j = await r.json();
    if (!j.ok) return;
    paint(j, { silent: true });
    requestAnimationFrame(() => {
      view.scrollTop = keep * Math.max(1, view.scrollHeight - view.clientHeight);
      updateProgress();
    });
    pulseLive();
  } catch (e) {}
}
function pulseLive() {
  const d = $('capLive'); d.classList.remove('pulse'); void d.offsetWidth; d.classList.add('pulse');
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
// Busqueda (CSS Custom Highlight API), sobre el código, excluyendo el gutter
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
function runFind(term) {
  if (!supportsHL) return;
  CSS.highlights.delete('cipher-find'); CSS.highlights.delete('cipher-find-current');
  find.matches = []; find.idx = -1;
  const q = term.trim().toLowerCase();
  if (!q) { updateFindCount(); return; }

  const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
      let p = node.parentElement;
      while (p && p !== code) {
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
  if (e.button !== 0 || e.target.closest('.winbtn') || e.target.closest('.tbtn')) return;
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
// Arrastrar y soltar (cualquier archivo)
// =========================================================================
window.addEventListener('dragover', (e) => { e.preventDefault(); body.classList.add('dragover'); });
window.addEventListener('dragleave', (e) => { if (!e.relatedTarget) body.classList.remove('dragover'); });
window.addEventListener('drop', async (e) => {
  e.preventDefault(); body.classList.remove('dragover');
  const f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
  if (!f) return;
  try { renderRawText(await f.text(), f.name); } catch (_) { toast('No se pudo leer'); }
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
// contador de selección vivo en la barra de estado (sólo selecciones dentro del código)
document.addEventListener('selectionchange', () => {
  const s = window.getSelection();
  let n = 0;
  if (s && s.rangeCount && !s.isCollapsed && code.contains(s.anchorNode)) n = s.toString().length;
  $('stSel').textContent = n > 0 ? 'sel ' + nf.format(n) : '';
});
window.addEventListener('resize', () => {
  body.classList.toggle('maximized', !isFs && window.innerWidth >= screen.availWidth - 6);
  updateProgress();
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
  // canal del daemon caliente: al reabrir, el host nos avisa por acá qué archivo mostrar
  try {
    const oe = new EventSource('/openevents');
    oe.onmessage = (ev) => { if (ev.data && ev.data !== current.path) { reveal(); openPath(ev.data); } };
  } catch (e) {}
  fetch('/api/initial').then((r) => r.json()).then(async (j) => {
    if (j && j.path) await openPath(j.path);
    reveal();
  }).catch(() => reveal());
}
boot();
