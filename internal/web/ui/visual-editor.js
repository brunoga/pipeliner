// ── visual pipeline editor ────────────────────────────────────────────────────

// Internal state
const ve = {
  plugins: [],          // [{name, phase, description, schema}] from /api/plugins
  model: {              // the current visual model
    variables: [],      // [{name, value}]
    tasks: [],          // [{name, schedule, plugins:[{name, config:{}}]}]
    activeTask: 0,
    selectedPlugin: -1,
  },
  autosync: false,
  dragSrcType: null,    // 'palette' | 'card'
  dragSrcIdx: -1,
};

const PHASE_ORDER = ['input','metainfo','filter','modify','output','learn','from'];

// ── view switching ──

let currentView = 'text';

function switchView(view) {
  if (view === currentView) return;
  if (view === 'visual' && !ve.plugins.length) {
    loadPalette().then(() => doSwitchView(view));
  } else {
    doSwitchView(view);
  }
}

function doSwitchView(view) {
  currentView = view;
  document.getElementById('view-text').style.display   = view === 'text'   ? '' : 'none';
  document.getElementById('view-visual').style.display = view === 'visual' ? '' : 'none';
  document.getElementById('view-btn-text').classList.toggle('active',   view === 'text');
  document.getElementById('view-btn-visual').classList.toggle('active', view === 'visual');
}

// ── palette ──

async function loadPalette() {
  try {
    const r = await fetch('/api/plugins');
    if (!r.ok) return;
    ve.plugins = await r.json();
    renderPalette('');
  } catch (e) { /* ignore */ }
}

function renderPalette(filter) {
  const body = document.getElementById('ve-palette-body');
  const q = filter.toLowerCase();
  const byPhase = {};
  for (const p of ve.plugins) {
    if (p.phase === 'from') continue; // from-plugins are sub-plugins, hide from palette
    if (q && !p.name.includes(q) && !p.description.toLowerCase().includes(q)) continue;
    (byPhase[p.phase] = byPhase[p.phase] || []).push(p);
  }
  const html = [];
  for (const phase of PHASE_ORDER) {
    const group = byPhase[phase];
    if (!group) continue;
    html.push(`<div class="ve-phase-header" data-phase="${phase}" onclick="togglePhaseGroup(this)">${phase}</div>`);
    html.push(`<div class="ve-phase-chips" id="ve-phase-${phase}">`);
    for (const p of group) {
      html.push(`<button class="ve-chip" draggable="true" title="${esc(p.description)}"
        data-plugin="${esc(p.name)}" data-phase="${esc(p.phase)}"
        ondragstart="paletteDragStart(event,${esc(JSON.stringify(p.name))})"
        onclick="appendPlugin(${esc(JSON.stringify(p.name))})">${esc(p.name)}</button>`);
    }
    html.push('</div>');
  }
  body.innerHTML = html.join('') || '<div style="color:var(--muted);font-size:12px;padding:8px 4px">No plugins match</div>';
}

function filterPalette(q) { renderPalette(q); }

function togglePhaseGroup(el) {
  const id = 've-phase-' + el.dataset.phase;
  const g = document.getElementById(id);
  if (g) g.style.display = g.style.display === 'none' ? '' : 'none';
}

// ── variables panel ──

function toggleVarsPanel() {
  const body = document.getElementById('ve-vars-body');
  const arrow = document.getElementById('ve-vars-arrow');
  const open = body.classList.toggle('open');
  arrow.textContent = open ? '▾' : '▸';
}

function addVariable() {
  ve.model.variables.push({name: '', value: ''});
  renderVars();
  if (!document.getElementById('ve-vars-body').classList.contains('open')) toggleVarsPanel();
}

function removeVariable(i) {
  ve.model.variables.splice(i, 1);
  renderVars();
  onModelChange();
}

function renderVars() {
  const rows = document.getElementById('ve-vars-rows');
  const count = ve.model.variables.length;
  document.getElementById('ve-vars-count').textContent = count ? `(${count})` : '';
  if (!count) { rows.innerHTML = ''; return; }
  rows.innerHTML = ve.model.variables.map((v, i) => `
    <div class="ve-var-row">
      <input class="ve-var-name" placeholder="name" value="${esc(v.name)}"
        oninput="ve.model.variables[${i}].name=this.value;onModelChange()">
      <span style="color:var(--muted);font-size:13px">=</span>
      <input class="ve-var-val" placeholder="value" value="${esc(v.value)}"
        oninput="ve.model.variables[${i}].value=this.value;onModelChange()">
      <button class="ve-var-del" onclick="removeVariable(${i})">×</button>
    </div>`).join('');
}

