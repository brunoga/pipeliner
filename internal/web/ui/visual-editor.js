// ── DAG visual pipeline editor ────────────────────────────────────────────────
//
// Supports only DAG-style pipelines (input / process / merge / output / pipeline).
// Serialises to and from Starlark DAG syntax and keeps the text editor in sync.

const ve = {
  plugins:  [],         // [{name, role, description, schema, produces, requires}]
  syncing:  false,
  model: {
    name:     'my-pipeline',
    schedule: '',
    nodes:    [],       // [{id, plugin, config:{}, upstreams:[id…]}]
    nextId:   0,
    selected: null,     // selected node id or null
  },
  dragSrc: null,        // {type:'palette'|'card', plugin?:'', id?:''}
};

// ── view switching ────────────────────────────────────────────────────────────

let currentView = 'text';

function switchView(view) {
  if (view === currentView) return;
  if (view === 'visual') {
    const load = ve.plugins.length ? Promise.resolve() : loadPalette();
    load.then(() => { doSwitchView('visual'); textToVisualSync(); });
  } else {
    doSwitchView('text');
  }
}

function doSwitchView(view) {
  currentView = view;
  document.getElementById('view-text').style.display   = view === 'text'   ? '' : 'none';
  document.getElementById('view-visual').style.display = view === 'visual' ? '' : 'none';
  document.getElementById('view-btn-text').classList.toggle('active',   view === 'text');
  document.getElementById('view-btn-visual').classList.toggle('active', view === 'visual');
}

// ── palette ───────────────────────────────────────────────────────────────────

const ROLE_ORDER = ['source', 'processor', 'sink'];
const ROLE_LABEL = {source: 'Sources', processor: 'Processors', sink: 'Sinks'};

async function loadPalette() {
  try {
    const r = await fetch('/api/plugins');
    if (!r.ok) return;
    ve.plugins = await r.json();
    renderPalette('');
  } catch (_) {}
}

function renderPalette(filter) {
  const q = filter.toLowerCase();
  const byRole = {};
  for (const p of ve.plugins) {
    if (q && !p.name.includes(q) && !p.description.toLowerCase().includes(q)) continue;
    (byRole[p.role] = byRole[p.role] || []).push(p);
  }
  const html = [];
  for (const role of ROLE_ORDER) {
    const group = byRole[role];
    if (!group) continue;
    html.push(`<div class="ve-role-header" data-role="${role}" onclick="toggleRoleGroup(this)">${ROLE_LABEL[role]}</div>`);
    html.push(`<div class="ve-role-chips" id="ve-role-${role}">`);
    for (const p of group) {
      html.push(`<button class="ve-chip" data-role="${role}" draggable="true"
        title="${esc(p.description)}"
        ondragstart="paletteDragStart(event,${esc(JSON.stringify(p.name))})"
        onclick="addNodeFromPalette(${esc(JSON.stringify(p.name))})">${esc(p.name)}</button>`);
    }
    html.push('</div>');
  }
  document.getElementById('ve-palette-body').innerHTML =
    html.join('') || '<div style="color:var(--muted);font-size:12px;padding:8px 4px">No plugins match</div>';
}

function filterPalette(q) { renderPalette(q); }

function toggleRoleGroup(el) {
  const g = document.getElementById('ve-role-' + el.dataset.role);
  if (g) g.style.display = g.style.display === 'none' ? '' : 'none';
}

// ── node management ───────────────────────────────────────────────────────────

function genId(pluginName) {
  const base = pluginName.replace(/[^a-z0-9]/gi, '_').toLowerCase();
  return base + '_' + (ve.model.nextId++);
}

function addNodeFromPalette(pluginName) {
  const id = genId(pluginName);
  ve.model.nodes.push({id, plugin: pluginName, config: {}, upstreams: []});
  ve.model.selected = id;
  veRender();
  onModelChange();
}

function removeNode(id) {
  ve.model.nodes = ve.model.nodes.filter(n => n.id !== id);
  for (const n of ve.model.nodes) n.upstreams = n.upstreams.filter(u => u !== id);
  if (ve.model.selected === id) ve.model.selected = null;
  veRender();
  onModelChange();
}

function selectNode(id) {
  ve.model.selected = (ve.model.selected === id) ? null : id;
  renderCanvas();
  renderParamPanel();
}

// ── pipeline name / schedule ──────────────────────────────────────────────────

function onPipelineName(val) { ve.model.name = val; onModelChange(); }
function onScheduleChange(val) { ve.model.schedule = val; onModelChange(); }

// ── canvas ────────────────────────────────────────────────────────────────────

function veRender() { renderCanvas(); renderParamPanel(); }

function renderCanvas() {
  const m = ve.model;
  document.getElementById('ve-pipeline-name').value = m.name;
  document.getElementById('ve-schedule').value       = m.schedule;

  const canvas    = document.getElementById('ve-pipeline');
  const emptyHint = document.getElementById('ve-empty-hint');

  if (!m.nodes.length) { canvas.innerHTML = ''; emptyHint.style.display = ''; return; }
  emptyHint.style.display = 'none';

  let html = dropZoneHTML(0);
  for (let i = 0; i < m.nodes.length; i++) {
    const n    = m.nodes[i];
    const meta = pluginMeta(n.plugin) || {role: 'processor'};
    const role = meta.role;
    const sel  = n.id === m.selected;
    const upLabels = n.upstreams.length
      ? n.upstreams.map(u => `<code>${esc(u)}</code>`).join(' + ')
      : '';
    const warns   = fieldWarnings(n);
    const preview = configPreview(n.config);

    html += `
      <div class="ve-node${sel ? ' selected' : ''}" data-role="${role}"
           onclick="selectNode(${esc(JSON.stringify(n.id))})"
           draggable="true"
           ondragstart="cardDragStart(event,${esc(JSON.stringify(n.id))})"
           ondragover="event.preventDefault()"
           ondrop="cardDrop(event,${i})">
        <div class="ve-node-role-bar"></div>
        <div class="ve-node-drag" title="Drag to reorder">⠿</div>
        <div class="ve-node-body">
          <div class="ve-node-header">
            <span class="ve-node-name">${esc(n.plugin)}</span>
            <span class="ve-node-role-badge ve-role-${role}">${role}</span>
          </div>
          ${upLabels  ? `<div class="ve-node-from">← ${upLabels}</div>` : ''}
          ${preview   ? `<div class="ve-node-preview">${esc(preview)}</div>` : ''}
          ${warns.length ? `<div class="ve-node-warn">⚠ ${esc(warns[0])}</div>` : ''}
        </div>
        <button class="ve-node-remove" title="Remove"
          onclick="event.stopPropagation();removeNode(${esc(JSON.stringify(n.id))})">×</button>
      </div>` + dropZoneHTML(i + 1);
  }
  canvas.innerHTML = html;

  canvas.querySelectorAll('.ve-drop-zone').forEach(el => {
    el.addEventListener('dragover', e => { e.preventDefault(); el.classList.add('drag-over'); });
    el.addEventListener('dragleave', () => el.classList.remove('drag-over'));
    el.addEventListener('drop', e => dropOnZone(e, +el.dataset.idx));
  });
}

function dropZoneHTML(idx) {
  return `<div class="ve-drop-zone" data-idx="${idx}"></div>`;
}

function configPreview(cfg) {
  const entries = Object.entries(cfg || {}).slice(0, 2);
  return entries.map(([k, v]) => {
    const vs = Array.isArray(v) ? `[${v.slice(0,2).join(', ')}${v.length>2?'…':''}]`
             : typeof v === 'string' ? (v.length > 28 ? v.slice(0,28)+'…' : v) : String(v);
    return `${k}: ${vs}`;
  }).join('  ');
}