// ── task management ──

function addTask() {
  const idx = ve.model.tasks.length + 1;
  ve.model.tasks.push({name: 'task-' + idx, schedule: '', plugins: []});
  ve.model.activeTask = ve.model.tasks.length - 1;
  ve.model.selectedPlugin = -1;
  renderTaskTabs();
  renderCanvas();
  renderParamPanel();
}

function removeTask(i, e) {
  e.stopPropagation();
  if (ve.model.tasks.length <= 1 && !confirm('Remove the last task?')) return;
  ve.model.tasks.splice(i, 1);
  ve.model.activeTask = Math.min(ve.model.activeTask, ve.model.tasks.length - 1);
  ve.model.selectedPlugin = -1;
  renderTaskTabs();
  renderCanvas();
  renderParamPanel();
  onModelChange();
}

function switchTask(i) {
  ve.model.activeTask = i;
  ve.model.selectedPlugin = -1;
  renderTaskTabs();
  renderCanvas();
  renderParamPanel();
}

function renderTaskTabs() {
  const bar = document.getElementById('ve-task-bar');
  const tabs = ve.model.tasks.map((t, i) => `
    <button class="ve-task-tab${i === ve.model.activeTask ? ' active' : ''}" onclick="switchTask(${i})">
      ${esc(t.name)}
      <span class="ve-task-tab-close" onclick="removeTask(${i},event)">×</span>
    </button>`).join('');
  bar.innerHTML = tabs + '<button class="ve-add-task" onclick="addTask()">+ New task</button>';
  const t = ve.model.tasks[ve.model.activeTask];
  document.getElementById('ve-schedule').value = t ? t.schedule : '';
}

function onScheduleChange(val) {
  const t = ve.model.tasks[ve.model.activeTask];
  if (t) { t.schedule = val; onModelChange(); }
}

// ── canvas rendering ──

function renderCanvas() {
  const canvas = document.getElementById('ve-pipeline');
  const empty  = document.getElementById('ve-empty-hint');
  const t = ve.model.tasks[ve.model.activeTask];
  if (!t || !t.plugins.length) {
    canvas.innerHTML = '';
    empty.style.display = '';
    return;
  }
  empty.style.display = 'none';
  let html = dropZoneHTML(0);
  for (let i = 0; i < t.plugins.length; i++) {
    const p = t.plugins[i];
    const meta = ve.plugins.find(x => x.name === p.name) || {phase: '?'};
    const preview = configPreview(p.config);
    const sel = i === ve.model.selectedPlugin;
    html += `
      <div class="ve-node${sel ? ' selected' : ''}" data-idx="${i}" data-phase="${meta.phase}"
           onclick="selectPlugin(${i})"
           draggable="true"
           ondragstart="cardDragStart(event,${i})"
           ondragover="event.preventDefault()"
           ondrop="cardDrop(event,${i})">
        <div class="ve-node-phase"></div>
        <div class="ve-node-drag" title="Drag to reorder">⠿</div>
        <div class="ve-node-body">
          <div class="ve-node-name">${esc(p.name)}</div>
          <div class="ve-node-phase-label">${meta.phase}</div>
          ${preview ? `<div class="ve-node-preview">${esc(preview)}</div>` : ''}
        </div>
        <button class="ve-node-remove" title="Remove" onclick="event.stopPropagation();removePluginAt(${i})">×</button>
      </div>` + dropZoneHTML(i + 1);
  }
  canvas.innerHTML = html;
  // Attach drop-zone listeners
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
  if (!cfg) return '';
  const entries = Object.entries(cfg).slice(0, 2);
  return entries.map(([k, v]) => {
    const vs = Array.isArray(v) ? `[${v.slice(0,2).join(', ')}${v.length > 2 ? '…' : ''}]`
             : typeof v === 'string' ? (v.length > 30 ? v.slice(0,30) + '…' : v)
             : String(v);
    return `${k}: ${vs}`;
  }).join('  ');
}

// ── drag-and-drop ──

function paletteDragStart(e, name) {
  ve.dragSrcType = 'palette';
  e.dataTransfer.setData('text/plain', name);
  e.dataTransfer.effectAllowed = 'copy';
}