function fieldWarnings(node) {
  const meta = pluginMeta(node.plugin);
  if (!meta?.requires?.length) return [];
  const produced = allProducedUpstream(node.id);
  return meta.requires.filter(f => !produced.has(f))
    .map(f => `requires "${f}" — add ${f}-producing node upstream`);
}

function allProducedUpstream(nodeId) {
  const produced = new Set();
  const visited  = new Set();
  const queue    = [...(ve.model.nodes.find(n => n.id === nodeId)?.upstreams || [])];
  while (queue.length) {
    const id = queue.shift();
    if (visited.has(id)) continue;
    visited.add(id);
    const n    = ve.model.nodes.find(x => x.id === id);
    const meta = n ? pluginMeta(n.plugin) : null;
    if (meta?.produces) meta.produces.forEach(f => produced.add(f));
    if (n) n.upstreams.forEach(u => queue.push(u));
  }
  return produced;
}

function pluginMeta(name) {
  return ve.plugins.find(p => p.name === name) || null;
}

// ── param panel ───────────────────────────────────────────────────────────────

function renderParamPanel() {
  const m     = ve.model;
  const empty = document.getElementById('ve-param-empty');
  const title = document.getElementById('ve-param-title');
  const nameEl = document.getElementById('ve-param-name');
  const roleEl = document.getElementById('ve-param-phase');
  const body   = document.getElementById('ve-param-body');
  const footer = document.getElementById('ve-param-footer');

  const node = m.selected ? m.nodes.find(n => n.id === m.selected) : null;
  if (!node) {
    empty.style.display = ''; title.style.display = 'none';
    body.innerHTML = ''; footer.style.display = 'none';
    return;
  }

  const meta = pluginMeta(node.plugin) || {role: 'processor', schema: [], produces: [], requires: []};
  empty.style.display = 'none'; title.style.display = '';
  nameEl.textContent = node.plugin;
  roleEl.textContent = meta.role;
  footer.style.display = '';

  const html = [];

  // Upstream connections.
  if (meta.role !== 'source') {
    const others = m.nodes.filter(n => n.id !== node.id);
    html.push(`<div class="ve-field"><div class="ve-field-label">Upstreams (from_=)</div>`);
    if (!others.length) {
      html.push('<div style="color:var(--muted);font-size:12px;margin-top:4px">Add source nodes first</div>');
    } else {
      for (const other of others) {
        const checked   = node.upstreams.includes(other.id);
        const otherMeta = pluginMeta(other.plugin);
        html.push(`<label class="ve-upstream-row">
          <input type="checkbox" ${checked ? 'checked' : ''}
            onchange="toggleUpstream(${esc(JSON.stringify(node.id))},${esc(JSON.stringify(other.id))},this.checked)">
          <code class="ve-upstream-id">${esc(other.id)}</code>
          <span class="ve-node-role-badge ve-role-${otherMeta?.role||''}" style="font-size:9px">${otherMeta?.role||''}</span>
        </label>`);
      }
    }
    html.push('</div>');
  }

  // Produces / Requires info.
  if (meta.produces?.length || meta.requires?.length) {
    html.push('<div class="ve-field-sep"></div>');
    if (meta.produces?.length)
      html.push(`<div class="ve-field-hint-block"><b>Produces:</b> ${meta.produces.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
    if (meta.requires?.length)
      html.push(`<div class="ve-field-hint-block"><b>Requires:</b> ${meta.requires.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
  }

  // Field validation warnings.
  const warns = fieldWarnings(node);
  if (warns.length)
    html.push(`<div class="ve-conn-warn">${warns.map(w => `⚠ ${esc(w)}`).join('<br>')}</div>`);

  // Plugin config fields.
  html.push('<div class="ve-field-sep"></div>');
  if (meta.schema?.length) {
    for (const f of meta.schema) html.push(renderField(f, node.config));
  } else {
    html.push(renderGenericKV(node.config));
  }

  body.innerHTML = html.join('');

  if (meta.schema?.length) {
    body.querySelectorAll('[data-field]').forEach(el => {
      el.addEventListener('change', () => collectParams(node, meta.schema, body));
      el.addEventListener('input',  () => collectParams(node, meta.schema, body));
    });
  } else {
    wireGenericKV(body, node);
  }
}

function toggleUpstream(nodeId, upId, checked) {
  const node = ve.model.nodes.find(n => n.id === nodeId);
  if (!node) return;
  if (checked && !node.upstreams.includes(upId)) node.upstreams.push(upId);
  else if (!checked) node.upstreams = node.upstreams.filter(u => u !== upId);
  renderCanvas();
  onModelChange();
}

// ── field widgets ─────────────────────────────────────────────────────────────

function renderField(f, config) {
  const val = config[f.key];
  let widget = '';
  switch (f.type) {
    case 'bool':
      widget = `<label style="display:flex;align-items:center;gap:6px;cursor:pointer">
        <input type="checkbox" data-field="${f.key}" ${val ? 'checked' : ''}> ${val ? 'true' : 'false'}</label>`;
      break;
    case 'int':
      widget = `<input type="number" data-field="${f.key}" value="${val ?? (f.default ?? '')}" placeholder="${f.default ?? ''}">`;
      break;
    case 'enum':
      widget = `<select data-field="${f.key}">${(f.enum||[]).map(v =>
        `<option${v===(val??f.default)?' selected':''}>${v}</option>`).join('')}</select>`;
      break;
    case 'list': {
      const items = Array.isArray(val) ? val : (val ? [String(val)] : []);
      const fid   = 'vef-' + f.key;
      widget = `<div class="ve-tag-list" data-field="${f.key}" data-type="list">${
        items.map(s => `<span class="ve-tag">${esc(s)}<button class="ve-tag-del" onclick="removeTag(this,'${f.key}')">×</button></span>`).join('')
      }</div><div style="display:flex;gap:4px">
        <input class="ve-tag-input" id="${fid}" placeholder="add item…" onkeydown="if(event.key==='Enter'){event.preventDefault();addTag('${fid}','${f.key}')}">
        <button class="ve-add-kv" onclick="addTag('${fid}','${f.key}')">Add</button></div>`;
      break;
    }
    default:
      widget = `<input type="text" data-field="${f.key}" value="${esc(String(val ?? ''))}" placeholder="${esc(String(f.default ?? f.hint ?? ''))}">`;
  }
  return `<div class="ve-field">
    <div class="ve-field-label">${esc(f.key)}${f.required ? ' <span class="ve-field-required">*</span>' : ''}
      ${f.hint ? `<span class="ve-field-hint">— ${esc(f.hint)}</span>` : ''}</div>${widget}</div>`;
}

function collectParams(node, schema, body) {
  for (const f of schema) {
    const el = body.querySelector(`[data-field="${f.key}"]`);
    if (!el) continue;
    if (f.type === 'bool')     node.config[f.key] = el.querySelector('input[type=checkbox]')?.checked ?? el.checked;
    else if (f.type === 'int') { const v=parseInt(el.value,10); if(!isNaN(v)) node.config[f.key]=v; else delete node.config[f.key]; }
    else if (f.type !== 'list') { if(el.value!=='') node.config[f.key]=el.value; else delete node.config[f.key]; }
  }
  renderCanvas(); onModelChange();
}

function addTag(inputId, field) {
  const input = document.getElementById(inputId);
  if (!input || !input.value.trim()) return;
  const node = ve.model.nodes.find(n => n.id === ve.model.selected);
  if (!node) return;
  if (!Array.isArray(node.config[field])) node.config[field] = [];
  node.config[field].push(input.value.trim());
  input.value = '';
  renderParamPanel(); onModelChange();
}

function removeTag(btn, field) {
  const tag  = btn.closest('.ve-tag');
  const list = tag?.closest('[data-type="list"]');
  const idx  = [...list.querySelectorAll('.ve-tag')].indexOf(tag);
  const node = ve.model.nodes.find(n => n.id === ve.model.selected);
  if (!node || !Array.isArray(node.config[field])) return;
  node.config[field].splice(idx, 1);
  tag.remove(); onModelChange();
}

function renderGenericKV(cfg) {
  const entries = Object.entries(cfg || {});
  return entries.map(([k, v], i) => `<div class="ve-kv-row">
    <input class="ve-kv-key" placeholder="key" value="${esc(k)}" data-kv-key="${i}">
    <input class="ve-kv-val" placeholder="value" value="${esc(typeof v==='object'?JSON.stringify(v):String(v))}" data-kv-val="${i}">
    <button class="ve-kv-del" data-kv-del="${i}">×</button></div>`).join('')
    + '<button class="ve-add-kv" id="ve-kv-add">+ Add key</button>';
}

function wireGenericKV(body, node) {
  const sync = () => {
    const cfg = {};
    body.querySelectorAll('.ve-kv-row').forEach(row => {
      const k = row.querySelector('[data-kv-key]')?.value.trim();
      const v = row.querySelector('[data-kv-val]')?.value;
      if (k) { try { cfg[k] = JSON.parse(v); } catch { cfg[k] = v; } }
    });
    node.config = cfg; renderCanvas(); onModelChange();
  };
  body.addEventListener('input', sync);
  body.querySelectorAll('[data-kv-del]').forEach(btn =>
    btn.addEventListener('click', () => { btn.closest('.ve-kv-row').remove(); sync(); }));
  body.querySelector('#ve-kv-add')?.addEventListener('click', () => {
    const row = document.createElement('div');
    row.className = 've-kv-row';
    row.innerHTML = `<input class="ve-kv-key" placeholder="key">
      <input class="ve-kv-val" placeholder="value"><button class="ve-kv-del">×</button>`;
    row.querySelector('.ve-kv-del').addEventListener('click', () => { row.remove(); sync(); });
    body.querySelector('#ve-kv-add').before(row);
  });
}

// ── drag and drop ─────────────────────────────────────────────────────────────

function paletteDragStart(e, name) {
  ve.dragSrc = {type: 'palette', plugin: name};
  e.dataTransfer.setData('text/plain', name);
  e.dataTransfer.effectAllowed = 'copy';
}

function cardDragStart(e, id) {
  ve.dragSrc = {type: 'card', id};
  e.dataTransfer.setData('text/plain', id);
  e.dataTransfer.effectAllowed = 'move';
}

function dropOnZone(e, targetIdx) {
  e.preventDefault();
  document.querySelectorAll('.ve-drop-zone').forEach(el => el.classList.remove('drag-over'));
  if (!ve.dragSrc) return;
  if (ve.dragSrc.type === 'palette') {
    const id = genId(ve.dragSrc.plugin);
    ve.model.nodes.splice(targetIdx, 0, {id, plugin: ve.dragSrc.plugin, config: {}, upstreams: []});
    ve.model.selected = id;
  } else if (ve.dragSrc.type === 'card') {
    const src = ve.model.nodes.findIndex(n => n.id === ve.dragSrc.id);
    const dst = targetIdx > src ? targetIdx - 1 : targetIdx;
    if (src !== dst) { const [item] = ve.model.nodes.splice(src, 1); ve.model.nodes.splice(dst, 0, item); }
  }
  ve.dragSrc = null;
  veRender(); onModelChange();
}

function cardDrop(e) { e.preventDefault(); }

// ── Starlark serialisation ────────────────────────────────────────────────────

function dagToStarlark() {
  const m = ve.model;
  const lines = [];
  for (const n of m.nodes) {
    const role = pluginMeta(n.plugin)?.role || 'processor';
    const cfgKw  = configToKwargs(n.config);
    const fromStr = upstreamsStr(n.upstreams);
    if (role === 'source') {
      lines.push(`${n.id} = input(${[starLit(n.plugin), cfgKw].filter(Boolean).join(', ')})`);
    } else if (role === 'processor') {
      const parts = [starLit(n.plugin)];
      if (fromStr) parts.push(`from_=${fromStr}`);
      if (cfgKw)   parts.push(cfgKw);
      lines.push(`${n.id} = process(${parts.join(', ')})`);
    } else {
      const parts = [starLit(n.plugin)];
      if (fromStr) parts.push(`from_=${fromStr}`);
      if (cfgKw)   parts.push(cfgKw);
      lines.push(`output(${parts.join(', ')})`);
    }
  }
  lines.push('');
  const schedArg = m.schedule ? `, schedule=${starLit(m.schedule)}` : '';
  lines.push(`pipeline(${starLit(m.name)}${schedArg})`);
  return lines.join('\n') + '\n';
}

function upstreamsStr(ups) {
  if (!ups?.length) return '';
  return ups.length === 1 ? ups[0] : `merge(${ups.join(', ')})`;
}

function configToKwargs(cfg) {
  const keys = Object.keys(cfg || {}).filter(k => cfg[k] !== '' && cfg[k] != null);
  return keys.map(k => `${k}=${valToStar(cfg[k])}`).join(', ');
}

function starLit(v) {
  if (typeof v !== 'string') return String(v);
  if (v.includes('\n')) return '"""' + v + '"""';
  return '"' + v.replace(/\\/g,'\\\\').replace(/"/g,'\\"') + '"';
}

function valToStar(v) {
  if (v === null || v === undefined) return 'None';
  if (typeof v === 'boolean') return v ? 'True' : 'False';
  if (typeof v === 'number')  return String(v);
  if (typeof v === 'string')  return starLit(v);
  if (Array.isArray(v))       return '[' + v.map(valToStar).join(', ') + ']';
  if (typeof v === 'object')  return '{' + Object.entries(v).map(([k,val])=>`${starLit(k)}: ${valToStar(val)}`).join(', ') + '}';
  return starLit(String(v));
}

// ── model change → sync to text editor ───────────────────────────────────────

function onModelChange() {
  if (ve.syncing) return;
  document.getElementById('config-editor').value = dagToStarlark();
  syncHighlight();
}

// ── text → visual sync ────────────────────────────────────────────────────────

async function textToVisualSync() {
  const content = document.getElementById('config-editor').value;
  setSyncNote('Parsing…');
  try {
    const r = await fetch('/api/config/parse', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content}),
    });
    if (r.status === 422) {
      const {error} = await r.json();
      setSyncNote('✗ ' + error.split('\n')[0]);
      return;
    }
    const data   = await r.json();
    const graphs  = data.graphs || {};
    const entries = Object.entries(graphs);

    ve.syncing = true;
    if (!entries.length) {
      ve.model.nodes    = [];
      ve.model.selected = null;
      ve.syncing = false;
  veRender();
      setSyncNote('No DAG pipeline found — add nodes from the palette or write pipeline() in the text editor');
      return;
    }

    const [name, graph] = entries[0];
    ve.model.name     = name;
    ve.model.schedule = graph.schedule || '';
    ve.model.nodes    = (graph.nodes || []).map(n => ({
      id: n.id, plugin: n.plugin, config: n.config || {}, upstreams: n.upstreams || [],
    }));
    ve.model.nextId   = ve.model.nodes.reduce((max, n) => {
      const m = n.id.match(/_(\d+)$/);
      return m ? Math.max(max, parseInt(m[1]) + 1) : max;
    }, 0);
    ve.model.selected = null;
    ve.syncing = false;
  veRender();
    setSyncNote(entries.length > 1 ? `Showing first pipeline (${entries.length} total in config)` : '');
  } catch (e) {
    ve.syncing = false;
    setSyncNote('✗ ' + String(e));
  }
}

function setSyncNote(msg) {
  const el = document.getElementById('ve-sync-note');
  if (el) el.textContent = msg;
}

// ── utility ───────────────────────────────────────────────────────────────────

function esc(s) {
  return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