function cardDragStart(e, idx) {
  ve.dragSrcType = 'card';
  ve.dragSrcIdx = idx;
  e.dataTransfer.setData('text/plain', String(idx));
  e.dataTransfer.effectAllowed = 'move';
}

function dropOnZone(e, targetIdx) {
  e.preventDefault();
  document.querySelectorAll('.ve-drop-zone').forEach(el => el.classList.remove('drag-over'));
  const t = ve.model.tasks[ve.model.activeTask];
  if (!t) return;
  if (ve.dragSrcType === 'palette') {
    const name = e.dataTransfer.getData('text/plain');
    t.plugins.splice(targetIdx, 0, {name, config: {}});
    ve.model.selectedPlugin = targetIdx;
  } else if (ve.dragSrcType === 'card') {
    const src = ve.dragSrcIdx;
    const dst = targetIdx > src ? targetIdx - 1 : targetIdx;
    if (src !== dst) {
      const [item] = t.plugins.splice(src, 1);
      t.plugins.splice(dst, 0, item);
      ve.model.selectedPlugin = dst;
    }
  }
  renderCanvas();
  renderParamPanel();
  onModelChange();
}

function cardDrop(e, _idx) {
  // Handled by drop zones; suppress default browser behaviour.
  e.preventDefault();
}

// ── plugin operations ──

function appendPlugin(name) {
  const t = ve.model.tasks[ve.model.activeTask];
  if (!t) { addTask(); return appendPlugin(name); }
  t.plugins.push({name, config: {}});
  ve.model.selectedPlugin = t.plugins.length - 1;
  renderCanvas();
  renderParamPanel();
  onModelChange();
}

function selectPlugin(i) {
  ve.model.selectedPlugin = i;
  renderCanvas();
  renderParamPanel();
}

function removeSelectedPlugin() {
  removePluginAt(ve.model.selectedPlugin);
}

function removePluginAt(i) {
  const t = ve.model.tasks[ve.model.activeTask];
  if (!t) return;
  t.plugins.splice(i, 1);
  ve.model.selectedPlugin = Math.min(ve.model.selectedPlugin, t.plugins.length - 1);
  renderCanvas();
  renderParamPanel();
  onModelChange();
}

// ── param panel ──

function renderParamPanel() {
  const t = ve.model.tasks[ve.model.activeTask];
  const i = ve.model.selectedPlugin;
  const empty   = document.getElementById('ve-param-empty');
  const title   = document.getElementById('ve-param-title');
  const nameEl  = document.getElementById('ve-param-name');
  const phaseEl = document.getElementById('ve-param-phase');
  const body    = document.getElementById('ve-param-body');
  const footer  = document.getElementById('ve-param-footer');

  if (!t || i < 0 || i >= t.plugins.length) {
    empty.style.display = ''; title.style.display = 'none';
    body.innerHTML = ''; footer.style.display = 'none';
    return;
  }
  const p    = t.plugins[i];
  const meta = ve.plugins.find(x => x.name === p.name) || {phase: '?', schema: []};
  empty.style.display = 'none'; title.style.display = '';
  nameEl.textContent  = p.name;
  phaseEl.textContent = meta.phase;
  footer.style.display = '';

  if (meta.schema && meta.schema.length) {
    body.innerHTML = meta.schema.map(f => renderField(f, p.config)).join('');
    // Wire change listeners
    body.querySelectorAll('[data-field]').forEach(el => {
      el.addEventListener('change', () => collectParams(t.plugins[i], meta.schema, body));
      el.addEventListener('input',  () => collectParams(t.plugins[i], meta.schema, body));
    });
  } else {
    // Generic key-value editor
    body.innerHTML = renderGenericKV(p.config);
    wireGenericKV(body, p);
  }
}

function renderField(f, config) {
  const val = config[f.key];
  const id  = 've-f-' + f.key;
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
      widget = `<select data-field="${f.key}">${f.enum.map(v =>
        `<option${v === (val ?? f.default) ? ' selected' : ''}>${v}</option>`).join('')}</select>`;
      break;
    case 'list': {
      const items = Array.isArray(val) ? val : (val ? [String(val)] : []);
      widget = `<div class="ve-tag-list" data-field="${f.key}" data-type="list">${
        items.map((s, j) => `<span class="ve-tag">${esc(s)}<button class="ve-tag-del" onclick="removeTag(this,'${f.key}')">×</button></span>`).join('')
      }</div>
      <div style="display:flex;gap:4px">
        <input class="ve-tag-input" id="${id}-new" placeholder="add item…" onkeydown="tagKeydown(event,this,'${f.key}')">
        <button class="ve-add-kv" onclick="addTag('${id}-new','${f.key}')">Add</button>
      </div>`;
      break;
    }
    case 'pattern':
    case 'string':
    default:
      if (typeof (val ?? '') === 'object') {
        widget = `<textarea data-field="${f.key}" rows="3">${esc(JSON.stringify(val, null, 2))}</textarea>`;
      } else {
        widget = `<input type="text" data-field="${f.key}" value="${esc(String(val ?? ''))}" placeholder="${esc(String(f.default ?? f.hint ?? ''))}">`;
      }
  }
  return `<div class="ve-field">
    <div class="ve-field-label">${esc(f.key)}${f.required ? ' <span class="ve-field-required">*</span>' : ''}
      ${f.hint ? `<span class="ve-field-hint">— ${esc(f.hint)}</span>` : ''}
    </div>
    ${widget}
  </div>`;
}

function collectParams(plugin, schema, body) {
  for (const f of schema) {
    const el = body.querySelector(`[data-field="${f.key}"]`);
    if (!el) continue;
    if (f.type === 'bool') {
      plugin.config[f.key] = el.querySelector('input[type=checkbox]')?.checked ?? el.checked;
    } else if (f.type === 'int') {
      const v = parseInt(el.value, 10);
      if (!isNaN(v)) plugin.config[f.key] = v; else delete plugin.config[f.key];
    } else if (f.type === 'list') {
      // handled by addTag / removeTag
    } else {
      if (el.value !== '') plugin.config[f.key] = el.value;
      else delete plugin.config[f.key];
    }
  }
  renderCanvas();
  onModelChange();
}

function tagKeydown(e, input, field) {
  if (e.key === 'Enter') { e.preventDefault(); addTag(input.id, field); }
}
function addTag(inputId, field) {
  const input = document.getElementById(inputId);
  if (!input || !input.value.trim()) return;
  const t = ve.model.tasks[ve.model.activeTask];
  const p = t?.plugins[ve.model.selectedPlugin];
  if (!p) return;
  if (!Array.isArray(p.config[field])) p.config[field] = [];
  p.config[field].push(input.value.trim());
  input.value = '';
  renderParamPanel(); onModelChange();
}
function removeTag(btn, field) {
  const tag = btn.closest('.ve-tag');
  const list = tag?.closest('[data-type="list"]');
  const idx = [...list.querySelectorAll('.ve-tag')].indexOf(tag);
  const t = ve.model.tasks[ve.model.activeTask];
  const p = t?.plugins[ve.model.selectedPlugin];
  if (!p || !Array.isArray(p.config[field])) return;
  p.config[field].splice(idx, 1);
  tag.remove(); onModelChange();
}

function renderGenericKV(cfg) {
  const entries = Object.entries(cfg || {});
  let html = entries.map(([k, v], i) => `<div class="ve-kv-row">
    <input class="ve-kv-key" placeholder="key" value="${esc(k)}" data-kv-key="${i}">
    <input class="ve-kv-val" placeholder="value" value="${esc(typeof v === 'object' ? JSON.stringify(v) : String(v))}" data-kv-val="${i}">
    <button class="ve-kv-del" data-kv-del="${i}">×</button>
  </div>`).join('');
  html += '<button class="ve-add-kv" id="ve-kv-add">+ Add key</button>';
  return html;
}

function wireGenericKV(body, plugin) {
  const sync = () => {
    const cfg = {};
    body.querySelectorAll('.ve-kv-row').forEach(row => {
      const k = row.querySelector('[data-kv-key]')?.value.trim();
      const v = row.querySelector('[data-kv-val]')?.value;
      if (k) {
        try { cfg[k] = JSON.parse(v); } catch { cfg[k] = v; }
      }
    });
    plugin.config = cfg;
    renderCanvas();
    onModelChange();
  };
  body.addEventListener('input', sync);
  body.querySelectorAll('[data-kv-del]').forEach(btn => {
    btn.addEventListener('click', () => { btn.closest('.ve-kv-row').remove(); sync(); });
  });
  body.querySelector('#ve-kv-add')?.addEventListener('click', () => {
    const row = document.createElement('div');
    row.className = 've-kv-row';
    row.innerHTML = `<input class="ve-kv-key" placeholder="key">
      <input class="ve-kv-val" placeholder="value">
      <button class="ve-kv-del">×</button>`;
    row.querySelector('.ve-kv-del').addEventListener('click', () => { row.remove(); sync(); });
    body.querySelector('#ve-kv-add').before(row);
  });
}

// ── auto-sync ──

function setAutosync(on) {
  ve.autosync = on;
  if (on) visualToTextSync();
}

function onModelChange() {
  if (ve.autosync) {
    const star = visualToStarlark();
    document.getElementById('config-editor').value = star;
    syncHighlight();
  }
}

// ── sync: visual → text ──

function visualToTextSync() {
  const star = visualToStarlark();
  document.getElementById('config-editor').value = star;
  syncHighlight();
  setSyncNote('✓ Text updated');
}

function visualToStarlark() {
  const m = ve.model;
  const lines = [];
  if (m.variables.length) {
    for (const v of m.variables) {
      if (v.name) lines.push(`${v.name} = ${starLit(v.value)}`);
    }
    lines.push('');
  }
  for (let ti = 0; ti < m.tasks.length; ti++) {
    const t = m.tasks[ti];
    if (ti > 0) lines.push('');
    lines.push(`task(${starLit(t.name)},`);
    lines.push('    [');
    for (const p of t.plugins) {
      lines.push('        ' + pluginToStar(p) + ',');
    }
    lines.push('    ]' + (t.schedule ? `,\n    schedule=${starLit(t.schedule)}` : '') + ')');
  }
  return lines.join('\n') + (lines.length ? '\n' : '');
}

function pluginToStar(p) {
  const cfg = p.config || {};
  const keys = Object.keys(cfg).filter(k => cfg[k] !== '' && cfg[k] !== null && cfg[k] !== undefined);
  if (!keys.length) return `plugin(${starLit(p.name)})`;
  if (keys.includes('from') || keys.length > 3) {
    // Dict form for complex configs
    return `plugin(${starLit(p.name)}, {${keys.map(k => `${starLit(k)}: ${valToStar(cfg[k])}`).join(', ')}})`;
  }
  if (keys.length === 1) {
    return `plugin(${starLit(p.name)}, ${keys[0]}=${valToStar(cfg[keys[0]])})`;
  }
  return `plugin(${starLit(p.name)},\n               ${keys.map(k => `${k}=${valToStar(cfg[k])}`).join(',\n               ')})`;
}

function starLit(v) {
  if (typeof v !== 'string') return String(v);
  if (v.includes('\n')) return '"""' + v + '"""';
  return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

function valToStar(v) {
  if (v === null || v === undefined) return 'None';
  if (typeof v === 'boolean') return v ? 'True' : 'False';
  if (typeof v === 'number') return String(v);
  if (typeof v === 'string') return starLit(v);
  if (Array.isArray(v)) {
    if (v.every(x => typeof x === 'string')) return '[' + v.map(starLit).join(', ') + ']';
    return '[' + v.map(valToStar).join(', ') + ']';
  }
  if (typeof v === 'object') {
    return '{' + Object.entries(v).map(([k, val]) => `${starLit(k)}: ${valToStar(val)}`).join(', ') + '}';
  }
  return starLit(String(v));
}

// ── sync: text → visual ──

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
      const { error } = await r.json();
      setSyncNote('✗ ' + error.split('\n')[0]);
      return;
    }
    const { tasks } = await r.json();
    // Rebuild model from parsed config
    ve.model.tasks = Object.entries(tasks || {}).map(([name, t]) => ({
      name,
      schedule: t.schedule || '',
      plugins:  (t.plugins || []).map(p => ({name: p.name, config: p.config || {}})),
    }));
    ve.model.activeTask = 0;
    ve.model.selectedPlugin = -1;
    ve.model.variables = []; // can't recover variables from flat parse
    renderVars();
    renderTaskTabs();
    renderCanvas();
    renderParamPanel();
    setSyncNote('✓ Visual updated' + (ve.model.tasks.length ? '' : ' (no tasks found)'));
  } catch(e) {
    setSyncNote('✗ ' + String(e));
  }
}

function setSyncNote(msg) {
  const el = document.getElementById('ve-sync-note');
  if (el) el.textContent = msg;
}

// ── utility ──

function esc(s) {
  return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

