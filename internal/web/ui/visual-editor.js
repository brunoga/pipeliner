// ── DAG visual pipeline editor ────────────────────────────────────────────────
//
// Supports only DAG-style pipelines (input / process / merge / output / pipeline).
// Serialises to and from Starlark DAG syntax and keeps the text editor in sync.

const NODE_W = 200;  // node card width (px)
const NODE_H = 80;   // fallback node height for edge midpoints

const ve = {
  plugins:          [],   // [{name, role, description, schema, produces, requires}]
  userFunctions:    {},   // {funcName: {name, role, description, params, _sourceText}}
  syncing:          false,
  // All loaded pipelines. ve.model is a live alias for ve.graphs[ve.activeGraph].
  graphs:           [{name: 'my-pipeline', schedule: '', nodes: []}],
  nextId:           0,
  activeGraph:      0,
  selectedNodeId:   null,
  selectedNodeIds:  new Set(), // multi-select for Extract to Function
  clipboard:        null,      // {nodes:[{plugin,config,comment,origId,relX,relY}], edges:[{from,to}]}
  dragSrc:          null,      // {type:'palette', plugin:''}
  fnEditor:         {active: false}, // function editing mode state
  get model() { return this.graphs[this.activeGraph] || this.graphs[0]; },
};

let ve_canvasInited  = false;
let ve_zoom          = 1.0;
let ve_dragging      = null;   // truthy while a node is being moved
let ve_connecting    = null;   // {srcId, curX, curY} while drawing a live edge
let ve_searchConnecting  = null;   // {discoverNodeId, curX, curY} while drawing a search edge
let ve_listConnecting    = null;   // {parentNodeId, curX, curY} while drawing a list edge
let ve_panX          = 0;      // canvas pan offset (screen pixels)
let ve_panY          = 0;

// ── helpers ───────────────────────────────────────────────────────────────────

function activeG() { return ve.graphs[ve.activeGraph] || ve.graphs[0]; }

function findNode(nodeId) {
  for (const g of ve.graphs) {
    const n = g.nodes.find(n => n.id === nodeId);
    if (n) return n;
  }
  return null;
}

function findNodeGraph(nodeId) {
  return ve.graphs.findIndex(g => g.nodes.some(n => n.id === nodeId));
}

// Return the vertical midpoint of a rendered node (falls back to NODE_H/2).
function nodeMidY(nodeId, nodeY) {
  const el = document.querySelector(`.ve-node[data-id="${nodeId}"]`);
  return (nodeY ?? 0) + (el ? el.offsetHeight / 2 : NODE_H / 2);
}

// Return the bottom Y of a rendered node.
function nodeBottomY(nodeId, nodeY) {
  const el = document.querySelector(`.ve-node[data-id="${nodeId}"]`);
  return (nodeY ?? 0) + (el ? el.offsetHeight : NODE_H);
}

// Disconnect a search-connected node from its parent discover node.
function disconnectSearch(discoverNodeId, searchNodeId) {
  const disc = findNode(discoverNodeId);
  const sn   = findNode(searchNodeId);
  if (disc) disc.searchNodeIds = (disc.searchNodeIds || []).filter(id => id !== searchNodeId);
  if (sn)  { sn.isSearchNode = false; delete sn.searchParentId; }
  veRender();
  onModelChange();
}

function disconnectList(parentNodeId, listNodeId) {
  const parent = findNode(parentNodeId);
  const ln     = findNode(listNodeId);
  if (parent) parent.listNodeIds = (parent.listNodeIds || []).filter(id => id !== listNodeId);
  if (ln)  { ln.isListNode = false; delete ln.listParentId; }
  veRender();
  onModelChange();
}

// ── view switching ─────────────────────────────────────────────────────────────

let currentView = 'text';

function switchView(view) {
  if (view === currentView) return;
  if (view === 'visual') {
    const load = ve.plugins.length ? Promise.resolve() : loadPalette();
    load.then(() => {
      doSwitchView('visual');
      if (!ve_canvasInited) { initCanvasEvents(); ve_canvasInited = true; }
      textToVisualSync();
    });
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
  // Re-measure the textarea now that it is visible so the highlight overlay
  // gets the correct width (it would be 0 if syncHighlight ran while hidden).
  if (view === 'text' && typeof syncHighlight === 'function') syncHighlight();
  if (view === 'visual') fitVisualEditor();
}

// ── palette ───────────────────────────────────────────────────────────────────

// Track the last disabled state so we only re-render the palette when it changes.
let ve_paletteWasDisabled = null;

function syncPaletteState() {
  if (!ve.plugins.length) return; // palette not loaded yet
  const disabled = ve.graphs.length === 0;
  if (disabled === ve_paletteWasDisabled) return; // no change
  ve_paletteWasDisabled = disabled;
  renderPalette(document.getElementById('ve-search')?.value ?? '');
}

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
  const body = document.getElementById('ve-palette-body');
  if (!body) return;

  // When no pipelines exist, show a hint and grey out the chip list.
  if (ve.graphs.length === 0) {
    const q = filter.toLowerCase();
    const byRole = {};
    for (const p of ve.plugins) {
      if (q && !p.name.includes(q) && !p.description.toLowerCase().includes(q)) continue;
      (byRole[p.role] = byRole[p.role] || []).push(p);
    }
    const chipHtml = [];
    for (const role of ROLE_ORDER) {
      const group = byRole[role];
      if (!group) continue;
      chipHtml.push(`<div class="ve-role-header" data-role="${role}">${ROLE_LABEL[role]}</div>`);
      chipHtml.push(`<div class="ve-role-chips" id="ve-role-${role}">`);
      for (const p of group) {
        chipHtml.push(`<button class="ve-chip" data-role="${role}" disabled title="${esc(p.description)}">${esc(p.name)}</button>`);
      }
      chipHtml.push('</div>');
    }
    body.innerHTML = chipHtml.join(''); // chips visible but disabled; toolbar has the add button
    return;
  }

  const q = filter.toLowerCase();
  const html = [];

  // User functions section (shown first when any are defined).
  const userFuncList = Object.values(ve.userFunctions)
    .filter(fd => !q || fd.name.includes(q) || (fd.description||'').toLowerCase().includes(q));
  if (userFuncList.length) {
    html.push(`<div class="ve-role-header" onclick="toggleRoleGroup(this)">Functions</div>`);
    html.push(`<div class="ve-role-chips">`);
    for (const fd of userFuncList) {
      html.push(`<span class="ve-chip-fn-wrap">
        <button class="ve-chip ve-chip-fn" data-role="${fd.role}" draggable="true"
          title="${esc(fd.description || fd.name)}"
          ondragstart="paletteDragStart(event,${esc(JSON.stringify(fd.name))})"
          onclick="addNodeFromPalette(${esc(JSON.stringify(fd.name))})">
          ${esc(fd.name)}<span class="ve-chip-fn-badge">fn</span></button
        ><button class="ve-chip-fn-edit" title="Edit function body"
          onclick="openFunctionEditor(${esc(JSON.stringify(fd.name))})">✏</button
        ><button class="ve-chip-fn-remove" title="Expand and remove function"
          onclick="expandAndRemoveFunction(${esc(JSON.stringify(fd.name))})">×</button>
      </span>`);
    }
    html.push('</div>');
  }

  const byRole = {};
  for (const p of ve.plugins) {
    if (q && !p.name.includes(q) && !p.description.toLowerCase().includes(q)) continue;
    (byRole[p.role] = byRole[p.role] || []).push(p);
  }
  for (const role of ROLE_ORDER) {
    const group = byRole[role];
    if (!group) continue;
    html.push(`<div class="ve-role-header" data-role="${role}" onclick="toggleRoleGroup(this)">${ROLE_LABEL[role]}</div>`);
    html.push(`<div class="ve-role-chips" id="ve-role-${role}">`);
    for (const p of group) {
      const searchBadge = p.is_search_plugin ? ' <span class="ve-chip-search-badge">search</span>'
                        : p.is_list_plugin  ? ' <span class="ve-chip-list-badge">list</span>' : '';
      const extraCls = p.is_search_plugin ? ' ve-chip-search' : p.is_list_plugin ? ' ve-chip-list' : '';
      const extraTip = p.is_search_plugin ? '\n(drag onto a discover node\'s search port to use as a search backend)'
                     : p.is_list_plugin   ? '\n(drag onto a series/movies node\'s list port as a list source)' : '';
      html.push(`<button class="ve-chip${extraCls}" data-role="${role}" draggable="true"
        title="${esc(p.description)}${extraTip}"
        ondragstart="paletteDragStart(event,${esc(JSON.stringify(p.name))})"
        onclick="addNodeFromPalette(${esc(JSON.stringify(p.name))})">${esc(p.name)}${searchBadge}</button>`);
    }
    html.push('</div>');
  }
  body.innerHTML =
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
  return base + '_' + (ve.nextId++);
}

function addNodeFromPalette(pluginName) {
  const g = activeG();
  if (!g) return; // palette is disabled when no pipelines exist
  const id     = genId(pluginName);
  const {x, y} = newNodePos(g);
  const fd     = ve.userFunctions[pluginName];
  // Pre-populate call args from function param defaults so the call site has
  // the right initial values; mark the node as a function call so dagToStarlark
  // emits  var = funcname(...)  instead of  var = input("funcname", ...).
  const config = {};
  if (fd) {
    for (const p of (fd.params || [])) {
      if (p.default != null) {
        config[p.key] = p.default;
      } else {
        // Required param with no default: seed with a type-appropriate empty
        // value so the call site is syntactically valid from the start.
        config[p.key] = emptyForType(p.type);
      }
    }
  }
  g.nodes.push({
    id, plugin: pluginName, config, upstreams: [], x, y, comment: '',
    searchNodeIds: [], listNodeIds: [],
    ...(fd ? {isFunctionCall: true, funcCallKey: id} : {}),
  });
  ve.selectedNodeId = id;
  clearMultiSelect();
  veRender();
  onModelChange();
}

function newNodePos(g) {
  const body = document.getElementById('ve-canvas-body');
  const nodes = g?.nodes || [];
  if (!body || !body.clientWidth) {
    const s = nodes.length * 20;
    return {x: 60 + s, y: 60 + s};
  }
  const s = (nodes.length * 24) % 120;
  return {
    x: Math.max(20, body.scrollLeft / ve_zoom + body.clientWidth  / ve_zoom / 2 - NODE_W / 2 + s),
    y: Math.max(20, body.scrollTop  / ve_zoom + body.clientHeight / ve_zoom / 2 - NODE_H / 2 + s),
  };
}

function removeNode(id) {
  if (findNode(id)?.isUpstreamPseudo) return; // pseudo-node is permanent
  for (const g of ve.graphs) {
    const idx = g.nodes.findIndex(n => n.id === id);
    if (idx < 0) continue;
    const [removed] = g.nodes.splice(idx, 1);
    // Clean up upstreams and search/list references.
    for (const n of g.nodes) {
      n.upstreams      = (n.upstreams     || []).filter(u => u !== id);
      n.searchNodeIds  = (n.searchNodeIds || []).filter(u => u !== id);
      n.listNodeIds    = (n.listNodeIds   || []).filter(u => u !== id);
    }
    // If this was a search/list node, remove its parent connection.
    if (removed.searchParentId) {
      const parent = g.nodes.find(n => n.id === removed.searchParentId);
      if (parent) parent.searchNodeIds = (parent.searchNodeIds || []).filter(u => u !== id);
    }
    if (removed.listParentId) {
      const parent = g.nodes.find(n => n.id === removed.listParentId);
      if (parent) parent.listNodeIds = (parent.listNodeIds || []).filter(u => u !== id);
    }
    break;
  }
  if (ve.selectedNodeId === id) ve.selectedNodeId = null;
  veRender();
  onModelChange();
}

// selectNode: toggle selection WITHOUT rebuilding DOM (keeps drag div ref valid).
// Clears any multi-selection when called (single-click behaviour).
function selectNode(id) {
  clearMultiSelect();
  const prev = ve.selectedNodeId;
  ve.selectedNodeId = (ve.selectedNodeId === id) ? null : id;

  if (ve.selectedNodeId) {
    const gi = findNodeGraph(ve.selectedNodeId);
    if (gi >= 0) ve.activeGraph = gi;
  }

  // Just toggle CSS class — no full DOM rebuild.
  if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
  if (ve.selectedNodeId) document.querySelector(`.ve-node[data-id="${ve.selectedNodeId}"]`)?.classList.add('selected');

  renderEdges();
  renderParamPanel();
}

// toggleMultiSelect: Cmd/Ctrl+click adds/removes a node from the multi-selection
// used for "Extract to function". The param panel and extract button are updated.
function toggleMultiSelect(id) {
  const n = findNode(id);
  if (!n || n.isSearchNode || n.isListNode) return;
  if (ve.selectedNodeIds.has(id)) {
    ve.selectedNodeIds.delete(id);
  } else {
    ve.selectedNodeIds.add(id);
  }
  // Keep single-selection in sync: show the last toggled node in the param panel
  // only when the multi-selection is empty; otherwise clear it.
  if (ve.selectedNodeIds.size === 0) {
    ve.selectedNodeId = null;
  } else {
    const prev = ve.selectedNodeId;
    ve.selectedNodeId = id;
    if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
    if (ve.selectedNodeId) document.querySelector(`.ve-node[data-id="${ve.selectedNodeId}"]`)?.classList.add('selected');
  }
  document.querySelector(`.ve-node[data-id="${id}"]`)?.classList.toggle('multi-selected', ve.selectedNodeIds.has(id));
  updateExtractButton();
  renderParamPanel();
}

function clearMultiSelect() {
  for (const id of ve.selectedNodeIds) {
    document.querySelector(`.ve-node[data-id="${id}"]`)?.classList.remove('multi-selected');
  }
  ve.selectedNodeIds.clear();
  updateExtractButton();
}

function updateExtractButton() {
  const btn = document.getElementById('ve-extract-fn-btn');
  if (!btn) return;
  btn.style.display = ve.selectedNodeIds.size >= 1 && multiSelectIsValid() ? '' : 'none';
}

// Quick check: all selected nodes are non-sub, non-function main nodes in the same pipeline.
function multiSelectIsValid() {
  if (ve.selectedNodeIds.size === 0) return false;
  let graphIdx = -1;
  for (const id of ve.selectedNodeIds) {
    const n = findNode(id);
    if (!n || n.isSearchNode || n.isListNode || n.isFunctionCall || n.isUpstreamPseudo) return false;
    const gi = findNodeGraph(id);
    if (graphIdx < 0) graphIdx = gi;
    else if (gi !== graphIdx) return false;
  }
  return true;
}

// ── canvas ─────────────────────────────────────────────────────────────────────

function veRender() { renderCanvas(); renderParamPanel(); syncPaletteState(); }

function renderCanvas() {
  // Empty-hint only shows when there are zero pipelines.
  const hint = document.getElementById('ve-empty-hint');
  if (hint) hint.style.display = ve.graphs.length === 0 ? '' : 'none';

  renderPipelineRegions(); // drawn first so they sit behind nodes
  renderGraphNodes();
  renderPipelineLabels();
  // Defer edge drawing one animation frame so the browser has laid out the
  // newly-added .ve-node elements before nodeMidY reads their offsetHeight.
  // Without this, nodeMidY falls back to NODE_H/2 and edges are mispositioned.
  if (typeof requestAnimationFrame === 'function') {
    requestAnimationFrame(() => { renderEdges(); updateCanvasSize(); });
  } else {
    renderEdges(); updateCanvasSize(); // test / headless fallback
  }
}

// ── graph node rendering ───────────────────────────────────────────────────────

function renderGraphNodes() {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return;
  canvas.querySelectorAll('.ve-node').forEach(el => el.remove());

  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      // Upstream pseudo-node: non-selectable entry point for function body editing.
      if (n.isUpstreamPseudo) {
        const div = document.createElement('div');
        div.className = 've-node ve-node-upstream-pseudo';
        div.dataset.id   = n.id;
        div.dataset.role = 'source';
        div.style.left   = (n.x ?? 60) + 'px';
        div.style.top    = (n.y ?? 60) + 'px';
        div.innerHTML = `<div class="ve-node-role-bar"></div>
          <div class="ve-node-body">
            <div class="ve-node-name" style="color:#fbbf24">upstream</div>
            <span class="ve-node-role-badge ve-role-source">source</span>
            <div class="ve-upstream-label">function entry point</div>
          </div>
          <div class="ve-node-out-port" title="Drag to connect"></div>`;
        div.style.left = (n.x ?? 60) + 'px';
        div.style.top  = (n.y ?? 60) + 'px';
        canvas.appendChild(div);
        // Wire drag-to-connect from the out-port only.
        div.querySelector('.ve-node-out-port')?.addEventListener('pointerdown', e => {
          e.stopPropagation();
          startConnect(e, n.id);
        });
        continue;
      }

      const meta    = pluginMeta(n.plugin) || {role: 'processor'};
      const role    = meta.role;
      const sel     = n.id === ve.selectedNodeId;
      const warns   = fieldWarnings(n);
      const preview = configPreview(n.config);
      const isSearch = !!n.isSearchNode;
      const isList   = !!n.isListNode;
      const isFn     = !!n.isFunctionCall;
      // Sub-connected nodes show a badge in place of the role badge.
      const badgeHtml = isSearch ? '<span class="ve-node-search-badge">search</span>'
                      : isList   ? '<span class="ve-node-list-badge">list</span>'
                      : isFn     ? `<span class="ve-node-role-badge ve-role-${role}">${role}</span><span class="ve-node-fn-badge">fn</span>`
                      : `<span class="ve-node-role-badge ve-role-${role}">${role}</span>`;

      const multiSel = ve.selectedNodeIds.has(n.id);
      const div = document.createElement('div');
      div.className = `ve-node${sel ? ' selected' : ''}${multiSel ? ' multi-selected' : ''}${isSearch ? ' ve-node-search' : ''}${isList ? ' ve-node-list' : ''}${isFn ? ' ve-node-fn' : ''}`;
      div.dataset.role       = role;
      div.dataset.id         = n.id;
      div.dataset.isSearch   = meta.is_search_plugin ? 'true' : 'false';
      div.dataset.isList     = meta.is_list_plugin   ? 'true' : 'false';
      div.dataset.isSearchNd = isSearch ? 'true' : 'false';
      div.dataset.isListNd   = isList   ? 'true' : 'false';
      div.style.left = (n.x ?? 60) + 'px';
      div.style.top  = (n.y ?? 60) + 'px';
      const commentPreview = n.comment?.trim()
        ? `<div class="ve-node-comment-preview">${esc(n.comment.trim().split('\n')[0])}</div>` : '';
      const commentBtnCls = n.comment?.trim() ? ' has-comment' : '';
      div.innerHTML = [
        '<div class="ve-node-role-bar"></div>',
        '<div class="ve-node-body">',
          `<div class="ve-node-name">${esc(n.plugin)}</div>`,
          badgeHtml,
          preview        ? `<div class="ve-node-preview">${esc(preview)}</div>` : '',
          commentPreview,
          warns.length   ? `<div class="ve-node-warn">⚠ ${esc(warns[0])}</div>` : '',
        '</div>',
        `<button class="ve-node-remove" tabindex="-1" title="Remove">×</button>`,
        `<button class="ve-node-comment-btn${commentBtnCls}" tabindex="-1" title="Edit comment">#</button>`,
        // Output port: not shown on sub-nodes. Sinks show a chain port so they
        // can connect to downstream sinks (sink chaining).
        (!isSearch && !isList) ? `<div class="ve-node-out-port${role === 'sink' ? ' ve-node-chain-port' : ''}" title="${role === 'sink' ? 'Drag to chain to another output node' : 'Drag to connect'}"></div>` : '',
        // Input port indicator: shown on valid drop-targets while dragging an output port.
        (role !== 'source' && !isSearch && !isList) ? '<div class="ve-node-in-port"></div>' : '',
      ].join('');

      div.querySelector('.ve-node-remove').addEventListener('click', e => {
        e.stopPropagation();
        removeNode(n.id);
      });

      div.querySelector('.ve-node-comment-btn').addEventListener('click', e => {
        e.stopPropagation();
        openTextPopup(
          `Comment — ${n.plugin} (${n.id})`,
          'Enter a comment (shown above this node in the config file)…',
          n.comment || '',
          text => { n.comment = text; renderGraphNodes(); renderEdges(); onModelChange(); }
        );
      });

      div.addEventListener('pointerdown', e => {
        if (e.button !== 0) return; // left button only; let middle button pan
        if (e.target.closest('.ve-node-remove') || e.target.closest('.ve-node-out-port')      ||
            e.target.closest('.ve-node-search-port') || e.target.closest('.ve-node-list-port') ||
            e.target.closest('.ve-node-comment-btn')) return;
        e.preventDefault();
        e.stopPropagation();
        if (e.metaKey || e.ctrlKey) {
          toggleMultiSelect(n.id);
        } else {
          selectNode(n.id);
          startNodeDrag(e, n);
        }
      });

      // Double-click a function call node to open its editor.
      if (n.isFunctionCall) {
        div.addEventListener('dblclick', e => {
          e.stopPropagation();
          openFunctionEditor(n.plugin);
        });
      }

      // Receive regular upstream= drop (not allowed on source / sub-nodes).
      div.addEventListener('pointerup', () => {
        if (ve_connecting && ve_connecting.srcId !== n.id && role !== 'source' && !isSearch && !isList) finishConnect(n.id);
        // Receive search-port drop.
        if (ve_searchConnecting && ve_searchConnecting.discoverNodeId !== n.id && meta.is_search_plugin && !isSearch && !isList) finishSearchConnect(n.id);
        // Receive list-port drop.
        if (ve_listConnecting && ve_listConnecting.parentNodeId !== n.id && meta.is_list_plugin && !isSearch && !isList) finishListConnect(n.id);
      });

      const outPort = div.querySelector('.ve-node-out-port');
      if (outPort) {
        outPort.addEventListener('pointerdown', e => {
          e.stopPropagation();
          e.preventDefault();
          startConnect(e, n.id);
        });
      }

      // Search-port (bottom circle): drag FROM here to a search-plugin node.
      if (meta.accepts_search) {
        const searchPort = document.createElement('div');
        searchPort.className = 've-node-search-port';
        searchPort.title = 'Drag to a search-plugin node to add it as a search backend';
        searchPort.textContent = 'search';
        searchPort.addEventListener('pointerdown', e => {
          e.stopPropagation(); e.preventDefault();
          startSearchConnect(e, n.id);
        });
        div.appendChild(searchPort);
      }

      if (meta.accepts_list) {
        const listPort = document.createElement('div');
        listPort.className = 've-node-list-port';
        listPort.title = 'Drag from here to a list-source node — or drop a list-source node\'s output arrow here';
        listPort.textContent = 'list';

        // Initiate a list-connect drag (series → list-source).
        listPort.addEventListener('pointerdown', e => {
          e.stopPropagation(); e.preventDefault();
          startListConnect(e, n.id);
        });

        div.appendChild(listPort);
      }

      canvas.appendChild(div);
    }
  }
}

// ── pipeline labels / separators ───────────────────────────────────────────────

function renderPipelineLabels() {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return;
  canvas.querySelectorAll('.ve-pipeline-label').forEach(el => el.remove());

  for (let i = 0; i < ve.graphs.length; i++) {
    const g = ve.graphs[i];
    if (g._labelY == null) continue;

    // ── Pipeline header label (name + schedule input) ────────────────────────
    const label = document.createElement('div');
    label.className = `ve-pipeline-label${i === ve.activeGraph ? ' active' : ''}`;
    label.dataset.graphIdx = i;   // needed so startNodeDrag can move it in-place
    label.style.top = (g._labelY - 4) + 'px';
    const commentBtnCls = g.comment?.trim() ? ' has-comment' : '';
    label.innerHTML = [
      `<span class="ve-pl-name" title="Click to activate · Double-click to rename">${esc(g.name)}</span>`,
      `<button class="ve-pl-comment-btn${commentBtnCls}" title="Edit pipeline comment">#</button>`,
      `<span class="ve-pl-sep">schedule:</span>`,
      `<input class="ve-pl-sched" placeholder="e.g. 1h" value="${esc(g.schedule || '')}" title="Cron or interval schedule">`,
      ve.fnEditor.active ? '' : `<button class="ve-pl-delete" tabindex="-1" title="Delete pipeline and all its nodes">×</button>`,
    ].join('');

    label.addEventListener('pointerdown', e => e.stopPropagation());

    const gi = i;
    label.querySelector('.ve-pl-sched').addEventListener('input',  e => { ve.graphs[gi].schedule = e.target.value; onModelChange(); });
    label.querySelector('.ve-pl-sched').addEventListener('change', e => { ve.graphs[gi].schedule = e.target.value; onModelChange(); });
    const nameSpan = label.querySelector('.ve-pl-name');
    nameSpan.addEventListener('click', () => {
      ve.activeGraph = gi;
      // Toggle .active on existing labels in-place so we don't recreate them
      // (recreating would destroy the dblclick handler on nameSpan).
      document.querySelectorAll('.ve-pipeline-label[data-graph-idx]').forEach(el => {
        el.classList.toggle('active', parseInt(el.dataset.graphIdx) === gi);
      });
      renderPipelineRegions();
      renderParamPanel();
    });

    // ── Double-click: edit pipeline name in-place ─────────────────────────
    nameSpan.addEventListener('dblclick', e => {
      e.stopPropagation();
      const original = ve.graphs[gi].name;

      const input = document.createElement('input');
      input.className = 've-pl-name-edit';
      input.value     = original;
      input.style.width = Math.max(60, original.length * 8 + 20) + 'px';
      nameSpan.replaceWith(input);
      input.focus();
      input.select();

      let done = false;
      function commit() {
        if (done) return; done = true;
        const next = input.value.trim();
        // Reject empty or duplicate names (preserve original silently).
        const duplicate = ve.graphs.some((gr, idx) => idx !== gi && gr.name === next);
        if (next && next !== original && !duplicate) {
          ve.graphs[gi].name = next;
          onModelChange();
        }
        renderPipelineLabels();
      }
      function cancel() {
        if (done) return; done = true;
        renderPipelineLabels();
      }

      input.addEventListener('blur', commit);
      input.addEventListener('input', () => {
        input.style.width = Math.max(60, input.value.length * 8 + 20) + 'px';
      });
      input.addEventListener('keydown', ev => {
        if (ev.key === 'Enter')  { ev.preventDefault(); input.blur(); }
        if (ev.key === 'Escape') { ev.preventDefault(); input.removeEventListener('blur', commit); cancel(); }
      });
    });
    label.querySelector('.ve-pl-comment-btn').addEventListener('click', e => {
      e.stopPropagation();
      openTextPopup(
        `Comment — pipeline "${g.name}"`,
        'Enter a comment (shown above pipeline() in the config file)…',
        ve.graphs[gi].comment || '',
        text => { ve.graphs[gi].comment = text; renderPipelineLabels(); onModelChange(); }
      );
    });
    label.querySelector('.ve-pl-delete').addEventListener('click', e => {
      e.stopPropagation();
      deletePipeline(gi);
    });

    canvas.appendChild(label);
  }

}

// ── pipeline regions (background panels) ──────────────────────────────────────

function renderPipelineRegions() {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return;

  // Remove regions for graphs that no longer exist.
  canvas.querySelectorAll('.ve-pipeline-region').forEach(el => {
    if (parseInt(el.dataset.graphIdx) >= ve.graphs.length) el.remove();
  });

  for (let i = 0; i < ve.graphs.length; i++) {
    const g = ve.graphs[i];
    if (g._regionY == null) continue;

    // Recompute height from current node positions every time so the region
    // can both grow AND shrink as nodes are moved around.
    let regionH = 80;
    for (const n of g.nodes) {
      const nodeBot = (n.y ?? 0) + NODE_H + (n.searchNodeIds?.length ? 100 : 24);
      regionH = Math.max(regionH, nodeBot - (g._regionY ?? 0) + 24);
    }
    g._regionH = regionH;

    // Update an existing element in-place (smooth during drag — no DOM churn).
    let region = canvas.querySelector(`.ve-pipeline-region[data-graph-idx="${i}"]`);
    if (region) {
      region.className    = `ve-pipeline-region${i === ve.activeGraph ? ' active' : ''}`;
      region.style.top    = g._regionY + 'px';
      region.style.height = regionH + 'px';
      // Sync empty-pipeline hint.
      const hint = region.querySelector('.ve-region-hint');
      if (!g.nodes.length && !hint) {
        region.innerHTML = '<div class="ve-region-hint">Drop plugins from the palette to build this pipeline</div>';
      } else if (g.nodes.length && hint) {
        hint.remove();
      }
      continue;
    }

    // First render — create the element.
    region = document.createElement('div');
    region.className = `ve-pipeline-region${i === ve.activeGraph ? ' active' : ''}`;
    region.dataset.graphIdx = i;
    region.style.top    = g._regionY + 'px';
    region.style.height = regionH + 'px';
    region.addEventListener('pointerdown', e => {
      if (e.target === region) { ve.activeGraph = i; renderPipelineRegions(); }
    });
    if (!g.nodes.length) {
      region.innerHTML = '<div class="ve-region-hint">Drop plugins from the palette to build this pipeline</div>';
    }
    canvas.insertBefore(region, canvas.firstChild);
  }
}

// ── add / manage pipelines ────────────────────────────────────────────────────

function addPipeline() {
  const lastG = ve.graphs[ve.graphs.length - 1];
  const newLabelY = lastG
    ? (lastG._regionY ?? 40) + Math.max(80, lastG._regionH ?? 80) + 60
    : 40;
  ve.graphs.push({
    name:     `pipeline-${ve.graphs.length + 1}`,
    schedule: '',
    nodes:    [],
    _labelY:  newLabelY,
    _regionY: newLabelY - 8,
    _regionH: 80,
  });
  ve.activeGraph = ve.graphs.length - 1;
  veRender();
  onModelChange();
}

function deletePipeline(graphIdx) {
  // In function editing mode the single graph IS the function body — deleting
  // it would wipe the canvas.  Use Cancel or ← Back to exit the editor instead.
  if (ve.fnEditor.active) return;
  const removed = ve.graphs[graphIdx];
  // How much vertical space the deleted pipeline occupied (region height + the
  // 60 px inter-pipeline gap used by autoLayout).
  const vacated = (removed._regionH ?? 80) + 60;

  ve.graphs.splice(graphIdx, 1);
  ve.activeGraph    = Math.max(0, Math.min(ve.activeGraph, ve.graphs.length - 1));
  ve.selectedNodeId = null;

  // Shift every pipeline that was below the deleted one upward to fill the gap.
  for (let j = graphIdx; j < ve.graphs.length; j++) {
    const g = ve.graphs[j];
    if (g._labelY  != null) g._labelY  -= vacated;
    if (g._regionY != null) g._regionY -= vacated;
    for (const n of g.nodes) {
      if (n.y != null) n.y = Math.max(0, n.y - vacated);
    }
  }

  veRender();
  onModelChange();
}

// ── SVG edges ─────────────────────────────────────────────────────────────────

function renderEdges() {
  const svg = document.getElementById('ve-graph-svg');
  if (!svg) return;

  let vis = '';   // visible styled paths
  let hit = '';   // invisible wide paths for click-to-delete

  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      for (const upId of (n.upstreams || [])) {
        const up = g.nodes.find(x => x.id === upId);
        if (!up) continue;
        const x1 = (up.x ?? 0) + NODE_W;
        const y1 = nodeMidY(up.id, up.y);
        const x2 = (n.x ?? 0);
        const y2 = nodeMidY(n.id, n.y);
        const dx  = Math.max(60, Math.abs(x2 - x1) * 0.5);
        const sel = n.id === ve.selectedNodeId || up.id === ve.selectedNodeId;
        const d   = `M${x1},${y1} C${x1+dx},${y1} ${x2-dx},${y2} ${x2},${y2}`;
        // Use a distinct style for sink→sink chain edges.
        const upRole  = pluginMeta(up.plugin)?.role;
        const nRole   = pluginMeta(n.plugin)?.role;
        const isChain = upRole === 'sink' && nRole === 'sink';
        if (isChain) {
          const mId = sel ? '#arrow-chain-sel' : '#arrow-chain';
          vis += `<path d="${d}" class="ve-chain-edge${sel ? ' selected' : ''}" marker-end="url(${mId})"/>`;
        } else {
          vis += `<path d="${d}" class="ve-edge${sel ? ' selected' : ''}" marker-end="url(${sel ? '#arrow-sel' : '#arrow'})"/>`;
        }
        // Invisible fat stroke for easy click-to-delete; data attrs carry the link info.
        hit += `<path d="${d}" class="ve-edge-hit" data-src="${upId}" data-dst="${n.id}"><title>Click to disconnect</title></path>`;
      }
    }
  }

  if (ve_connecting) {
    const src = findNode(ve_connecting.srcId);
    if (src) {
      const x1 = (src.x ?? 0) + NODE_W, y1 = nodeMidY(src.id, src.y);
      const x2 = ve_connecting.curX,     y2 = ve_connecting.curY;
      const dx  = Math.max(60, Math.abs(x2 - x1) * 0.5);
      vis += `<path d="M${x1},${y1} C${x1+dx},${y1} ${x2-dx},${y2} ${x2},${y2}" class="ve-edge connecting"/>`;
    }
  }

  // Search-edges: dashed lines from discover's search-port (bottom centre) to each search-connected node.
  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      for (const searchId of (n.searchNodeIds || [])) {
        const sn = g.nodes.find(x => x.id === searchId);
        if (!sn) continue;
        const x1 = (n.x ?? 0) + NODE_W / 2;
        const y1 = nodeBottomY(n.id, n.y);         // bottom of discover (search-port)
        const x2 = (sn.x ?? 0) + NODE_W / 2;
        const y2 = (sn.y ?? 0);                    // TOP border of search node (not mid)
        const sel = ve.selectedNodeId === n.id || ve.selectedNodeId === searchId;
        const mId = sel ? '#arrow-search-sel' : '#arrow-search';
        const d   = `M${x1},${y1} C${x1},${y1+40} ${x2},${y2-40} ${x2},${y2}`;
        vis += `<path d="${d}" class="ve-search-edge${sel ? ' selected' : ''}"` +
               ` marker-end="url(${mId})" data-src="${n.id}" data-dst="${searchId}" data-search="true"/>`;
        hit += `<path d="${d}" class="ve-edge-hit"` +
               ` data-src="${n.id}" data-dst="${searchId}" data-search="true"><title>Click to disconnect</title></path>`;
      }
    }
  }

  // Live cursor line while dragging from a search-port.
  if (ve_searchConnecting) {
    const disc = findNode(ve_searchConnecting.discoverNodeId);
    if (disc) {
      const x1 = (disc.x ?? 0) + NODE_W / 2;
      const y1 = nodeBottomY(disc.id, disc.y);
      const x2 = ve_searchConnecting.curX, y2 = ve_searchConnecting.curY;
      vis += `<path d="M${x1},${y1} C${x1},${y1+40} ${x2},${y2-40} ${x2},${y2}" class="ve-search-edge connecting"/>`;
    }
  }

  // List-edges: teal dashed lines flowing downward FROM the list-node's bottom
  // TO the series/movies node's top (list-port).  list-nodes sit above the parent.
  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      for (const lnId of (n.listNodeIds || [])) {
        const ln = g.nodes.find(x => x.id === lnId);
        if (!ln) continue;
        // List-node is above; its bottom connects to the parent's top (list-port).
        // The curve always approaches the list-port from directly above so the
        // arrowhead visually lands on the teal port, not the left-side input.
        const x1 = (ln.x ?? 0) + NODE_W / 2;
        const y1 = nodeBottomY(lnId, ln.y);
        const x2 = (n.x  ?? 0) + NODE_W / 2;
        const y2 = (n.y  ?? 0);                // top of parent = list-port
        const dy  = Math.max(50, Math.abs(y2 - y1) * 0.5);
        // cp2 uses x2 so the final approach is straight down into the list port.
        const sel  = ve.selectedNodeId === n.id || ve.selectedNodeId === lnId;
        const mEnd = sel ? '#arrow-list-sel' : '#arrow-list';
        vis += `<path d="M${x1},${y1} C${x1},${y1+dy} ${x2},${y2-dy} ${x2},${y2}"` +
               ` class="ve-list-edge${sel ? ' selected' : ''}" marker-end="url(${mEnd})"` +
               ` data-src="${n.id}" data-dst="${lnId}" data-list="true"/>`;
        hit += `<path d="M${x1},${y1} C${x1},${y1+dy} ${x2},${y2-dy} ${x2},${y2}"` +
               ` class="ve-edge-hit" data-src="${n.id}" data-dst="${lnId}" data-list="true"><title>Click to disconnect</title></path>`;
      }
    }
  }

  // Live cursor line while dragging from a list-port.
  // Drawn FROM the list-port (top of parent) UPWARD TO the cursor — same
  // convention as startConnect/startSearchConnect so the fixed anchor is obvious.
  if (ve_listConnecting) {
    const par = findNode(ve_listConnecting.parentNodeId);
    if (par) {
      const x1 = (par.x ?? 0) + NODE_W / 2;
      const y1 = (par.y ?? 0);  // top of parent = list-port
      const x2 = ve_listConnecting.curX, y2 = ve_listConnecting.curY;
      const dy  = Math.max(40, Math.abs(y1 - y2) * 0.4);
      vis += `<path d="M${x1},${y1} C${x1},${y1-dy} ${x2},${y2+dy} ${x2},${y2}" class="ve-list-edge connecting"/>`;
    }
  }

  svg.innerHTML =
    `<defs>` +
      `<marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#555e6a"/>` +
      `</marker>` +
      `<marker id="arrow-sel" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#58a6ff"/>` +
      `</marker>` +
      `<marker id="arrow-chain" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#e3b341"/>` +
      `</marker>` +
      `<marker id="arrow-chain-sel" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#f0d070"/>` +
      `</marker>` +
      `<marker id="arrow-list" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#0d9373"/>` +
      `</marker>` +
      `<marker id="arrow-list-sel" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#2dd4b8"/>` +
      `</marker>` +
      `<marker id="arrow-search" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#9a6ad8"/>` +
      `</marker>` +
      `<marker id="arrow-search-sel" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#d2a8ff"/>` +
      `</marker>` +
    `</defs>${vis}${hit}`;
}

// ── canvas sizing + zoom ───────────────────────────────────────────────────────

function updateCanvasSize() {
  const canvas = document.getElementById('ve-graph-canvas');
  const svg    = document.getElementById('ve-graph-svg');
  if (!canvas) return;
  let w = 500, h = 300;
  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      w = Math.max(w, (n.x ?? 0) + NODE_W + 120);
      h = Math.max(h, (n.y ?? 0) + NODE_H + 120);
    }
  }
  canvas.style.width  = w + 'px';
  canvas.style.height = h + 'px';
  if (svg) { svg.setAttribute('width', w); svg.setAttribute('height', h); }
  applyZoom();
}

// Combines pan and zoom into a single CSS transform on the canvas element.
// No outer sizer div needed — the canvas body uses overflow:hidden.
function applyZoom() {
  const canvas = document.getElementById('ve-graph-canvas');
  const label  = document.getElementById('ve-zoom-pct');
  if (!canvas) return;
  canvas.style.transform = `translate(${ve_panX}px,${ve_panY}px) scale(${ve_zoom})`;
  if (label) label.textContent = Math.round(ve_zoom * 100) + '%';
}

function setZoom(z) {
  ve_zoom = Math.max(0.2, Math.min(3.0, z));
  applyZoom();
}

// ── layout ─────────────────────────────────────────────────────────────────────

// layoutGraph auto-lays out a single pipeline starting at globalY.
// Returns the new globalY (bottom of this pipeline + gap).
function layoutGraph(g, globalY) {
  const COL_W = 260, ROW_H = 120, PAD_X = 50, PIPELINE_GAP = 60;

  if (!g.nodes.length) {
    g._labelY  = globalY;
    g._regionY = globalY - 8;
    g._regionH = 80;
    return globalY + 80 + PIPELINE_GAP;
  }

  g._labelY = globalY;
  const isSub = n => n.isSearchNode || n.isListNode;
  // Extra padding above main nodes so list-connected sub-nodes have room to sit
  // above their parent without overlapping the pipeline label area.
  const listPad = g.nodes.some(n => !isSub(n) && n.listNodeIds?.length)
    ? NODE_H + 65 + 16
    : 0;
  const startY = globalY + 36 + listPad; // space for pipeline label (+ list nodes)

  // ── 1. Topological depth (search/list sub-nodes are laid out separately) ───
  const depth = {};
  for (const n of g.nodes) if (!isSub(n) && !n.upstreams.length) depth[n.id] = 0;
  let changed = true;
  while (changed) {
    changed = false;
    for (const n of g.nodes) {
      if (isSub(n) || !n.upstreams.length) continue;
      const maxUp = Math.max(...n.upstreams.map(u => depth[u] ?? -1));
      if (maxUp >= 0 && depth[n.id] !== maxUp + 1) { depth[n.id] = maxUp + 1; changed = true; }
    }
  }
  for (const n of g.nodes) if (!isSub(n) && depth[n.id] == null) depth[n.id] = 0;

  const byDepth = {};
  for (const n of g.nodes) {
    if (isSub(n)) continue;
    (byDepth[depth[n.id]] = byDepth[depth[n.id]] || []).push(n.id);
  }
  const depths = Object.keys(byDepth).map(Number).sort((a, b) => a - b);

  // ── 2. Initial column X; Y evenly-spaced within each column ─────────────
  for (const d of depths) {
    byDepth[d].forEach((id, i) => {
      const n = g.nodes.find(n => n.id === id);
      if (n) { n.x = PAD_X + d * COL_W; n.y = startY + i * ROW_H; }
    });
  }

  // ── 3. Barycenter pass: pull each non-root node toward avg Y of upstreams.
  for (const d of depths) {
    if (d === 0) continue;
    const ids = byDepth[d];
    const target = {};
    for (const id of ids) {
      const n = g.nodes.find(n => n.id === id);
      if (!n?.upstreams.length) { target[id] = null; continue; }
      const upYs = n.upstreams.map(uid => (g.nodes.find(x => x.id === uid)?.y ?? startY));
      target[id] = upYs.reduce((a, b) => a + b, 0) / upYs.length;
    }
    const sorted = [...ids].sort((a, b) => (target[a] ?? startY) - (target[b] ?? startY));
    let minY = -Infinity;
    for (const id of sorted) {
      const n = g.nodes.find(n => n.id === id);
      if (!n) continue;
      n.y = Math.max(minY, target[id] ?? startY, startY);
      minY = n.y + ROW_H;
    }
  }

  // ── 4. Shift everything so the topmost node starts at startY ─────────────
  let topY = Infinity;
  for (const n of g.nodes) topY = Math.min(topY, n.y ?? Infinity);
  if (topY > startY) {
    const shift = topY - startY;
    for (const n of g.nodes) n.y -= shift;
  }

  let maxY = startY + ROW_H;
  for (const n of g.nodes) {
    if (!isSub(n)) maxY = Math.max(maxY, (n.y ?? 0) + ROW_H);
  }

  // Position search-connected nodes in a row below their parent.
  for (const n of g.nodes) {
    if (!n.searchNodeIds?.length) continue;
    const SEARCH_GAP = 18;
    const totalW  = n.searchNodeIds.length * NODE_W + (n.searchNodeIds.length - 1) * SEARCH_GAP;
    const startSX = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
    n.searchNodeIds.forEach((id, i) => {
      const sn = g.nodes.find(x => x.id === id);
      if (sn) {
        sn.x = Math.max(0, startSX + i * (NODE_W + SEARCH_GAP));
        sn.y = (n.y ?? 0) + NODE_H + 70;
        maxY = Math.max(maxY, sn.y + NODE_H + 20);
      }
    });
  }

  // Position list-connected nodes in a row above their parent.
  for (const n of g.nodes) {
    if (!n.listNodeIds?.length) continue;
    const LIST_GAP = 18;
    const totalW   = n.listNodeIds.length * NODE_W + (n.listNodeIds.length - 1) * LIST_GAP;
    const startLX  = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
    n.listNodeIds.forEach((id, i) => {
      const ln = g.nodes.find(x => x.id === id);
      if (ln) {
        ln.x = Math.max(0, startLX + i * (NODE_W + LIST_GAP));
        ln.y = Math.max(startY + 4, (n.y ?? 0) - NODE_H - 65);
      }
    });
  }

  g._regionY = g._labelY - 8;
  g._regionH = maxY - g._regionY + 16;
  return maxY + PIPELINE_GAP;
}

// initLayout places all pipelines in order.
//
// Per-node positions (x, relative-y) come from # pipeliner:pos comments parsed
// by the server and stored directly on each node. initLayout converts relative-y
// to absolute-y once the pipeline's _regionY is known.
//
// A pipeline with at least one positioned main node is "has layout". Unpositioned
// nodes in such a pipeline are placed to the right of the existing bounding box.
// Pipelines with no positioned nodes are fully auto-laid out via layoutGraph.
function initLayout() {
  const PIPELINE_GAP = 60;
  let globalY = 40;

  for (const g of ve.graphs) {
    const mainNodes = g.nodes.filter(n => !n.isSearchNode && !n.isListNode);
    const withPos   = mainNodes.filter(n => n.x != null && n.y != null);

    if (!withPos.length) {
      // No stored positions — full auto-layout.
      globalY = layoutGraph(g, globalY);
      continue;
    }

    // At least one node has a stored position.
    g._labelY  = globalY;
    g._regionY = globalY - 8;

    // Convert relative-y to absolute-y for positioned nodes.
    let maxAbsY = g._regionY;
    let maxAbsX = 0;
    for (const n of withPos) {
      n.y = g._regionY + n.y;          // relative → absolute
      maxAbsY = Math.max(maxAbsY, n.y);
      maxAbsX = Math.max(maxAbsX, (n.x ?? 0) + NODE_W + 60);
    }

    // Convert relative-y to absolute-y for sub-nodes with stored positions.
    for (const n of g.nodes) {
      if ((n.isSearchNode || n.isListNode) && n.x != null && n.y != null) {
        n.y = g._regionY + n.y;
      }
    }

    // Place unpositioned main nodes using DAG structure so that each node lands
    // to the right of its rightmost positioned upstream, preserving the pipeline's
    // left-to-right flow instead of stacking everything in a single column.
    const noPos = mainNodes.filter(n => n.x == null || n.y == null);
    if (noPos.length) {
      const COL_W   = 260; // mirrors layoutGraph constant
      const posById = {};
      for (const n of withPos) posById[n.id] = n;

      // Topological order ensures each node's upstreams are placed first.
      const noPosOrdered = topoSortNodes(mainNodes).filter(n => n.x == null || n.y == null);
      let stackIdx = 0;
      for (const n of noPosOrdered) {
        const posUps = (n.upstreams || []).map(u => posById[u]).filter(Boolean);
        if (posUps.length) {
          const rightmost = posUps.reduce((a, b) => (a.x > b.x ? a : b));
          n.x = rightmost.x + COL_W;
          n.y = rightmost.y;
        } else {
          n.x = maxAbsX;
          n.y = g._regionY + 40 + stackIdx * 120;
          stackIdx++;
        }
        posById[n.id] = n;
        maxAbsX = Math.max(maxAbsX, (n.x ?? 0) + NODE_W + 60);
        maxAbsY = Math.max(maxAbsY, n.y);
      }
    }

    // Derive positions for sub-nodes that don't yet have stored positions.
    placeSubNodes(g, g._regionY + 36);

    g._regionH = maxAbsY - g._regionY + NODE_H + 60;
    globalY = g._regionY + g._regionH + PIPELINE_GAP;
  }
}

// placeSubNodes positions search/list sub-nodes relative to their parent.
// Mirrors the sub-node placement in layoutGraph; called after main node
// positions are finalised so parents have absolute coordinates.
// Sub-nodes that already have stored positions (x != null && y != null) are skipped.
function placeSubNodes(g, startY) {
  for (const n of g.nodes) {
    if (n.searchNodeIds?.length) {
      const SEARCH_GAP = 18;
      const totalW  = n.searchNodeIds.length * NODE_W + (n.searchNodeIds.length - 1) * SEARCH_GAP;
      const startSX = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
      n.searchNodeIds.forEach((id, i) => {
        const sn = g.nodes.find(x => x.id === id);
        // Recompute if missing OR if the stored position overlaps the parent.
        if (sn && (sn.x == null || sn.y == null || sn.y < (n.y ?? 0) + NODE_H)) {
          sn.x = Math.max(0, startSX + i * (NODE_W + SEARCH_GAP));
          sn.y = (n.y ?? 0) + NODE_H + 70;
        }
      });
    }
    if (n.listNodeIds?.length) {
      const LIST_GAP = 18;
      const totalW   = n.listNodeIds.length * NODE_W + (n.listNodeIds.length - 1) * LIST_GAP;
      const startLX  = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
      n.listNodeIds.forEach((id, i) => {
        const ln = g.nodes.find(x => x.id === id);
        // Recompute if missing OR if the stored position places the list at or
        // below the parent (overlap — can happen with positions saved before
        // the listPad fix that ensured enough vertical clearance).
        if (ln && (ln.x == null || ln.y == null || ln.y >= (n.y ?? 0))) {
          ln.x = Math.max(0, startLX + i * (NODE_W + LIST_GAP));
          ln.y = Math.max(startY + 4, (n.y ?? 0) - NODE_H - 65);
        }
      });
    }
  }
}

// autoLayout re-lays out every pipeline (used by the "auto-layout" button).
function autoLayout() {
  let globalY = 40;
  for (const g of ve.graphs) globalY = layoutGraph(g, globalY);
}

// ── cycle detection ───────────────────────────────────────────────────────────

// Returns true if adding the directed edge src → target would create a cycle.
// Uses BFS from `target` following existing downstream edges; if we reach `src`
// a path target →* src already exists so the new edge would close a loop.
function wouldCreateCycle(graphNodes, srcId, targetId) {
  if (srcId === targetId) return true; // self-loop
  const visited = new Set();
  const queue   = [targetId];
  while (queue.length) {
    const cur = queue.shift();
    if (visited.has(cur)) continue;
    visited.add(cur);
    for (const n of graphNodes) {
      if ((n.upstreams || []).includes(cur)) {
        if (n.id === srcId) return true;
        if (!visited.has(n.id)) queue.push(n.id);
      }
    }
  }
  return false;
}

// Returns the set of all ancestor IDs of nodeId (nodes from which nodeId is
// reachable following downstream edges). Used to dim cycle-causing targets.
function ancestorIds(graphNodes, nodeId) {
  const result  = new Set();
  const visited = new Set();
  const queue   = [nodeId];
  while (queue.length) {
    const cur = queue.shift();
    if (visited.has(cur)) continue;
    visited.add(cur);
    const n = graphNodes.find(x => x.id === cur);
    for (const upId of (n?.upstreams ?? [])) {
      if (!result.has(upId)) { result.add(upId); queue.push(upId); }
    }
  }
  return result;
}

// ── drop helpers ──────────────────────────────────────────────────────────────

// Return the index of the pipeline whose region contains canvas point (cx, cy),
// or -1 if the point is outside every region.
function findGraphAtPosition(cx, cy) {
  for (let i = 0; i < ve.graphs.length; i++) {
    const g = ve.graphs[i];
    if (g._regionY == null) continue;
    const top    = g._regionY;
    const bottom = g._regionY + Math.max(80, g._regionH ?? 80);
    if (cy >= top && cy <= bottom) return i;
  }
  return -1;
}

// Enlarge a pipeline's region so a node placed at (nodeX, nodeY) is fully
// contained. Both vertical growth (down and up into the label area) are handled.
function expandRegionForNode(graphIdx, nodeX, nodeY) {
  const g = ve.graphs[graphIdx];
  if (g._regionY == null) return;
  const PAD     = 28;
  const nodeB   = nodeY + NODE_H + PAD;
  const regionB = (g._regionY ?? 0) + (g._regionH ?? 80);
  if (nodeB <= regionB) return; // no expansion needed

  const growth = nodeB - regionB;
  g._regionH   = (g._regionH ?? 80) + growth;

  // Push every pipeline that sits below this one down by the same amount so
  // they never overlap. Never expand upward — Y is clamped at the drop site.
  for (let j = graphIdx + 1; j < ve.graphs.length; j++) {
    const next = ve.graphs[j];
    for (const n of next.nodes) {
      if (n.y != null) n.y += growth;
    }
    if (next._labelY  != null) next._labelY  += growth;
    if (next._regionY != null) next._regionY += growth;
  }
}

// ── viewport fit ──────────────────────────────────────────────────────────────
// Set .ve-layout height so the visual editor fills the available viewport
// height exactly, avoiding a page-level scrollbar.

function fitVisualEditor() {
  const layout = document.querySelector('.ve-layout');
  if (!layout) return;
  // Page-absolute top of the layout (stable across scroll positions).
  const layoutPageTop = layout.getBoundingClientRect().top + window.scrollY;
  // Body bottom padding so the layout ends flush with the content area.
  const padBottom = parseFloat(getComputedStyle(document.body).paddingBottom) || 0;
  // Layout should reach exactly: window.innerHeight - padBottom (page coords).
  const h = Math.floor(window.innerHeight - padBottom - layoutPageTop);
  layout.style.height = Math.max(400, h) + 'px';
}

// ── canvas event wiring (once) ─────────────────────────────────────────────────

function initCanvasEvents() {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return;

  // Re-fit the layout on every window resize so the page never scrolls.
  window.addEventListener('resize', fitVisualEditor);

  // Keyboard shortcuts: Escape clears multi-selection; Cmd/Ctrl+C copies;
  // Cmd/Ctrl+V pastes. Shortcuts are suppressed when focus is in a text field.
  window.addEventListener('keydown', e => {
    const inInput = ['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName) ||
                    e.target.isContentEditable;
    if (e.key === 'Escape' && ve.selectedNodeIds.size > 0 && !inInput) {
      clearMultiSelect();
      renderParamPanel();
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'c' && !inInput) {
      e.preventDefault();
      copySelected();
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'v' && !inInput) {
      e.preventDefault();
      pasteClipboard();
    }
  });

  // Deselect on empty-canvas click.
  canvas.addEventListener('pointerdown', e => {
    if (!e.target.closest('.ve-node') && !e.target.closest('.ve-pipeline-label')) {
      const prev = ve.selectedNodeId;
      ve.selectedNodeId = null;
      if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
      clearMultiSelect();
      renderEdges();
      renderParamPanel();
    }
  });

  // Click on an edge hit-path → remove that connection.
  const svg = document.getElementById('ve-graph-svg');
  if (svg) {
    // Use pointerdown (not click) so we can stopPropagation before the canvas
    // pointerdown handler fires renderEdges() — which would remove the hit-path
    // element from the DOM before the click event's target could be resolved.
    svg.addEventListener('pointerdown', e => {
      if (e.button !== 0) return; // left button only; let middle button pan
      const hit = e.target.closest?.('[data-src][data-dst]');
      if (!hit) return;
      e.stopPropagation(); // prevent canvas handler from deselecting / rebuilding SVG
      e.preventDefault();
      if (hit.dataset.search === 'true') {
        disconnectSearch(hit.dataset.src, hit.dataset.dst);
      } else if (hit.dataset.list === 'true') {
        disconnectList(hit.dataset.src, hit.dataset.dst);
      } else {
        // Regular edge: remove the upstream.
        const tgt = findNode(hit.dataset.dst);
        if (tgt) {
          tgt.upstreams = tgt.upstreams.filter(u => u !== hit.dataset.src);
          veRender();
          onModelChange();
        }
      }
    });
  }

  // Ctrl-wheel to zoom.
  canvas.addEventListener('wheel', e => {
    if (!e.ctrlKey) return;
    e.preventDefault();
    setZoom(ve_zoom * (e.deltaY < 0 ? 1.1 : 1 / 1.1));
  }, {passive: false});

  // Middle-mouse drag to pan (unlimited, no scrollbars).
  const body = document.getElementById('ve-canvas-body');
  if (body) {
    body.addEventListener('pointerdown', e => {
      if (e.button !== 1) return; // middle button only
      e.preventDefault();
      body.setPointerCapture(e.pointerId);
      body.style.cursor = 'grabbing';
      function onMove(ev) {
        ve_panX += ev.movementX;
        ve_panY += ev.movementY;
        applyZoom();
      }
      function onUp() {
        body.style.cursor = '';
        body.removeEventListener('pointermove', onMove);
      }
      body.addEventListener('pointermove', onMove);
      body.addEventListener('pointerup',     onUp, {once: true});
      body.addEventListener('pointercancel', onUp, {once: true});
    });
  }

  // Palette HTML5 drop — pipeline-aware.
  // getBoundingClientRect() already accounts for scroll+transform; dividing by
  // zoom gives the canvas coordinate directly.
  function clearDragOver() {
    canvas.querySelectorAll('.ve-pipeline-region.drag-over').forEach(el => el.classList.remove('drag-over'));
  }

  canvas.addEventListener('dragover', e => {
    if (ve.dragSrc?.type !== 'palette') return;
    e.preventDefault();
    const rect = canvas.getBoundingClientRect();
    const cx = (e.clientX - rect.left) / ve_zoom;
    const cy = (e.clientY - rect.top)  / ve_zoom;
    const gi = findGraphAtPosition(cx, cy);
    e.dataTransfer.dropEffect = gi >= 0 ? 'copy' : 'none';
    // Highlight the target pipeline region.
    canvas.querySelectorAll('.ve-pipeline-region').forEach(el => {
      el.classList.toggle('drag-over', parseInt(el.dataset.graphIdx) === gi);
    });
  });

  canvas.addEventListener('dragleave', e => {
    // Only clear when the cursor truly leaves the canvas (not just a child element).
    if (!canvas.contains(e.relatedTarget)) clearDragOver();
  });

  canvas.addEventListener('drop', e => {
    e.preventDefault();
    clearDragOver();
    if (!ve.dragSrc || ve.dragSrc.type !== 'palette') return;
    const rect = canvas.getBoundingClientRect();
    // Top-left corner of the new node card (centred on cursor).
    const x = Math.max(0, (e.clientX - rect.left) / ve_zoom - NODE_W / 2);
    let   y = Math.max(0, (e.clientY - rect.top)  / ve_zoom - NODE_H / 2);
    // Only accept drops that land inside a pipeline region.
    const gi = findGraphAtPosition(x + NODE_W / 2, y + NODE_H / 2);
    if (gi < 0) { ve.dragSrc = null; return; }
    const g = ve.graphs[gi];
    // Clamp Y downward so the node never lands above the pipeline's label row.
    // Regions only expand down-and-right; they never expand upward.
    const labelBottom = (g._labelY ?? (g._regionY ?? 0) + 8) + 30;
    y = Math.max(y, labelBottom + 4);
    const id      = genId(ve.dragSrc.plugin);
    const dragFd  = ve.userFunctions[ve.dragSrc.plugin];
    const dragCfg = {};
    if (dragFd) {
      for (const p of (dragFd.params || [])) {
        dragCfg[p.key] = p.default != null ? p.default : emptyForType(p.type);
      }
    }
    g.nodes.push({
      id, plugin: ve.dragSrc.plugin, config: dragCfg, upstreams: [], x, y, comment: '',
      searchNodeIds: [], listNodeIds: [],
      ...(dragFd ? {isFunctionCall: true, funcCallKey: id} : {}),
    });
    ve.selectedNodeId = id;
    ve.activeGraph    = gi;
    // Expand the region (downward only) so the new node is fully visible.
    expandRegionForNode(gi, x, y);
    ve.dragSrc = null;
    veRender(); onModelChange();
  });
}

// ── node drag ─────────────────────────────────────────────────────────────────
// selectNode no longer rebuilds the DOM, so div (found by data-id) stays valid.

function startNodeDrag(e, n) {
  const origX = n.x ?? 0, origY = n.y ?? 0;
  const startX = e.clientX, startY = e.clientY;
  ve_dragging = true;

  function onMove(ev) {
    n.x = Math.max(0, origX + (ev.clientX - startX) / ve_zoom);
    n.y = Math.max(0, origY + (ev.clientY - startY) / ve_zoom);
    const div = document.querySelector(`.ve-node[data-id="${n.id}"]`);
    if (div) { div.style.left = n.x + 'px'; div.style.top = n.y + 'px'; }

    // Recompute this pipeline's region height, then cascade any change in
    // height (up or down) to all subsequent pipelines so they never overlap.
    const gi = findNodeGraph(n.id);
    if (gi >= 0) {
      const g = ve.graphs[gi];
      const prevH = g._regionH ?? 80;
      let regionH = 80;
      for (const nd of g.nodes) {
        const nodeBot = (nd.y ?? 0) + NODE_H + (nd.searchNodeIds?.length ? 100 : 24);
        regionH = Math.max(regionH, nodeBot - (g._regionY ?? 0) + 24);
      }
      g._regionH = regionH;
      const delta = regionH - prevH;
      if (delta !== 0) {
        const canvas = document.getElementById('ve-graph-canvas');
        for (let j = gi + 1; j < ve.graphs.length; j++) {
          const next = ve.graphs[j];
          if (next._labelY  != null) next._labelY  += delta;
          if (next._regionY != null) next._regionY += delta;
          // Move the label element directly (same fast path as node divs).
          const labelEl = canvas?.querySelector(`.ve-pipeline-label[data-graph-idx="${j}"]`);
          if (labelEl) labelEl.style.top = (next._labelY - 4) + 'px';
          for (const nd of next.nodes) {
            if (nd.y != null) {
              nd.y += delta;
              const ndDiv = document.querySelector(`.ve-node[data-id="${nd.id}"]`);
              if (ndDiv) ndDiv.style.top = nd.y + 'px';
            }
          }
        }
      }
    }

    renderEdges();
    renderPipelineRegions();
    updateCanvasSize();
  }
  function onUp() {
    ve_dragging = null;
    document.removeEventListener('pointermove', onMove);
    onModelChange();
  }
  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp, {once: true});
  document.addEventListener('pointercancel', onUp, {once: true});
}

// ── via-node drag ─────────────────────────────────────────────────────────────



// ── connect interaction ────────────────────────────────────────────────────────

function startConnect(e, srcId) {
  const canvas = document.getElementById('ve-graph-canvas');
  // getBoundingClientRect() accounts for scroll and CSS transform,
  // so (clientX - rect.left) / zoom gives the canvas coordinate directly.
  const rect = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
  ve_connecting = {
    srcId,
    curX: (e.clientX - rect.left) / ve_zoom,
    curY: (e.clientY - rect.top)  / ve_zoom,
  };
  canvas?.classList.add('is-connecting');
  // Mark the source node so CSS can exclude its own input indicator.
  const srcDiv = document.querySelector(`.ve-node[data-id="${srcId}"]`);
  srcDiv?.classList.add('is-connect-source');
  // Mark all ancestor nodes (which would form a cycle if connected to src).
  const gi = findNodeGraph(srcId);
  const g  = gi >= 0 ? ve.graphs[gi] : null;
  if (g) {
    ancestorIds(g.nodes, srcId).forEach(id => {
      document.querySelector(`.ve-node[data-id="${id}"]`)?.classList.add('would-create-cycle');
    });
  }

  function onMove(ev) {
    const r = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
    ve_connecting.curX = (ev.clientX - r.left) / ve_zoom;
    ve_connecting.curY = (ev.clientY - r.top)  / ve_zoom;
    renderEdges();
  }
  function cleanup() {
    document.removeEventListener('pointermove', onMove);
    canvas?.classList.remove('is-connecting');
    document.querySelectorAll('.ve-node.is-connect-source').forEach(el => el.classList.remove('is-connect-source'));
    document.querySelectorAll('.ve-node.would-create-cycle').forEach(el => el.classList.remove('would-create-cycle'));
  }
  function onUp(ev) {
    cleanup();
    if (!ve_connecting) return;
    const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
    const tid = el?.dataset?.id;
    const tgtMeta = tid ? pluginMeta(findNode(tid)?.plugin) : null;
    if (tid && tid !== srcId && tgtMeta?.role !== 'source') finishConnect(tid);
    else { ve_connecting = null; renderEdges(); }
  }
  function onCancel() { cleanup(); ve_connecting = null; renderEdges(); }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp,    {once: true});
  document.addEventListener('pointercancel', onCancel, {once: true});
}

function finishConnect(targetId) {
  const tgt     = findNode(targetId);
  const src     = findNode(ve_connecting?.srcId);
  const tgtRole = pluginMeta(tgt?.plugin)?.role;
  const srcRole = pluginMeta(src?.plugin)?.role;
  const srcGi   = findNodeGraph(ve_connecting?.srcId);
  const tgtGi   = findNodeGraph(targetId);
  // Sources never accept incoming edges; list-source nodes can't also be regular
  // upstreams; and adding the edge must not create a cycle in the DAG.
  // Additionally, sink nodes may only connect to other sink nodes (chaining).
  if (src && tgt && tgtRole !== 'source' && !src.isListNode &&
      !tgt.isUpstreamPseudo &&
      srcGi === tgtGi && !tgt.upstreams.includes(src.id)) {
    // If source is a sink, the target must also be a sink (sink chaining rule).
    if (srcRole === 'sink' && tgtRole !== 'sink') {
      ve_connecting = null;
      veRender();
      return;
    }
    const g = srcGi >= 0 ? ve.graphs[srcGi] : null;
    if (g && !wouldCreateCycle(g.nodes, src.id, tgt.id)) {
      tgt.upstreams.push(src.id);
      onModelChange();
    }
  }
  ve_connecting = null;
  veRender();
}

// ── search-port connect interaction ───────────────────────────────────────────
// Drag from a discover node's search-port to a search-plugin node on the canvas.

function startSearchConnect(e, discoverNodeId) {
  const canvas = document.getElementById('ve-graph-canvas');
  const rect   = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
  ve_searchConnecting = {
    discoverNodeId,
    curX: (e.clientX - rect.left) / ve_zoom,
    curY: (e.clientY - rect.top)  / ve_zoom,
  };
  canvas?.classList.add('is-searchconnecting');

  function onMove(ev) {
    const r = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
    ve_searchConnecting.curX = (ev.clientX - r.left) / ve_zoom;
    ve_searchConnecting.curY = (ev.clientY - r.top)  / ve_zoom;
    renderEdges();
  }
  function cleanup() {
    document.removeEventListener('pointermove', onMove);
    canvas?.classList.remove('is-searchconnecting');
  }
  function onUp(ev) {
    cleanup();
    if (!ve_searchConnecting) return;
    const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
    const tid = el?.dataset?.id;
    if (tid && tid !== discoverNodeId && el.dataset.isSearch === 'true' && el.dataset.isSearchNd !== 'true') {
      finishSearchConnect(tid);
    } else { ve_searchConnecting = null; renderEdges(); }
  }
  function onCancel() { cleanup(); ve_searchConnecting = null; renderEdges(); }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp,    {once: true});
  document.addEventListener('pointercancel', onCancel, {once: true});
}

function finishSearchConnect(targetNodeId) {
  const disc   = findNode(ve_searchConnecting?.discoverNodeId);
  const target = findNode(targetNodeId);
  if (disc && target && pluginMeta(target.plugin)?.is_search_plugin && !target.isSearchNode) {
    if (!disc.searchNodeIds) disc.searchNodeIds = [];
    if (!disc.searchNodeIds.includes(targetNodeId)) {
      disc.searchNodeIds.push(targetNodeId);
      target.isSearchNode   = true;
      target.searchParentId = disc.id;
      onModelChange();
    }
  }
  ve_searchConnecting = null;
  veRender();
}

// ── list-port connect interaction ─────────────────────────────────────────────
// Drag from a series/movies node's "list" port to a list-source plugin node.

function startListConnect(e, parentNodeId) {
  const canvas = document.getElementById('ve-graph-canvas');
  const rect   = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
  ve_listConnecting = {
    parentNodeId,
    curX: (e.clientX - rect.left) / ve_zoom,
    curY: (e.clientY - rect.top)  / ve_zoom,
  };
  canvas?.classList.add('is-listconnecting');

  function onMove(ev) {
    const r = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
    ve_listConnecting.curX = (ev.clientX - r.left) / ve_zoom;
    ve_listConnecting.curY = (ev.clientY - r.top)  / ve_zoom;
    renderEdges();
  }
  function cleanup() {
    document.removeEventListener('pointermove', onMove);
    canvas?.classList.remove('is-listconnecting');
  }
  function onUp(ev) {
    cleanup();
    if (!ve_listConnecting) return;
    const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
    const tid = el?.dataset?.id;
    if (tid && tid !== parentNodeId && el.dataset.isList === 'true' && el.dataset.isListNd !== 'true') finishListConnect(tid);
    else { ve_listConnecting = null; renderEdges(); }
  }
  function onCancel() { cleanup(); ve_listConnecting = null; renderEdges(); }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp,    {once: true});
  document.addEventListener('pointercancel', onCancel, {once: true});
}

function finishListConnect(targetNodeId) {
  const parent = findNode(ve_listConnecting?.parentNodeId);
  const target = findNode(targetNodeId);
  // Also block if the target is already a regular upstream of the parent
  // (one connection type per node).
  if (parent && target && pluginMeta(target.plugin)?.is_list_plugin &&
      !target.isListNode && !(parent.upstreams || []).includes(targetNodeId)) {
    if (!parent.listNodeIds) parent.listNodeIds = [];
    if (!parent.listNodeIds.includes(targetNodeId)) {
      parent.listNodeIds.push(targetNodeId);
      target.isListNode   = true;
      target.listParentId = parent.id;
      onModelChange();
    }
  }
  ve_listConnecting = null;
  veRender();
}

// ── palette drag ───────────────────────────────────────────────────────────────

function paletteDragStart(e, name) {
  ve.dragSrc = {type: 'palette', plugin: name};
  e.dataTransfer.setData('text/plain', name);
  e.dataTransfer.effectAllowed = 'copy';

  // ── Suppress the browser's own drag ghost ──────────────────────────────────
  // setDragImage with a rendered-but-off-screen element is unreliable in
  // Chromium when the element is outside the viewport. Use a 1×1 blank canvas
  // to suppress the default ghost entirely, then track the cursor manually.
  const blank = document.createElement('canvas');
  blank.width = blank.height = 1;
  e.dataTransfer.setDragImage(blank, 0, 0);

  // ── Floating node-card preview ─────────────────────────────────────────────
  // position:fixed + pointer-events:none so it never blocks drag events.
  // All values are concrete hex/px — no CSS-variable or width-inheritance issues.
  const meta = pluginMeta(name);
  const role = meta?.role || 'processor';
  const RC   = {source: '#3fb950', processor: '#58a6ff', sink: '#ffa657'};
  const rc   = RC[role] || '#58a6ff';

  const preview = document.createElement('div');
  preview.style.cssText =
    `position:fixed;top:-200px;left:-200px;` +
    `width:${NODE_W}px;display:flex;align-items:stretch;` +
    `border:1px solid rgba(88,166,255,.55);border-radius:8px;background:#161b22;` +
    `box-shadow:0 6px 28px rgba(0,0,0,.75);opacity:0.92;` +
    `pointer-events:none;z-index:9999;` +
    `font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif;` +
    `font-size:13px;`;
  preview.innerHTML =
    `<div style="width:4px;flex-shrink:0;border-radius:7px 0 0 7px;background:${rc}"></div>` +
    `<div style="flex:1;padding:8px 10px;min-width:0;text-align:center">` +
      `<div style="font-size:13px;font-weight:600;color:#e6edf3;` +
               `overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-bottom:4px">${name}</div>` +
      `<span style="font-size:9px;font-weight:700;text-transform:uppercase;` +
             `letter-spacing:.05em;padding:1px 5px;border-radius:3px;` +
             `border:1px solid ${rc};color:${rc}">${role}</span>` +
    `</div>`;
  document.body.appendChild(preview);

  // dragover fires continuously while the user moves the mouse during a drag
  // (unlike pointermove, which is suppressed by the browser during DnD).
  function onOver(ev) {
    preview.style.left = (ev.clientX - NODE_W / 2) + 'px';
    preview.style.top  = (ev.clientY - 44) + 'px';
  }
  function onEnd() {
    preview.remove();
    document.removeEventListener('dragover', onOver);
  }
  document.addEventListener('dragover', onOver);
  document.addEventListener('dragend',   onEnd, {once: true});
}

// ── zoom buttons (called from HTML) ───────────────────────────────────────────

function zoomIn()    { setZoom(ve_zoom * 1.25); }
function zoomOut()   { setZoom(ve_zoom / 1.25); }
function zoomReset() { setZoom(1.0); }

// ── param panel ───────────────────────────────────────────────────────────────

function renderParamPanel() {
  const empty  = document.getElementById('ve-param-empty');
  if (!empty) return; // no DOM (e.g. test environment)
  const title  = document.getElementById('ve-param-title');
  const nameEl = document.getElementById('ve-param-name');
  const roleEl = document.getElementById('ve-param-phase');
  const body   = document.getElementById('ve-param-body');
  const footer = document.getElementById('ve-param-footer');

  const node = ve.selectedNodeId ? findNode(ve.selectedNodeId) : null;
  const gi   = node ? findNodeGraph(ve.selectedNodeId) : -1;
  const g    = gi >= 0 ? ve.graphs[gi] : null;

  // Upstream pseudo-node: show a simple description, no config.
  if (node?.isUpstreamPseudo) {
    empty.style.display = 'none'; title.style.display = '';
    if (nameEl) nameEl.textContent = 'upstream';
    if (roleEl) roleEl.textContent = 'function entry';
    body.innerHTML = '<p style="font-size:12px;color:var(--muted);padding:8px 0">This node represents the <code>upstream=</code> argument passed to the function at its call site. Connect it to the first node in the function body.</p>';
    footer.style.display = 'none';
    return;
  }

  if (!node || !g) {
    empty.style.display = ''; title.style.display = 'none';
    body.innerHTML = ''; footer.style.display = 'none';
    return;
  }

  const meta = pluginMeta(node.plugin) || {role: 'processor', schema: [], produces: [], requires: []};
  empty.style.display = 'none'; title.style.display = '';
  nameEl.textContent = node.plugin;
  roleEl.textContent = node.isSearchNode ? 'search' : node.isListNode ? 'list' : meta.role;

  if (node.isSearchNode && node.searchParentId) {
    const parentName = findNode(node.searchParentId)?.plugin ?? node.searchParentId;
    footer.innerHTML = [
      `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">Search backend for <b>${esc(parentName)}</b></div>`,
      `<button class="ve-remove-btn" onclick="disconnectSearch(${esc(JSON.stringify(node.searchParentId))},${esc(JSON.stringify(node.id))})">Disconnect from search</button>`,
    ].join('');
  } else if (node.isListNode && node.listParentId) {
    const parentName = findNode(node.listParentId)?.plugin ?? node.listParentId;
    footer.innerHTML = [
      `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">List source for <b>${esc(parentName)}</b></div>`,
      `<button class="ve-remove-btn" onclick="disconnectList(${esc(JSON.stringify(node.listParentId))},${esc(JSON.stringify(node.id))})">Disconnect from list</button>`,
    ].join('');
  } else if (node.isFunctionCall) {
    footer.innerHTML = `<button class="ve-remove-btn" onclick="ve.selectedNodeId && removeNode(ve.selectedNodeId)">Remove call</button>`;
  } else {
    footer.innerHTML = `<button class="ve-remove-btn" onclick="ve.selectedNodeId && removeNode(ve.selectedNodeId)">Remove node</button>`;
  }
  footer.style.display = '';

  const html = [];

  // Search-connected nodes have no pipeline upstreams (they're search backends, not DAG nodes).
  if (meta.role !== 'source' && !node.isSearchNode) {
    const others = g.nodes.filter(n => n.id !== node.id && !n.isSearchNode && !n.isListNode);
    html.push(`<div class="ve-field"><div class="ve-field-label">Upstreams (upstream=)</div>`);
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

  // ── Search section (discover and similar AcceptsSearch nodes) ─────────────
  if (meta.accepts_search) {
    const searchNodes = g.nodes.filter(nd => pluginMeta(nd.plugin)?.is_search_plugin);
    html.push(`<div class="ve-field"><div class="ve-field-label">Search (search backends)</div>`);
    if (!searchNodes.length) {
      html.push(`<div style="color:var(--muted);font-size:12px;margin-top:4px">Add search-plugin nodes (jackett, rss_search…) to the canvas, then drag the <b>search</b> port to connect them</div>`);
    } else {
      for (const sn of searchNodes) {
        const checked = (node.searchNodeIds || []).includes(sn.id);
        html.push(`<label class="ve-upstream-row">
          <input type="checkbox" ${checked ? 'checked' : ''}
            onchange="toggleSearch(${esc(JSON.stringify(node.id))},${esc(JSON.stringify(sn.id))},this.checked)">
          <code class="ve-upstream-id">${esc(sn.id)}</code>
          <span class="ve-node-search-badge" style="font-size:9px">search</span>
        </label>`);
      }
    }
    html.push('</div>');
  }

  // ── List section (series, movies and similar AcceptsList nodes) ─────────────
  if (meta.accepts_list) {
    const listNodes = g.nodes.filter(nd => pluginMeta(nd.plugin)?.is_list_plugin);
    html.push(`<div class="ve-field"><div class="ve-field-label">List (list sources)</div>`);
    if (!listNodes.length) {
      html.push(`<div style="color:var(--muted);font-size:12px;margin-top:4px">Add list-source nodes (tvdb_favorites, trakt_list…) to the canvas, then drag the <b>list</b> port to connect them</div>`);
    } else {
      for (const ln of listNodes) {
        const checked = (node.listNodeIds || []).includes(ln.id);
        html.push(`<label class="ve-upstream-row">
          <input type="checkbox" ${checked ? 'checked' : ''}
            onchange="toggleList(${esc(JSON.stringify(node.id))},${esc(JSON.stringify(ln.id))},this.checked)">
          <code class="ve-upstream-id">${esc(ln.id)}</code>
          <span class="ve-node-list-badge" style="font-size:9px">list</span>
        </label>`);
      }
    }
    html.push('</div>');
  }

  if (meta.produces?.length || meta.requires?.length) {
    html.push('<div class="ve-field-sep"></div>');
    if (meta.produces?.length)
      html.push(`<div class="ve-field-hint-block"><b>Produces:</b> ${meta.produces.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
    if (meta.requires?.length)
      html.push(`<div class="ve-field-hint-block"><b>Requires:</b> ${meta.requires.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
  }

  const warns = fieldWarnings(node);
  if (warns.length)
    html.push(`<div class="ve-conn-warn">${warns.map(w => `⚠ ${esc(w)}`).join('<br>')}</div>`);

  html.push('<div class="ve-field-sep"></div>');
  if (node.plugin === 'condition') {
    html.push(renderCondRulesWidget(node));
  } else if (meta.schema?.length) {
    for (const f of meta.schema) {
      // Skip 'search' and 'list' fields — managed visually by the sections above.
      if ((f.key === 'search' && meta.accepts_search) || (f.key === 'list' && meta.accepts_list)) continue;
      html.push(renderField(f, node.config, node));
    }
  } else {
    html.push(renderGenericKV(node.config));
  }

  body.innerHTML = html.join('');

  if (node.plugin !== 'condition' && meta.schema?.length) {
    body.querySelectorAll('[data-field]').forEach(el => {
      el.addEventListener('change', () => collectParams(node, meta.schema, body));
      el.addEventListener('input',  () => collectParams(node, meta.schema, body));
    });
  } else if (node.plugin !== 'condition') {
    wireGenericKV(body, node);
  }
}

function toggleUpstream(nodeId, upId, checked) {
  const node = findNode(nodeId);
  if (!node) return;
  if (checked && !node.upstreams.includes(upId)) {
    // Reject if adding this upstream would create a cycle.
    const gi = findNodeGraph(nodeId);
    const g  = gi >= 0 ? ve.graphs[gi] : null;
    if (g && wouldCreateCycle(g.nodes, upId, nodeId)) {
      renderParamPanel(); // re-render to uncheck the checkbox
      return;
    }
    node.upstreams.push(upId);
  } else if (!checked) {
    node.upstreams = node.upstreams.filter(u => u !== upId);
  }
  renderEdges();
  renderGraphNodes();
  onModelChange();
}

function toggleSearch(nodeId, searchId, checked) {
  const node   = findNode(nodeId);
  const target = findNode(searchId);
  if (!node || !target) return;
  if (checked) {
    if (!pluginMeta(target.plugin)?.is_search_plugin || target.isSearchNode) { renderParamPanel(); return; }
    if (!node.searchNodeIds) node.searchNodeIds = [];
    if (!node.searchNodeIds.includes(searchId)) {
      node.searchNodeIds.push(searchId);
      target.isSearchNode   = true;
      target.searchParentId = nodeId;
    }
  } else {
    node.searchNodeIds    = (node.searchNodeIds || []).filter(id => id !== searchId);
    target.isSearchNode   = false;
    delete target.searchParentId;
  }
  renderEdges(); renderGraphNodes(); onModelChange();
}

function toggleList(nodeId, listId, checked) {
  const node   = findNode(nodeId);
  const target = findNode(listId);
  if (!node || !target) return;
  if (checked) {
    if (!pluginMeta(target.plugin)?.is_list_plugin || target.isListNode) { renderParamPanel(); return; }
    if ((node.upstreams || []).includes(listId)) { renderParamPanel(); return; } // already regular upstream
    if (!node.listNodeIds) node.listNodeIds = [];
    if (!node.listNodeIds.includes(listId)) {
      node.listNodeIds.push(listId);
      target.isListNode   = true;
      target.listParentId = nodeId;
    }
  } else {
    node.listNodeIds    = (node.listNodeIds || []).filter(id => id !== listId);
    target.isListNode   = false;
    delete target.listParentId;
  }
  renderEdges(); renderGraphNodes(); onModelChange();
}

// ── field widgets ─────────────────────────────────────────────────────────────

// ── condition plugin — rules editor ──────────────────────────────────────────

// Convert any stored condition config format → [{type:'accept'|'reject', expr}].
function condRulesFromConfig(cfg) {
  if (!cfg) return [];
  if (Array.isArray(cfg.rules)) {
    const rows = [];
    for (const item of cfg.rules) {
      if (item && typeof item === 'object') {
        if (item.reject != null) rows.push({type: 'reject', expr: String(item.reject)});
        if (item.accept != null) rows.push({type: 'accept', expr: String(item.accept)});
      }
    }
    return rows;
  }
  const rows = [];
  if (cfg.reject != null) rows.push({type: 'reject', expr: String(cfg.reject)});
  if (cfg.accept != null) rows.push({type: 'accept', expr: String(cfg.accept)});
  return rows;
}

// Convert [{type, expr}] → the config object the condition plugin expects.
// Single accept/reject stays as a top-level key; multiple rules use the rules list.
function buildCondConfig(rules) {
  const valid = rules.filter(r => r.expr.trim());
  if (valid.length === 0) return {};
  if (valid.length === 1) return {[valid[0].type]: valid[0].expr.trim()};
  return {rules: valid.map(r => ({[r.type]: r.expr.trim()}))};
}

function renderCondRulesWidget(node) {
  const rules = condRulesFromConfig(node.config);
  const rowsHtml = rules.map(r => `
    <div class="ve-cond-row">
      <button class="ve-cond-type ${r.type}" onclick="toggleCondType(this)"
              title="Click to toggle accept / reject">${r.type}</button>
      <input type="text" class="ve-cond-expr" value="${esc(r.expr)}"
             placeholder="expression…"
             oninput="updateCondRules()" onchange="updateCondRules()">
      <button class="ve-cond-del" onclick="deleteCondRule(this)" title="Remove rule">×</button>
    </div>`).join('');
  const body = rowsHtml || '<div class="ve-cond-empty">No rules yet</div>';
  return `<div class="ve-field">
    <div class="ve-field-label">Rules
      <span class="ve-field-hint">— evaluated top to bottom; first match wins; within a rule, reject beats accept</span>
    </div>
    <div class="ve-cond-rules" id="ve-cond-rules">${body}</div>
    <div style="display:flex;gap:6px;margin-top:6px">
      <button class="ve-add-kv" onclick="addCondRule('reject')">+ Reject</button>
      <button class="ve-add-kv" onclick="addCondRule('accept')">+ Accept</button>
    </div>
  </div>`;
}

function addCondRule(type) {
  const container = document.getElementById('ve-cond-rules');
  if (!container) return;
  container.querySelector('.ve-cond-empty')?.remove();
  const row = document.createElement('div');
  row.className = 've-cond-row';
  row.innerHTML = `
    <button class="ve-cond-type ${type}" onclick="toggleCondType(this)"
            title="Click to toggle accept / reject">${type}</button>
    <input type="text" class="ve-cond-expr" placeholder="expression…"
           oninput="updateCondRules()" onchange="updateCondRules()">
    <button class="ve-cond-del" onclick="deleteCondRule(this)" title="Remove rule">×</button>`;
  container.appendChild(row);
  row.querySelector('.ve-cond-expr').focus();
}

function deleteCondRule(btn) {
  const row = btn.closest('.ve-cond-row');
  const container = row?.closest('.ve-cond-rules');
  if (!row || !container) return;
  row.remove();
  if (!container.querySelector('.ve-cond-row')) {
    container.innerHTML = '<div class="ve-cond-empty">No rules yet</div>';
  }
  updateCondRules();
}

function toggleCondType(btn) {
  const newType = btn.classList.contains('reject') ? 'accept' : 'reject';
  btn.className = `ve-cond-type ${newType}`;
  btn.textContent = newType;
  updateCondRules();
}

function updateCondRules() {
  const node = findNode(ve.selectedNodeId);
  if (!node) return;
  const rows = document.querySelectorAll('#ve-cond-rules .ve-cond-row');
  const rules = [...rows].map(row => ({
    type: row.querySelector('.ve-cond-type').classList.contains('reject') ? 'reject' : 'accept',
    expr: row.querySelector('.ve-cond-expr').value,
  }));
  node.config = buildCondConfig(rules);
  renderGraphNodes(); renderEdges(); onModelChange();
}

// renderField renders one schema field widget.  When called inside the function
// body editor (ve.fnEditor.active) and the field is a param reference, it shows
// a read-only "param: name" indicator plus a "× literal" button.  Non-ref fields
// get a "→ param" button so the user can promote them to function parameters.
function renderField(f, config, node) {
  const val      = config[f.key];
  const paramRef = node?._paramRefs?.[f.key];   // paramName if this field is a ref
  const inFnEditor = ve.fnEditor.active;

  // ── param-reference mode: field is driven by a function parameter ──────────
  if (paramRef) {
    const badge = `<span class="ve-param-ref-badge">${esc(paramRef)}</span>`;
    const btn   = `<button class="ve-param-ref-unlink" title="Convert back to a literal value"
        onclick="fnEditorUnlinkParamRef(${esc(JSON.stringify(node.id))},${esc(JSON.stringify(f.key))})">× literal</button>`;
    return `<div class="ve-field ve-field-param-ref">
      <div class="ve-field-label">
        <span>${esc(f.key)}${f.required ? ' <span class="ve-field-required">*</span>' : ''} ${badge}</span>
        ${btn}
      </div>
      ${f.hint ? `<div class="ve-field-hint">— ${esc(f.hint)}</div>` : ''}
      <div class="ve-param-ref-note">value supplied by caller</div></div>`;
  }

  // ── normal editable widget ─────────────────────────────────────────────────
  let widget = '';
  if (f.multiline) {
    const preview = val ? String(val).split('\n')[0].slice(0, 50) : '';
    widget = `<button class="ve-multiline-btn" onclick="openFieldPopup('${esc(f.key)}','${esc(f.hint||'')}')">` +
      (preview ? `<span class="ve-multiline-preview">${esc(preview)}</span>` : '<span class="ve-multiline-empty">click to edit…</span>') +
      '</button>';
  } else {
    switch (f.type) {
      case 'bool': {
        const checked = val != null ? val : (f.default ?? false);
        widget = `<input type="checkbox" data-field="${f.key}" ${checked ? 'checked' : ''}>`;
        break;
      }
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
  }

  // In the function body editor, add a "→ param" button for non-multiline fields.
  const promoteBtn = (inFnEditor && !f.multiline && node)
    ? `<button class="ve-param-promote-btn" title="Expose this field as a function parameter"
        onclick="fnEditorPromoteToParam(${esc(JSON.stringify(node.id))},${esc(JSON.stringify(f.key))},${esc(JSON.stringify(f.type))})">→ param</button>`
    : '';

  return `<div class="ve-field">
    <div class="ve-field-label">
      <span>${esc(f.key)}${f.required ? ' <span class="ve-field-required">*</span>' : ''}</span>
      ${promoteBtn}
    </div>
    ${f.hint ? `<div class="ve-field-hint">— ${esc(f.hint)}</div>` : ''}
    ${widget}</div>`;
}

// Opens the text popup to edit a multiline schema field on the selected node.
function openFieldPopup(fieldKey, hint) {
  const node = findNode(ve.selectedNodeId);
  if (!node) return;
  openTextPopup(fieldKey, hint, String(node.config[fieldKey] ?? ''), text => {
    if (text !== '') node.config[fieldKey] = text;
    else delete node.config[fieldKey];
    renderGraphNodes(); renderEdges(); onModelChange();
    renderParamPanel();
  });
}

function collectParams(node, schema, body) {
  for (const f of schema) {
    if (f.multiline) continue; // saved directly via openFieldPopup
    if (node._paramRefs?.[f.key]) continue; // param ref — not editable inline
    const el = body.querySelector(`[data-field="${f.key}"]`);
    if (!el) continue;
    if (f.type === 'bool')     node.config[f.key] = el.checked;
    else if (f.type === 'int') { const v=parseInt(el.value,10); if(!isNaN(v)) node.config[f.key]=v; else delete node.config[f.key]; }
    else if (f.type !== 'list') { if(el.value!=='') node.config[f.key]=el.value; else delete node.config[f.key]; }
  }
  renderGraphNodes(); renderEdges(); onModelChange();
}

// Like collectParams but for a via-node (re-renders via nodes + edges).

function addTag(inputId, field) {
  const input = document.getElementById(inputId);
  if (!input || !input.value.trim()) return;
  const node = findNode(ve.selectedNodeId);
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
  const node = findNode(ve.selectedNodeId);
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
    node.config = cfg; renderGraphNodes(); renderEdges(); onModelChange();
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

// ── helpers ───────────────────────────────────────────────────────────────────

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
  const gi = findNodeGraph(nodeId);
  const g  = gi >= 0 ? ve.graphs[gi] : null;
  if (!g) return new Set();

  const produced = new Set();
  const visited  = new Set();
  const startNode = g.nodes.find(n => n.id === nodeId);
  const queue = [...(startNode?.upstreams || [])];
  while (queue.length) {
    const id = queue.shift();
    if (visited.has(id)) continue;
    visited.add(id);
    const n    = g.nodes.find(x => x.id === id);
    const meta = n ? pluginMeta(n.plugin) : null;
    if (meta?.produces) meta.produces.forEach(f => produced.add(f));
    if (n) n.upstreams.forEach(u => queue.push(u));
  }
  return produced;
}

function pluginMeta(name) {
  if (ve.userFunctions[name]) {
    const fd = ve.userFunctions[name];
    return {name: fd.name, role: fd.role, description: fd.description, schema: fd.params || [],
            produces: [], requires: [], is_user_function: true};
  }
  return ve.plugins.find(p => p.name === name) || null;
}

// ── Starlark serialisation ────────────────────────────────────────────────────

// Topologically sort nodes (Kahn's BFS) so every upstream is emitted before
// the nodes that reference it. Any cycles (invalid DAGs) are appended last.
function topoSortNodes(nodes) {
  const byId = new Map(nodes.map(n => [n.id, n]));
  const deg  = new Map(nodes.map(n => [n.id, 0]));
  const succ = new Map(nodes.map(n => [n.id, []]));

  for (const n of nodes) {
    for (const upId of (n.upstreams ?? [])) {
      if (byId.has(upId)) {
        deg.set(n.id, (deg.get(n.id) ?? 0) + 1);
        succ.get(upId)?.push(n.id);
      }
    }
  }

  const queue  = nodes.filter(n => !deg.get(n.id)).map(n => n.id);
  const result = [];
  const seen   = new Set();

  while (queue.length) {
    const id = queue.shift();
    if (seen.has(id)) continue;
    seen.add(id);
    const n = byId.get(id);
    if (n) result.push(n);
    for (const sid of (succ.get(id) ?? [])) {
      const d = (deg.get(sid) ?? 1) - 1;
      deg.set(sid, d);
      if (d <= 0 && !seen.has(sid)) queue.push(sid);
    }
  }

  // Append any remaining nodes (cycles — shouldn't occur in a valid DAG).
  for (const n of nodes) {
    if (!seen.has(n.id)) result.push(n);
  }

  return result;
}

// ── Copy / Paste ───────────────────────────────────────────────────────────────

// copySelected copies the current single-selection or multi-selection to
// ve.clipboard. Sub-nodes (search/list) are excluded; internal edges between
// copied nodes are preserved so pasting a group reconnects them automatically.
function copySelected() {
  const ids = ve.selectedNodeIds.size > 0
    ? [...ve.selectedNodeIds]
    : ve.selectedNodeId ? [ve.selectedNodeId] : [];
  if (!ids.length) return;

  const nodes = ids.map(findNode).filter(n => n && !n.isSearchNode && !n.isListNode);
  if (!nodes.length) return;

  // Store positions relative to the group centroid so paste can reconstruct layout.
  const cx = nodes.reduce((s, n) => s + (n.x ?? 0), 0) / nodes.length;
  const cy = nodes.reduce((s, n) => s + (n.y ?? 0), 0) / nodes.length;

  const idSet = new Set(ids);
  const edges = [];
  for (const n of nodes) {
    for (const up of (n.upstreams || [])) {
      if (idSet.has(up)) edges.push({from: up, to: n.id});
    }
  }

  ve.clipboard = {
    nodes: nodes.map(n => ({
      plugin:         n.plugin,
      config:         JSON.parse(JSON.stringify(n.config || {})),
      comment:        n.comment || '',
      isFunctionCall: !!n.isFunctionCall,
      origId:         n.id,
      relX:           (n.x ?? 0) - cx,
      relY:           (n.y ?? 0) - cy,
    })),
    edges,
  };

  updatePasteButton();
  const count = ve.clipboard.nodes.length;
  setSyncNote(`Copied ${count} node${count !== 1 ? 's' : ''}`);
  setTimeout(() => setSyncNote(ve.graphs.length > 1 ? `Showing ${ve.graphs.length} pipelines` : ''), 1500);
}

// pasteClipboard inserts copies of the clipboard nodes into the active pipeline.
// Each node gets a fresh ID; internal connections are re-wired; external
// upstreams are left empty for the user to connect. Works across pipelines.
function pasteClipboard() {
  if (!ve.clipboard?.nodes?.length) return;
  const g = activeG();
  if (!g) return;

  // Place pasted nodes centred on the visible viewport, offset slightly so
  // repeated pastes don't stack exactly on top of each other.
  const body  = document.getElementById('ve-canvas-body');
  const PASTE_OFFSET = 40;
  const baseX = body ? Math.max(20, body.scrollLeft / ve_zoom + body.clientWidth  / ve_zoom / 2) + PASTE_OFFSET : 200;
  const baseY = body ? Math.max(20, body.scrollTop  / ve_zoom + body.clientHeight / ve_zoom / 2) + PASTE_OFFSET : 200;

  const idMap  = {};  // origId → newId
  const newIds = [];

  for (const entry of ve.clipboard.nodes) {
    const newId = genId(entry.plugin);
    idMap[entry.origId] = newId;
    newIds.push(newId);

    const node = {
      id:            newId,
      plugin:        entry.plugin,
      config:        JSON.parse(JSON.stringify(entry.config)),
      comment:       entry.comment,
      upstreams:     [],
      searchNodeIds: [],
      listNodeIds:   [],
      x: baseX + entry.relX,
      y: baseY + entry.relY,
    };
    if (entry.isFunctionCall) {
      node.isFunctionCall  = true;
      node.funcCallKey     = newId;
      node.internalNodeIds = [];
      node.returnNodeId    = '';
    }
    g.nodes.push(node);
  }

  // Reconnect internal edges using the new IDs.
  for (const {from, to} of (ve.clipboard.edges || [])) {
    const newTo   = idMap[to];
    const newFrom = idMap[from];
    if (!newTo || !newFrom) continue;
    const node = g.nodes.find(n => n.id === newTo);
    if (node) node.upstreams.push(newFrom);
  }

  // Select the newly pasted nodes.
  clearMultiSelect();
  if (newIds.length === 1) {
    ve.selectedNodeId = newIds[0];
  } else {
    for (const id of newIds) ve.selectedNodeIds.add(id);
    updateExtractButton();
  }

  veRender();
  onModelChange();
}

function updatePasteButton() {
  const btn = document.getElementById('ve-paste-btn');
  if (!btn) return;
  if (!ve.clipboard?.nodes?.length) {
    btn.style.display = 'none';
    return;
  }
  const n = ve.clipboard.nodes.length;
  btn.textContent  = `Paste (${n} node${n !== 1 ? 's' : ''})`;
  btn.style.display = '';
}

// ── Extract to function ────────────────────────────────────────────────────────

// validateExtraction checks that the multi-selection can form a valid function:
// - All nodes in the same pipeline, none are sub-nodes or function calls
// - Exactly one "exit" (the node whose output feeds the rest of the pipeline)
// Returns {ok, error, graphIdx, entryUpstreams, returnNodeId}.
function validateExtraction() {
  const ids = ve.selectedNodeIds;
  if (!multiSelectIsValid()) return {ok: false, error: 'Select at least one main node (Cmd/Ctrl+click).'};

  const graphIdx = findNodeGraph([...ids][0]);
  const g = ve.graphs[graphIdx];
  const allMain = g.nodes.filter(n => !n.isSearchNode && !n.isListNode);

  // Entry upstreams: upstreams of selected nodes that are outside the selection.
  const entryUpstreams = [];
  for (const id of ids) {
    for (const up of (findNode(id)?.upstreams || [])) {
      if (!ids.has(up)) entryUpstreams.push(up);
    }
  }

  // Exit nodes: selected nodes that have at least one downstream outside the selection.
  const exitNodes = [...ids].filter(id =>
    allMain.some(n => !ids.has(n.id) && (n.upstreams || []).includes(id))
  );
  // Terminal within the selection: selected nodes that no *other selected node*
  // consumes. This correctly handles sink chains like deluge→email where both
  // are selected — deluge is consumed by email (also selected), so only email
  // is terminal. The old check used non-selected nodes only, which made both
  // appear terminal when neither had an external consumer.
  const terminalSelected = [...ids].filter(id =>
    ![...ids].some(other => other !== id && (findNode(other)?.upstreams || []).includes(id))
  );

  let returnNodeId;
  if (exitNodes.length === 1) {
    returnNodeId = exitNodes[0];
  } else if (exitNodes.length === 0 && terminalSelected.length === 1) {
    returnNodeId = terminalSelected[0];
  } else if (exitNodes.length === 0 && terminalSelected.length > 1) {
    return {ok: false, error: 'Multiple terminal nodes — connect them first or pick a subset.'};
  } else {
    return {ok: false, error: 'Selection has multiple outputs. A function can only return one node.'};
  }

  return {ok: true, graphIdx, entryUpstreams, returnNodeId};
}

// inferExtractionParams collects all config key/value pairs across selected nodes
// as candidate function parameters, deduplicating names.
function inferExtractionParams() {
  const params = [];
  const usedNames = new Set(['upstream']); // reserved
  let dedupCounter = 0;
  for (const id of ve.selectedNodeIds) {
    const n = findNode(id);
    if (!n) continue;
    for (const [key, val] of Object.entries(n.config || {})) {
      if (val === null || val === undefined || val === '') continue;
      let pName = key;
      if (usedNames.has(pName)) pName = `${n.plugin.replace(/[^a-zA-Z0-9]/g, '_')}_${key}`;
      if (usedNames.has(pName)) pName = `${pName}_${dedupCounter++}`;
      usedNames.add(pName);
      const type = Array.isArray(val) ? 'list'
                 : typeof val === 'boolean' ? 'bool'
                 : typeof val === 'number'  ? 'int' : 'string';
      params.push({nodeId: id, configKey: key, paramName: pName, type, defaultValue: val, include: true});
    }
  }
  return params;
}

// nodesToFunctionSource generates the Starlark def block text for the extracted function.
function nodesToFunctionSource(funcName, params, selectedIds, validation, graph) {
  const {entryUpstreams, returnNodeId} = validation;

  // Map: nodeId → configKey → paramName (for included params).
  const paramLookup = {};
  for (const p of params) {
    if (!p.include) continue;
    (paramLookup[p.nodeId] = paramLookup[p.nodeId] || {})[p.configKey] = p.paramName;
  }

  const selectedNodes = graph.nodes.filter(n => selectedIds.has(n.id) && !n.isSearchNode && !n.isListNode);
  const ordered = topoSortNodes(selectedNodes);

  const lines = [];
  for (const p of params) {
    if (p.include) {
      // Persist non-string types so they survive a restart (the signature has
      // no defaults, so the Go parser can't infer types on its own).
      const typeAnno = (p.type && p.type !== 'string') ? `type=${p.type}  ` : '';
      lines.push((`# pipeliner:param ${p.paramName}  ${typeAnno}${p.hint || ''}`).trimEnd());
    }
  }

  // Parameters are required (no default in the def signature); the actual
  // values live at the call site so every use is explicit.
  const sigParams = params.filter(p => p.include).map(p => p.paramName);
  // Only include 'upstream' when there are entry upstreams (processors/sinks).
  const hasEntryUpstreams = entryUpstreams.length > 0;
  const sig = hasEntryUpstreams ? ['upstream', ...sigParams].join(', ') : sigParams.join(', ');
  lines.push(`def ${funcName}(${sig}):`);
  if (ordered.length === 0) lines.push('    pass');

  for (const n of ordered) {
    const role = pluginMeta(n.plugin)?.role || 'processor';
    const internalUps = (n.upstreams || []).filter(u => selectedIds.has(u));
    const externalUps = (n.upstreams || []).filter(u => !selectedIds.has(u));

    let upExpr;
    if (externalUps.length > 0 && internalUps.length === 0) {
      upExpr = 'upstream';
    } else if (internalUps.length > 0 && externalUps.length === 0) {
      upExpr = internalUps.length === 1 ? internalUps[0] : `merge(${internalUps.join(', ')})`;
    } else if (internalUps.length > 0 && externalUps.length > 0) {
      upExpr = `merge(upstream, ${internalUps.join(', ')})`;
    } else {
      upExpr = null;
    }

    const cfgParts = Object.entries(n.config || {}).map(([k, v]) => {
      const pName = paramLookup[n.id]?.[k];
      return `${k}=${pName ?? valToStar(v)}`;
    });

    // list= and search= are not in n.config (the server strips them); they live
    // in n.searchNodeIds / n.listNodeIds as canvas sub-nodes.
    if (n.searchNodeIds?.length) {
      const items = n.searchNodeIds
        .map(id => graph.nodes.find(x => x.id === id)).filter(Boolean)
        .map(viaNodeToStar).join(', ');
      cfgParts.push(`search=[${items}]`);
    }
    if (n.listNodeIds?.length) {
      const items = n.listNodeIds
        .map(id => graph.nodes.find(x => x.id === id)).filter(Boolean)
        .map(viaNodeToStar).join(', ');
      cfgParts.push(`list=[${items}]`);
    }

    if (role === 'source') {
      lines.push(`    ${n.id} = input(${[starLit(n.plugin), ...cfgParts].join(', ')})`);
    } else if (role === 'processor') {
      const parts = [starLit(n.plugin)];
      if (upExpr) parts.push(`upstream=${upExpr}`);
      parts.push(...cfgParts);
      lines.push(`    ${n.id} = process(${parts.join(', ')})`);
    } else {
      const parts = [starLit(n.plugin)];
      if (upExpr) parts.push(`upstream=${upExpr}`);
      parts.push(...cfgParts);
      lines.push(`    ${n.id} = output(${parts.join(', ')})`);
    }
  }

  if (returnNodeId) lines.push(`    return ${returnNodeId}`);
  return lines.join('\n') + '\n';
}

// openExtractDialog shows the extraction modal: function name + parameter table.
function openExtractDialog() {
  const validation = validateExtraction();
  if (!validation.ok) { alert(validation.error); return; }

  const params = inferExtractionParams();
  const graphIdx = validation.graphIdx;

  // Suggest a function name from the dominant plugin name.
  const pluginNames = [...ve.selectedNodeIds].map(id => findNode(id)?.plugin || '').filter(Boolean);
  const dominant = pluginNames[Math.round((pluginNames.length - 1) / 2)] || 'my_function';
  const suggestedName = dominant.replace(/[^a-zA-Z0-9]/g, '_') + '_fn';

  let modal = document.getElementById('ve-extract-modal');
  modal?.remove();
  modal = document.createElement('div');
  modal.id = 've-extract-modal';
  modal.className = 've-extract-modal';

  const paramRows = params.map((p, i) => `
    <tr>
      <td><input type="checkbox" data-pi="${i}" ${p.include ? 'checked' : ''}></td>
      <td><input type="text" class="ep-name" data-pi="${i}" value="${esc(p.paramName)}"></td>
      <td><input type="text" class="ep-hint" data-pi="${i}" value="${esc(p.hint || '')}" placeholder="optional description…"></td>
      <td style="color:var(--muted);font-size:11px">${esc(p.type)}</td>
      <td style="color:var(--muted);font-size:11px;max-width:80px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"
          title="${esc(String(p.defaultValue))}">${esc(String(p.defaultValue))}</td>
    </tr>`).join('');

  modal.innerHTML = `
    <div class="ve-extract-inner">
      <div class="ve-extract-header">
        <span class="ve-extract-title">Extract to function</span>
        <button class="ve-extract-close" id="ve-extract-close">×</button>
      </div>
      <div class="ve-extract-body">
        <div class="ve-extract-field">
          <label>Function name</label>
          <input type="text" id="ve-extract-name" value="${esc(suggestedName)}" placeholder="my_function" spellcheck="false">
        </div>
        ${params.length ? `
        <div class="ve-extract-field">
          <label>Parameters <span style="font-weight:400;text-transform:none;letter-spacing:0">(uncheck to hardcode the value)</span></label>
          <table class="ve-extract-param-table">
            <thead><tr><th></th><th>Param name</th><th>Description</th><th>Type</th><th>Call value</th></tr></thead>
            <tbody id="ve-extract-params">${paramRows}</tbody>
          </table>
        </div>` : '<p style="color:var(--muted);font-size:12px">No configurable values — the function will have only an upstream parameter.</p>'}
        <div class="ve-extract-error" id="ve-extract-err"></div>
      </div>
      <div class="ve-extract-footer">
        <button class="ve-extract-cancel" id="ve-extract-cancel">Cancel</button>
        <button class="ve-extract-create" id="ve-extract-create">Create function</button>
      </div>
    </div>`;
  document.body.appendChild(modal);

  const close = () => modal.remove();
  document.getElementById('ve-extract-close').onclick  = close;
  document.getElementById('ve-extract-cancel').onclick = close;
  modal.onkeydown = e => { if (e.key === 'Escape') close(); };
  modal.addEventListener('pointerdown', e => { if (e.target === modal) close(); });

  document.getElementById('ve-extract-create').onclick = () => {
    const name = document.getElementById('ve-extract-name').value.trim();
    const errEl = document.getElementById('ve-extract-err');
    errEl.textContent = '';
    if (!name || !/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(name)) {
      errEl.textContent = 'Function name must be a valid identifier (letters, digits, underscores).';
      return;
    }
    // Read back param state from the table.
    const tbody = document.getElementById('ve-extract-params');
    if (tbody) {
      tbody.querySelectorAll('tr').forEach(row => {
        const pi = parseInt(row.querySelector('[data-pi]')?.dataset.pi ?? '-1');
        if (pi < 0 || pi >= params.length) return;
        params[pi].include   = row.querySelector('input[type=checkbox]').checked;
        params[pi].paramName = row.querySelector('.ep-name').value.trim() || params[pi].paramName;
        params[pi].hint      = row.querySelector('.ep-hint').value.trim();
      });
    }
    // Check for duplicate param names.
    const included = params.filter(p => p.include);
    const nameSet = new Set(included.map(p => p.paramName));
    if (nameSet.size !== included.length) {
      errEl.textContent = 'Duplicate parameter names — each parameter must have a unique name.';
      return;
    }
    close();
    performExtraction(name, params, validation, graphIdx);
  };

  document.getElementById('ve-extract-name').focus();
  document.getElementById('ve-extract-name').select();
}

// performExtraction replaces the selected nodes with a function call node and
// registers the new user function definition.
function performExtraction(funcName, params, validation, graphIdx) {
  const {entryUpstreams, returnNodeId} = validation;
  const selectedIds = new Set(ve.selectedNodeIds); // snapshot before clearing
  const g = ve.graphs[graphIdx];

  // Generate the function source text.
  const sourceText = nodesToFunctionSource(funcName, params, selectedIds, validation, g);

  // Infer role from body.
  const role = sourceText.includes(' = input(') ? 'source'
             : sourceText.includes(' = output(') ? 'sink' : 'processor';

  // Build the function call node's config args from included params.
  const callArgs = {};
  for (const p of params) {
    if (p.include) callArgs[p.paramName] = p.defaultValue;
  }

  // Generate a unique call node ID.
  const callNodeId = genId(funcName);

  // Reroute: nodes that had the return node as an upstream now point to the call node.
  for (const n of g.nodes) {
    if (!selectedIds.has(n.id)) {
      n.upstreams = (n.upstreams || []).map(u => u === returnNodeId ? callNodeId : u);
    }
  }

  // Compute centroid position of selected nodes so the call node lands in place.
  const positioned = [...selectedIds].map(findNode).filter(n => n?.x != null && n?.y != null);
  const cx = positioned.length ? positioned.reduce((s, n) => s + n.x, 0) / positioned.length : null;
  const cy = positioned.length ? positioned.reduce((s, n) => s + n.y, 0) / positioned.length : null;

  // Collect list/search sub-nodes owned by the selected nodes so they don't
  // remain as orphans on the canvas after extraction.
  const subNodeIds = new Set();
  for (const id of selectedIds) {
    const n = g.nodes.find(x => x.id === id);
    if (!n) continue;
    for (const sid of (n.listNodeIds   || [])) subNodeIds.add(sid);
    for (const sid of (n.searchNodeIds || [])) subNodeIds.add(sid);
  }

  // Remove selected nodes (and their sub-nodes) and insert the function call node.
  g.nodes = g.nodes.filter(n => !selectedIds.has(n.id) && !subNodeIds.has(n.id));
  g.nodes.push({
    id:             callNodeId,
    plugin:         funcName,
    config:         callArgs,
    upstreams:      entryUpstreams,
    searchNodeIds:  [],
    listNodeIds:    [],
    comment:        '',
    isFunctionCall: true,
    funcCallKey:    callNodeId,
    internalNodeIds: [...selectedIds],
    returnNodeId,
    x: cx, y: cy,
  });

  // Register the user function.
  ve.userFunctions[funcName] = {
    name:        funcName,
    role,
    description: '',
    params:      params.filter(p => p.include).map(p => ({
      key: p.paramName, type: p.type, required: true, default: null, hint: p.hint || '',
    })),
    _sourceText: sourceText,
  };

  clearMultiSelect();
  ve.selectedNodeId = callNodeId;
  // Force a full palette re-render so the new function appears immediately in
  // the Functions section (syncPaletteState only re-renders on disabled state
  // change, so veRender() alone won't update the palette for a new function).
  renderPalette(document.getElementById('ve-search')?.value ?? '');
  veRender();
  onModelChange();
}

// expandAndRemoveFunction replaces every call site of funcName with the
// function's constituent nodes inline (expanding the collapsed card), then
// removes the function definition. If the function has no call sites the
// definition is simply deleted.
async function expandAndRemoveFunction(funcName) {
  const usedIn = [];
  for (const g of ve.graphs) {
    const count = g.nodes.filter(n => n.isFunctionCall && n.plugin === funcName).length;
    if (count) usedIn.push(`"${g.name}" (${count} call${count !== 1 ? 's' : ''})`);
  }
  const usageNote = usedIn.length
    ? `\n\nAll call sites will be expanded inline: ${usedIn.join(', ')}.`
    : '';
  if (!confirm(`Expand and remove function "${funcName}"?${usageNote}`)) return;

  // Fetch the fully-resolved parse response so we can recover internal nodes.
  const content = document.getElementById('config-editor')?.value ?? dagToStarlark();
  let data;
  try {
    const r = await fetch('/api/config/parse', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content}),
    });
    if (!r.ok) { alert('Could not expand function: server error ' + r.status); return; }
    data = await r.json();
  } catch (e) {
    alert('Could not expand function: ' + e);
    return;
  }

  // Rebuild each graph, expanding call sites of funcName and leaving all
  // other call sites collapsed as before.
  const entries = Object.entries(data.graphs || {});
  ve.graphs = entries.map(([name, graph]) => {
    const rawNodes    = graph.nodes || [];
    const allCalls    = graph.function_calls || [];
    const expandCalls = allCalls.filter(fc => fc.func === funcName);
    const keepCalls   = allCalls.filter(fc => fc.func !== funcName);

    // Internal IDs from calls we are NOT expanding still get hidden.
    const hiddenIds = new Set();
    for (const fc of keepCalls) {
      for (const nid of fc.internal_node_ids) hiddenIds.add(nid);
    }

    // First pass: all nodes except hidden-internal ones (expanded internal
    // nodes pass through because they are NOT in hiddenIds).
    const nodes = rawNodes
      .filter(n => !hiddenIds.has(n.id))
      .map(n => ({
        id: n.id, plugin: n.plugin, config: n.config || {}, upstreams: n.upstreams || [],
        searchNodeIds: [], listNodeIds: [], comment: n.comment || '',
        x: n.x ?? null, y: n.y ?? null,
      }));

    // Second pass: search/list sub-nodes (same logic as textToVisualSync).
    for (let ni = 0; ni < rawNodes.length; ni++) {
      const raw = rawNodes[ni];
      if (hiddenIds.has(raw.id)) continue;
      const nodeIdx = nodes.findIndex(n => n.id === raw.id);
      if (nodeIdx < 0) continue;
      for (let si = 0; si < (raw.search || []).length; si++) {
        const s = raw.search[si];
        const id = `${raw.id}__search__${si}`;
        nodes[nodeIdx].searchNodeIds.push(id);
        nodes.push({ id, plugin: s.plugin, config: s.config || {},
          upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
          isSearchNode: true, searchParentId: raw.id, x: s.x ?? null, y: s.y ?? null });
      }
      for (let li = 0; li < (raw.list || []).length; li++) {
        const l = raw.list[li];
        const id = `${raw.id}__list__${li}`;
        nodes[nodeIdx].listNodeIds.push(id);
        nodes.push({ id, plugin: l.plugin, config: l.config || {},
          upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
          isListNode: true, listParentId: raw.id, x: l.x ?? null, y: l.y ?? null });
      }
    }

    // Third pass: synthetic collapsed nodes for calls we are keeping.
    for (const fc of keepCalls) {
      const internalSet = new Set(fc.internal_node_ids);
      const entryUpstreams = [];
      for (const n of rawNodes) {
        if (!internalSet.has(n.id)) continue;
        for (const up of (n.upstreams || [])) {
          if (!internalSet.has(up)) entryUpstreams.push(up);
        }
      }
      for (const n of nodes) {
        if (n.isFunctionCall) continue;
        n.upstreams = n.upstreams.map(u => u === fc.return_node_id ? fc.call_key : u);
      }
      nodes.push({
        id: fc.call_key, plugin: fc.func, config: fc.args || {},
        upstreams: entryUpstreams, searchNodeIds: [], listNodeIds: [], comment: '',
        isFunctionCall: true, funcCallKey: fc.call_key,
        internalNodeIds: fc.internal_node_ids, returnNodeId: fc.return_node_id,
        x: fc.x ?? null, y: fc.y ?? null,
      });
    }

    const existingG = ve.graphs.find(g => g.name === name) || {};
    const _hasLayout = nodes.some(n => !n.isSearchNode && !n.isListNode && n.x != null && n.y != null);
    return {
      name,
      schedule: graph.schedule || existingG.schedule || '',
      comment:  graph.comment  || existingG.comment  || '',
      nodes, _hasLayout,
    };
  });

  // Update ve.nextId so freshly-placed nodes get unique IDs.
  ve.nextId = ve.graphs.flatMap(g => g.nodes).reduce((max, n) => {
    const m = n.id.match(/_(\d+)$/);
    return m ? Math.max(max, parseInt(m[1]) + 1) : max;
  }, ve.nextId ?? 0);

  // Remove the function definition.
  delete ve.userFunctions[funcName];

  if (ve.selectedNodeId) {
    const sel = findNode(ve.selectedNodeId);
    if (!sel || (sel.isFunctionCall && sel.plugin === funcName)) ve.selectedNodeId = null;
  }
  clearMultiSelect();
  initLayout();
  renderPalette(document.getElementById('ve-search')?.value ?? '');
  veRender();
  onModelChange();
}

// extractFunctionSource returns the full text of a user function (including its
// preceding # pipeliner: comments and the def block) from the raw config string.
function extractFunctionSource(src, funcName) {
  const lines = src.split('\n');
  let start = -1;
  for (let i = 0; i < lines.length; i++) {
    if (/^def\s+/.test(lines[i].trimStart()) &&
        lines[i].trimStart().startsWith(`def ${funcName}(`)) {
      // Walk backwards to include preceding comment block.
      let s = i;
      while (s > 0 && lines[s - 1].trimStart().startsWith('#')) s--;
      start = s;
      break;
    }
  }
  if (start < 0) return '';
  // Collect pre-def comments + def line + indented body.
  // Only apply the "stop at next top-level statement" check AFTER we have seen
  // the def line — comment lines above the def are non-indented but are part of
  // the function block and must not trigger early termination.
  const result = [];
  let pastDef = false;
  for (let i = start; i < lines.length; i++) {
    result.push(lines[i]);
    if (!pastDef && /^def\s+/.test(lines[i].trimStart())) {
      pastDef = true;
      continue; // def line itself is never the terminator
    }
    if (pastDef && lines[i] !== '' && !/^\s/.test(lines[i])) {
      result.pop(); // stop before the next top-level statement
      break;
    }
  }
  return result.join('\n');
}

// ── visual function editor ─────────────────────────────────────────────────────

// fnSplitArgs splits a function call argument string into tokens while
// respecting nested brackets. Used by fnParseCallArgs.
function fnSplitArgs(s) {
  const parts = [];
  let depth = 0, start = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === '(' || c === '[' || c === '{') depth++;
    else if (c === ')' || c === ']' || c === '}') depth--;
    else if (c === ',' && depth === 0) {
      parts.push(s.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(s.slice(start));
  return parts;
}

// fnParseCallArgs extracts the plugin name and keyword args from the raw
// arguments string of an input/process/output(...) call.
function fnParseCallArgs(argsRaw) {
  const parts = fnSplitArgs(argsRaw);
  const pluginRaw = (parts[0] || '').trim();
  const plugin = pluginRaw.replace(/^["']|["']$/g, '');
  const kwargs = {};
  for (let i = 1; i < parts.length; i++) {
    const p = parts[i].trim();
    const eq = p.indexOf('=');
    if (eq < 0) continue;
    kwargs[p.slice(0, eq).trim()] = p.slice(eq + 1).trim();
  }
  return {plugin, kwargs};
}

// fnParseUpstreamExpr parses an upstream= value into an array of upstream IDs,
// replacing the bare 'upstream' identifier with '_upstream' (pseudo-node).
function fnParseUpstreamExpr(expr) {
  const t = expr.trim();
  if (t === 'upstream') return ['_upstream'];
  if (t.startsWith('merge(') && t.endsWith(')')) {
    return fnSplitArgs(t.slice(6, -1))
      .map(s => s.trim())
      .map(s => s === 'upstream' ? '_upstream' : s);
  }
  return [t];
}

// fnParseLiteral converts a Starlark literal string to a JS value.
function fnParseLiteral(s) {
  s = s.trim();
  if (s === 'True')  return true;
  if (s === 'False') return false;
  if (s === 'None')  return null;
  if (/^-?\d+$/.test(s)) return parseInt(s, 10);
  if (/^-?\d*\.\d+$/.test(s)) return parseFloat(s);
  if ((s[0] === '"' && s[s.length-1] === '"') ||
      (s[0] === "'" && s[s.length-1] === "'")) {
    return s.slice(1, -1)
      .replace(/\\n/g, '\n').replace(/\\t/g, '\t')
      .replace(/\\"/g, '"').replace(/\\'/g, "'").replace(/\\\\/g, '\\');
  }
  if (s.startsWith('[')) {
    try {
      const inner = s.slice(1, -1).trim();
      if (!inner) return [];
      return fnSplitArgs(inner).map(item => fnParseLiteral(item.trim()));
    } catch (_) { return s; }
  }
  if (s.startsWith('{')) {
    try {
      const inner = s.slice(1, -1).trim();
      if (!inner) return {};
      const obj = {};
      for (const pair of fnSplitArgs(inner)) {
        const ci = pair.indexOf(':');
        if (ci < 0) continue;
        const k = fnParseLiteral(pair.slice(0, ci).trim());
        const v = fnParseLiteral(pair.slice(ci + 1).trim());
        if (typeof k === 'string') obj[k] = v;
      }
      return obj;
    } catch (_) { return s; }
  }
  return s; // bare identifier or unparseable — return as string
}

// parseFunctionBodyNodes extracts the internal canvas nodes from a function's
// _sourceText. Param references in kwargs (bare identifiers matching param names)
// are tracked in n._paramRefs = {configKey: paramName} so nodesToFunctionSource
// can re-emit them correctly without substituting literal values.
function parseFunctionBodyNodes(funcName) {
  const fd = ve.userFunctions[funcName];
  if (!fd?._sourceText) return null;

  const paramNames = new Set((fd.params || []).map(p => p.key));
  const lines = fd._sourceText.split('\n');
  const defIdx = lines.findIndex(l =>
    l.trimStart().startsWith(`def ${funcName}(`) && /^def\s+/.test(l.trimStart()));
  if (defIdx < 0) return null;

  const nodes = [];
  let returnNodeId = null;

  for (let i = defIdx + 1; i < lines.length; i++) {
    const raw = lines[i];
    if (!raw.trim()) continue;
    if (!/^\s/.test(raw)) break; // end of function body

    const line = raw.trim();
    if (line.startsWith('return ')) {
      returnNodeId = line.slice(7).trim();
      continue;
    }

    // Match: var = input/process/output("plugin", ...kwargs...)
    // Use a non-greedy match to the last ) on the line.
    const m = line.match(/^(\w+)\s*=\s*(input|process|output)\((.+)\)\s*$/);
    if (!m) continue;

    const [, varName, , argsRaw] = m;
    const {plugin, kwargs} = fnParseCallArgs(argsRaw);

    const config     = {};
    const paramRefs  = {};   // {configKey: paramName}
    let   upstreams  = [];
    let   searchRaw  = null;
    let   listRaw    = null;

    for (const [k, v] of Object.entries(kwargs)) {
      const val = v.trim();
      if (k === 'upstream') {
        upstreams = fnParseUpstreamExpr(val);
      } else if (k === 'search') {
        searchRaw = val;
      } else if (k === 'list') {
        listRaw = val;
      } else if (paramNames.has(val)) {
        // Bare identifier matches a function param → it's a param reference.
        const pDef = (fd.params || []).find(p => p.key === val);
        paramRefs[k] = val;
        config[k] = pDef?.default != null ? pDef.default : emptyForType(pDef?.type || 'string');
      } else {
        config[k] = fnParseLiteral(val);
      }
    }

    const node = {
      id: varName, plugin, config, upstreams,
      searchNodeIds: [], listNodeIds: [], comment: '',
      x: null, y: null,
    };
    if (Object.keys(paramRefs).length)  node._paramRefs = paramRefs;
    if (searchRaw !== null)             node._searchRaw  = searchRaw;
    if (listRaw   !== null)             node._listRaw    = listRaw;

    // Parse search/list sub-plugin lists into canvas sub-nodes.
    if (searchRaw) {
      const items = fnParseSubPluginList(searchRaw);
      items.forEach((item, si) => {
        const id = `${varName}__search__${si}`;
        node.searchNodeIds.push(id);
        nodes.push({id, plugin: item.plugin, config: item.config,
          upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
          isSearchNode: true, searchParentId: varName, x: null, y: null});
      });
    }
    if (listRaw) {
      const items = fnParseSubPluginList(listRaw);
      items.forEach((item, li) => {
        const id = `${varName}__list__${li}`;
        if (!node.listNodeIds) node.listNodeIds = [];
        node.listNodeIds.push(id);
        nodes.push({id, plugin: item.plugin, config: item.config,
          upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
          isListNode: true, listParentId: varName, x: null, y: null});
      });
    }

    nodes.push(node);
  }

  return {nodes, returnNodeId};
}

// fnParseSubPluginList parses a Starlark list of sub-plugin dicts:
//   [{"name": "plugin", "key": val, ...}, ...]
// into [{plugin, config}, ...].
function fnParseSubPluginList(raw) {
  const t = raw.trim();
  if (!t.startsWith('[')) return [];
  const inner = t.slice(1, -1).trim();
  if (!inner) return [];
  return fnSplitArgs(inner).map(item => {
    const d = item.trim();
    if (!d.startsWith('{')) return null;
    const inner2 = d.slice(1, -1).trim();
    const obj = {};
    for (const pair of fnSplitArgs(inner2)) {
      const ci = pair.indexOf(':');
      if (ci < 0) continue;
      const k = fnParseLiteral(pair.slice(0, ci).trim());
      const v = fnParseLiteral(pair.slice(ci + 1).trim());
      if (typeof k === 'string') obj[k] = v;
    }
    if (!obj.name) return null;
    const plugin = String(obj.name);
    const config = {...obj};
    delete config.name;
    return {plugin, config};
  }).filter(Boolean);
}

// openFunctionEditor enters function-editing mode for the named function.
// The existing pipeline canvas is swapped out for the function's internal
// nodes so all existing canvas interactions (drag, connect, palette) work
// without modification.
function openFunctionEditor(funcName) {
  if (ve.fnEditor.active) return; // already editing
  const fd = ve.userFunctions[funcName];
  if (!fd) return;

  const parsed = parseFunctionBodyNodes(funcName);
  if (!parsed) {
    alert(`Cannot open function "${funcName}": the function body could not be parsed. Try editing it in the text editor first.`);
    return;
  }

  const {nodes, returnNodeId} = parsed;
  const allNodes = [...nodes];

  // For processor/sink functions, prepend the upstream pseudo-node so users
  // can visually connect entry nodes to their function's upstream parameter.
  if (fd.role !== 'source') {
    allNodes.unshift({
      id: '_upstream', plugin: '_upstream', config: {},
      upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
      isUpstreamPseudo: true, x: null, y: null,
    });
  }

  // Snapshot current canvas state for restoration on exit.
  ve.fnEditor = {
    active:         true,
    funcName,
    savedGraphs:    ve.graphs,
    savedActive:    ve.activeGraph,
    savedNextId:    ve.nextId,
    savedSelected:  ve.selectedNodeId,
    returnNodeId,
    // Snapshot of params at open time (for change detection on save).
    paramsSnapshot: JSON.parse(JSON.stringify(fd.params || [])),
    paramsOpen:     false,
  };

  ve.graphs      = [{name: funcName, schedule: '', comment: '', nodes: allNodes, _hasLayout: false}];
  ve.activeGraph = 0;
  ve.selectedNodeId = null;

  // Advance nextId past any existing counter values inside the function.
  ve.nextId = allNodes.reduce((max, n) => {
    const m = n.id.match(/_(\d+)$/);
    return m ? Math.max(max, parseInt(m[1]) + 1) : max;
  }, ve.nextId);

  clearMultiSelect();
  ve_panX = 0; ve_panY = 0;

  // Immediately wipe the SVG so pipeline edges don't linger while the
  // function body layout is being calculated and rendered.
  const svgEl = document.getElementById('ve-graph-svg');
  if (svgEl) svgEl.innerHTML = '';

  // Show the function editor toolbar, hide the pipeline toolbar.
  const pbar = document.getElementById('ve-pipeline-bar');
  const fbar = document.getElementById('ve-fn-bar');
  if (pbar) pbar.style.display = 'none';
  if (fbar) fbar.style.display = '';
  const nameInput = document.getElementById('ve-fn-bar-name');
  if (nameInput) nameInput.value = funcName;

  initLayout();
  veRender();
}

// saveFunctionEditor regenerates the function's _sourceText from the edited
// canvas nodes, updates call sites, and exits editing mode.
function saveFunctionEditor() {
  if (!ve.fnEditor.active) return;
  const fd = ve.userFunctions[ve.fnEditor.funcName];
  if (!fd) { exitFunctionEditor(); return; }

  const g = ve.graphs[0];
  const mainNodes = g.nodes.filter(n => !n.isSearchNode && !n.isListNode && !n.isUpstreamPseudo);
  const selectedIds = new Set(mainNodes.map(n => n.id));

  // Identify the terminal (return) node — the one not consumed by any other
  // internal node as an upstream.
  const usedInternally = new Set(
    mainNodes.flatMap(n => (n.upstreams || []).filter(u => selectedIds.has(u)))
  );
  const terminals = mainNodes.filter(n => !usedInternally.has(n.id));

  if (mainNodes.length === 0) {
    alert('The function body is empty. Add at least one node.');
    return;
  }
  if (terminals.length > 1) {
    alert('The function has multiple output nodes. Connect them into a single chain first.');
    return;
  }

  const returnNodeId      = terminals[0]?.id ?? mainNodes[mainNodes.length - 1].id;
  const hasUpstreamPseudo = g.nodes.some(n => n.isUpstreamPseudo);
  // entryUpstreams just needs to be non-empty to tell nodesToFunctionSource to
  // add 'upstream' to the signature; the actual IDs don't matter here.
  const entryUpstreams = hasUpstreamPseudo ? ['__entry__'] : [];
  const validation     = {entryUpstreams, returnNodeId};

  // Build the params array for nodesToFunctionSource by mapping each param to
  // the node/configKey it references via _paramRefs.
  const currentParams = ve.fnEditor.paramsSnapshot;
  const fnParams = [];
  for (const p of currentParams) {
    let mapped = false;
    for (const n of mainNodes) {
      for (const [configKey, paramName] of Object.entries(n._paramRefs || {})) {
        if (paramName === p.key) {
          fnParams.push({nodeId: n.id, configKey, paramName: p.key,
            type: p.type, defaultValue: p.default, include: true, hint: p.hint || ''});
          mapped = true;
          break;
        }
      }
      if (mapped) break;
    }
    if (!mapped) {
      // Param not referenced in any current node — include in signature only.
      fnParams.push({nodeId: null, configKey: null, paramName: p.key,
        type: p.type, defaultValue: p.default, include: true, hint: p.hint || ''});
    }
  }

  const newSourceText = nodesToFunctionSource(ve.fnEditor.funcName, fnParams, selectedIds, validation, g);

  fd._sourceText = newSourceText;
  fd.params      = currentParams;
  fd.role        = newSourceText.includes(' = input(') ? 'source'
                 : newSourceText.includes(' = output(') ? 'sink' : 'processor';

  // Propagate param additions/removals to all call sites.
  fnSyncCallSiteParams(ve.fnEditor.funcName, ve.fnEditor.paramsSnapshot, currentParams);

  exitFunctionEditor();
  renderPalette(document.getElementById('ve-search')?.value ?? '');
  onModelChange();
}

// exitFunctionEditor restores the pipeline canvas and hides the function bar.
function exitFunctionEditor() {
  if (!ve.fnEditor.active) return;
  ve.graphs        = ve.fnEditor.savedGraphs;
  ve.activeGraph   = ve.fnEditor.savedActive;
  ve.nextId        = Math.max(ve.fnEditor.savedNextId, ve.nextId);
  ve.selectedNodeId = ve.fnEditor.savedSelected;
  ve.fnEditor      = {active: false};

  clearMultiSelect();
  const pbar = document.getElementById('ve-pipeline-bar');
  const fbar = document.getElementById('ve-fn-bar');
  const ppar = document.getElementById('ve-fn-params-panel');
  if (pbar) pbar.style.display = '';
  if (fbar) fbar.style.display = 'none';
  if (ppar) ppar.style.display = 'none';

  ve_panX = 0; ve_panY = 0;
  initLayout();
  veRender();
  // Flush the correct pipeline content back to the text editor now that
  // ve.fnEditor.active is false and dagToStarlark() sees the real graphs.
  onModelChange();
}

// fnEditorCommitRename is called when the function name input loses focus.
// It renames the function and updates all call sites.
function fnEditorCommitRename(newName) {
  if (!ve.fnEditor.active) return;
  const oldName = ve.fnEditor.funcName;
  if (!newName || newName === oldName) return;
  if (!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(newName)) {
    alert('Function name must be a valid identifier.');
    const inp = document.getElementById('ve-fn-bar-name');
    if (inp) inp.value = oldName;
    return;
  }
  if (ve.userFunctions[newName] && newName !== oldName) {
    alert(`A function named "${newName}" already exists.`);
    const inp = document.getElementById('ve-fn-bar-name');
    if (inp) inp.value = oldName;
    return;
  }

  // Update the function definition.
  const fd = ve.userFunctions[oldName];
  fd.name         = newName;
  fd._sourceText  = (fd._sourceText || '').replace(
    new RegExp(`\\bdef ${escapeRegExp(oldName)}\\b`), `def ${newName}`);
  ve.userFunctions[newName] = fd;
  delete ve.userFunctions[oldName];

  // Update all call nodes in the saved pipeline graphs.
  for (const g of (ve.fnEditor.savedGraphs || [])) {
    for (const n of g.nodes) {
      if ((n.isFunctionCall || ve.userFunctions[n.plugin]) && n.plugin === oldName) {
        n.plugin = newName;
        if (n.funcCallKey) n.funcCallKey = n.funcCallKey; // ID stays the same
      }
    }
  }

  // Update the graph name shown in the canvas.
  if (ve.graphs[0]) ve.graphs[0].name = newName;
  ve.fnEditor.funcName = newName;
}

function escapeRegExp(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// fnSyncCallSiteParams propagates param additions/removals to all call sites.
function fnSyncCallSiteParams(funcName, oldParams, newParams) {
  const oldKeys = new Set(oldParams.map(p => p.key));
  const newKeys = new Set(newParams.map(p => p.key));
  const added   = newParams.filter(p => !oldKeys.has(p.key));
  const removed = oldParams.filter(p => !newKeys.has(p.key));

  for (const g of (ve.fnEditor.savedGraphs || [])) {
    for (const n of g.nodes) {
      if (n.plugin !== funcName) continue;
      for (const p of added) {
        if (!(p.key in (n.config || {}))) {
          n.config = n.config || {};
          n.config[p.key] = p.default != null ? p.default : emptyForType(p.type);
        }
      }
      for (const p of removed) {
        delete (n.config || {})[p.key];
      }
    }
  }
}

// ── function editor parameter management ──────────────────────────────────────

function fnEditorToggleParams() {
  if (!ve.fnEditor.active) return;
  ve.fnEditor.paramsOpen = !ve.fnEditor.paramsOpen;
  const panel = document.getElementById('ve-fn-params-panel');
  const btn   = document.getElementById('ve-fn-bar-params-btn');
  if (panel) panel.style.display = ve.fnEditor.paramsOpen ? '' : 'none';
  if (btn)   btn.classList.toggle('active', ve.fnEditor.paramsOpen);
  if (ve.fnEditor.paramsOpen) renderFnEditorParams();
}

function renderFnEditorParams() {
  const body = document.getElementById('ve-fn-params-body');
  if (!body) return;
  const params = ve.fnEditor.paramsSnapshot || [];
  body.innerHTML = params.map((p, i) => `
    <div class="ve-fn-param-row" data-pi="${i}">
      <input class="ve-fn-param-name" value="${esc(p.key)}" placeholder="param name"
        onblur="fnEditorRenameParam(${i}, this.value)"
        onkeydown="if(event.key==='Enter')this.blur()">
      <select class="ve-fn-param-type" onchange="fnEditorChangeType(${i}, this.value)">
        ${['string','int','bool','list','duration','enum'].map(t =>
          `<option value="${t}"${p.type===t?' selected':''}>${t}</option>`).join('')}
      </select>
      <input class="ve-fn-param-hint" value="${esc(p.hint || '')}" placeholder="hint…"
        oninput="fnEditorUpdateHint(${i}, this.value)">
      <button class="ve-fn-param-remove" onclick="fnEditorRemoveParam(${i})" title="Remove parameter">✕</button>
    </div>`).join('');
}

function fnEditorAddParam() {
  if (!ve.fnEditor.active) return;
  const params = ve.fnEditor.paramsSnapshot;
  let i = params.length + 1;
  let key = `param${i}`;
  while (params.some(p => p.key === key)) key = `param${++i}`;
  params.push({key, type: 'string', required: true, default: null, hint: ''});
  renderFnEditorParams();
}

function fnEditorRemoveParam(idx) {
  if (!ve.fnEditor.active) return;
  ve.fnEditor.paramsSnapshot.splice(idx, 1);
  renderFnEditorParams();
}

function fnEditorRenameParam(idx, newKey) {
  if (!ve.fnEditor.active) return;
  const params = ve.fnEditor.paramsSnapshot;
  const p = params[idx];
  if (!p || !newKey || newKey === p.key) return;
  if (params.some((q, i) => i !== idx && q.key === newKey)) {
    alert(`Parameter "${newKey}" already exists.`);
    renderFnEditorParams();
    return;
  }
  // Update _paramRefs in canvas nodes.
  for (const n of (ve.graphs[0]?.nodes || [])) {
    if (n._paramRefs?.[p.key] === p.key) {
      n._paramRefs[p.key] = newKey; // wait — the key in _paramRefs is the configKey
    }
    // _paramRefs maps configKey → paramName; rename the paramName references.
    if (n._paramRefs) {
      for (const [cfgKey, pName] of Object.entries(n._paramRefs)) {
        if (pName === p.key) n._paramRefs[cfgKey] = newKey;
      }
    }
  }
  p.key = newKey;
  renderFnEditorParams();
}

function fnEditorChangeType(idx, type) {
  if (!ve.fnEditor.active) return;
  const p = ve.fnEditor.paramsSnapshot[idx];
  if (p) p.type = type;
}

function fnEditorUpdateHint(idx, hint) {
  if (!ve.fnEditor.active) return;
  const p = ve.fnEditor.paramsSnapshot[idx];
  if (p) p.hint = hint;
}

// fnEditorPromoteToParam converts a hardcoded config field on a function body
// node into a function parameter reference.  A new param is created (or reused
// if the field key already exists as a param) and the field is marked as a ref.
function fnEditorPromoteToParam(nodeId, configKey, fieldType) {
  if (!ve.fnEditor.active) return;
  const node = findNode(nodeId);
  if (!node) return;

  const params = ve.fnEditor.paramsSnapshot;

  // Suggest a param name: use the config key if not already taken, otherwise
  // append a number suffix to make it unique.
  let paramName = configKey;
  let i = 2;
  while (params.some(p => p.key === paramName)) paramName = `${configKey}${i++}`;

  // Prompt the user to confirm or customise the param name.
  const entered = window.prompt(`New parameter name for "${configKey}":`, paramName);
  if (!entered) return;
  paramName = entered.trim();
  if (!paramName || !/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(paramName)) {
    alert('Parameter name must be a valid identifier.');
    return;
  }
  if (params.some(p => p.key === paramName)) {
    // Reuse the existing param: just link the field to it.
    if (!node._paramRefs) node._paramRefs = {};
    node._paramRefs[configKey] = paramName;
    node.config[configKey] = emptyForType(fieldType);
    renderParamPanel();
    return;
  }

  // Create the new param.
  params.push({key: paramName, type: fieldType, required: true, default: null, hint: ''});

  // Mark the field as a param reference on this node.
  if (!node._paramRefs) node._paramRefs = {};
  node._paramRefs[configKey] = paramName;
  // Seed the config with an empty value (the real value comes from the call site).
  node.config[configKey] = emptyForType(fieldType);

  renderParamPanel();
  if (ve.fnEditor.paramsOpen) renderFnEditorParams();
}

// fnEditorUnlinkParamRef converts a param reference back to a literal value,
// leaving the current (empty) config value in place for the user to fill in.
function fnEditorUnlinkParamRef(nodeId, configKey) {
  if (!ve.fnEditor.active) return;
  const node = findNode(nodeId);
  if (!node?._paramRefs) return;

  const paramName = node._paramRefs[configKey];
  delete node._paramRefs[configKey];

  // Remove the param entirely if no other node still references it.
  const stillUsed = ve.graphs[0]?.nodes.some(n =>
    n._paramRefs && Object.values(n._paramRefs).includes(paramName)
  );
  if (!stillUsed) {
    ve.fnEditor.paramsSnapshot = ve.fnEditor.paramsSnapshot.filter(p => p.key !== paramName);
    if (ve.fnEditor.paramsOpen) renderFnEditorParams();
  }

  renderParamPanel();
}

// emptyForType returns a sensible empty/zero value for a given FieldType string.
function emptyForType(type) {
  if (type === 'list')     return [];
  if (type === 'int')      return 0;
  if (type === 'bool')     return false;
  return '';
}

function dagToStarlark() {
  const graphs = ve.graphs.filter(g => g.name || g.nodes.length);
  if (!graphs.length) return '';

  // Collect user functions that are actually used across all pipelines.
  // Also catch nodes whose plugin name is a user function but isFunctionCall
  // was not set (e.g. nodes created before the flag was introduced, or loaded
  // from a config saved in a broken state).
  const usedFunctions = new Set();
  for (const g of graphs) {
    for (const n of g.nodes) {
      if (ve.userFunctions[n.plugin]) usedFunctions.add(n.plugin);
    }
  }

  // Emit function definitions at the top (preserved from the parsed source).
  // We re-emit the function definitions from their stored body source if available,
  // otherwise from the internal node model (Phase 3 — editing not yet implemented).
  const preamble = [];
  for (const funcName of usedFunctions) {
    const fd = ve.userFunctions[funcName];
    if (!fd) continue;
    if (fd._sourceText) {
      // Preserve the original def block verbatim.
      preamble.push(fd._sourceText.trimEnd());
    }
  }

  const sections = [];
  for (const g of graphs) {
    const lines = [];
    // Sort so every upstream variable is assigned before it is referenced.
    const ordered = topoSortNodes(g.nodes.filter(n => !n.isSearchNode && !n.isListNode));
    for (const n of ordered) {
      const meta     = pluginMeta(n.plugin);
      const role     = meta?.role || 'processor';
      const cfgKw    = configToKwargs(n.config);
      const fromStr  = upstreamsStr(n.upstreams);

      // Emit user comment then pipeliner:pos before the definition.
      const hasPos     = !n.isSearchNode && !n.isListNode && n.x != null && n.y != null;
      const hasComment = !!n.comment?.trim();
      if (hasPos || hasComment) {
        if (lines.length > 0) lines.push('');
      }
      if (hasComment) {
        for (const cl of n.comment.trim().split('\n')) lines.push(`# ${cl}`);
      }
      if (hasPos) {
        const regionY = g._regionY ?? 0;
        let posLine = `# pipeliner:pos ${Math.round(n.x)} ${Math.round(n.y - regionY)}`;
        const subCoords = (ids) => ids
          .map(id => g.nodes.find(x => x.id === id))
          .filter(Boolean)
          .map(sn => `${Math.round(sn.x ?? 0)} ${Math.round((sn.y ?? 0) - regionY)}`)
          .join(' ');
        if (n.listNodeIds?.length)   posLine += ` list ${subCoords(n.listNodeIds)}`;
        if (n.searchNodeIds?.length) posLine += ` search ${subCoords(n.searchNodeIds)}`;
        lines.push(posLine);
      }

      if (n.isFunctionCall || ve.userFunctions[n.plugin]) {
        // Serialize as a user function call: varname = funcname(upstream=..., kwargs).
        // The second condition handles nodes whose plugin is a user function but
        // isFunctionCall was not set (e.g. loaded from a previously broken config).
        //
        // Self-healing: if required params are missing from n.config (e.g. the
        // config was saved before args were filled in), seed them with empty
        // defaults so the call is at least syntactically valid.
        const fd = ve.userFunctions[n.plugin];
        if (fd?.params?.length) {
          for (const p of fd.params) {
            if (!(p.key in (n.config || {}))) {
              n.config = n.config || {};
              n.config[p.key] = p.default != null ? p.default : emptyForType(p.type);
            }
          }
        }
        const healedKw = configToKwargs(n.config);
        const parts = [];
        if (fromStr)   parts.push(`upstream=${fromStr}`);
        if (healedKw)  parts.push(healedKw);
        lines.push(`${n.id} = ${n.plugin}(${parts.join(', ')})`);
      } else if (role === 'source') {
        lines.push(`${n.id} = input(${[starLit(n.plugin), cfgKw].filter(Boolean).join(', ')})`);
      } else if (role === 'processor') {
        const parts = [starLit(n.plugin)];
        if (fromStr) parts.push(`upstream=${fromStr}`);
        if (n.searchNodeIds?.length) {
          const searchItems = n.searchNodeIds.map(id => g.nodes.find(x => x.id === id)).filter(Boolean).map(viaNodeToStar).join(', ');
          parts.push(`search=[${searchItems}]`);
        }
        if (n.listNodeIds?.length) {
          const listItems = n.listNodeIds.map(id => g.nodes.find(x => x.id === id)).filter(Boolean).map(viaNodeToStar).join(', ');
          parts.push(`list=[${listItems}]`);
        }
        if (cfgKw) parts.push(cfgKw);
        lines.push(`${n.id} = process(${parts.join(', ')})`);
      } else {
        const parts = [starLit(n.plugin)];
        if (fromStr) parts.push(`upstream=${fromStr}`);
        if (cfgKw)   parts.push(cfgKw);
        lines.push(`${n.id} = output(${parts.join(', ')})`);
      }
    }

    // Pipeline footer: optional user comment then pipeline() call.
    if (lines.length > 0) lines.push('');
    if (g.comment?.trim()) {
      for (const cl of g.comment.trim().split('\n')) lines.push(`# ${cl}`);
    }

    const schedArg = g.schedule ? `, schedule=${starLit(g.schedule)}` : '';
    lines.push(`pipeline(${starLit(g.name)}${schedArg})`);
    sections.push(lines.join('\n'));
  }

  const body = sections.join('\n\n') + '\n';
  return preamble.length ? preamble.join('\n\n') + '\n\n' + body : body;
}

// Serialise a via-connected node as a Starlark dict: {"name": "jackett", "url": "..."}.
function viaNodeToStar(node) {
  const entries = [`${starLit('name')}: ${starLit(node.plugin)}`];
  for (const [k, val] of Object.entries(node.config || {})) {
    if (val !== '' && val != null) entries.push(`${starLit(k)}: ${valToStar(val)}`);
  }
  return `{${entries.join(', ')}}`;
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
  // While editing a function body, ve.graphs contains only the function's
  // internal nodes — serialising it would overwrite the pipeline content in
  // the text editor.  Changes are flushed when the editor is saved/closed.
  if (ve.fnEditor.active) return;
  const el = document.getElementById('config-editor');
  if (!el) return;
  el.value = dagToStarlark();
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
    const data    = await r.json();
    const entries = Object.entries(data.graphs || {});

    // Merge user functions into the plugin registry so the palette can show them.
    // Also extract each function's source text from the config content so
    // dagToStarlark can re-emit function definitions verbatim.
    ve.userFunctions = {};
    for (const fd of (data.functions || [])) {
      ve.userFunctions[fd.name] = fd;
      fd._sourceText = extractFunctionSource(content, fd.name);
    }

    ve.syncing = true;
    if (!entries.length) {
      ve.graphs         = [];       // no stub — user must click "+ Add pipeline"
      ve.selectedNodeId = null;
      ve.activeGraph    = 0;
      ve.syncing = false;
      veRender();
      setSyncNote('No DAG pipelines found — click "+ Add pipeline" to create one');
      return;
    }

    ve.graphs = entries.map(([name, graph]) => {
      const rawNodes = graph.nodes || [];

      // Build a set of node IDs that belong to a function call (internal nodes).
      // These are hidden from the main canvas in collapsed mode.
      const internalNodeIds = new Set();
      for (const fc of (graph.function_calls || [])) {
        for (const nid of fc.internal_node_ids) internalNodeIds.add(nid);
      }

      // First pass: build regular (non-internal) nodes.
      // Positions (x, y) come from per-node # pipeliner:pos comments on the
      // server; y is relative to the pipeline's region top and is converted to
      // absolute by initLayout() after stacking order is determined.
      const nodes = rawNodes
        .filter(n => !internalNodeIds.has(n.id))
        .map(n => ({
          id: n.id, plugin: n.plugin, config: n.config || {}, upstreams: n.upstreams || [],
          searchNodeIds: [], comment: n.comment || '',
          x: n.x ?? null, y: n.y ?? null,
        }));

      // Second pass: convert search/list items to regular nodes with flags.
      for (let ni = 0; ni < rawNodes.length; ni++) {
        const raw = rawNodes[ni];
        if (internalNodeIds.has(raw.id)) continue;
        const nodeIdx = nodes.findIndex(n => n.id === raw.id);
        if (nodeIdx < 0) continue;
        for (let si = 0; si < (raw.search || []).length; si++) {
          const s  = raw.search[si];
          const id = `${raw.id}__search__${si}`;
          nodes[nodeIdx].searchNodeIds.push(id);
          nodes.push({
            id, plugin: s.plugin, config: s.config || {},
            upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
            isSearchNode: true, searchParentId: raw.id,
            x: s.x ?? null, y: s.y ?? null,
          });
        }
        for (let li = 0; li < (raw.list || []).length; li++) {
          const l  = raw.list[li];
          const id = `${raw.id}__list__${li}`;
          if (!nodes[nodeIdx].listNodeIds) nodes[nodeIdx].listNodeIds = [];
          nodes[nodeIdx].listNodeIds.push(id);
          nodes.push({
            id, plugin: l.plugin, config: l.config || {},
            upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
            isListNode: true, listParentId: raw.id,
            x: l.x ?? null, y: l.y ?? null,
          });
        }
      }

      // Third pass: insert synthetic function-call nodes.
      // Each call is represented as a single collapsed card whose upstreams are
      // the external upstreams of the call's entry node(s) and whose position
      // is the stored call-key position (if any).
      for (const fc of (graph.function_calls || [])) {
        // Find the entry nodes: internal nodes whose upstreams are all external.
        const internalSet = new Set(fc.internal_node_ids);
        const entryUpstreams = [];
        for (const n of rawNodes) {
          if (!internalSet.has(n.id)) continue;
          for (const up of (n.upstreams || [])) {
            if (!internalSet.has(up)) entryUpstreams.push(up);
          }
        }
        // Find downstream nodes of the return node (nodes outside the function
        // that list the return node as an upstream).
        // The synthetic node replaces the return node in their upstream lists.
        for (const n of nodes) {
          if (n.isFunctionCall) continue;
          n.upstreams = n.upstreams.map(u => u === fc.return_node_id ? fc.call_key : u);
        }
        nodes.push({
          id:               fc.call_key,
          plugin:           fc.func,
          config:           fc.args || {},
          upstreams:        entryUpstreams,
          searchNodeIds:    [],
          listNodeIds:      [],
          comment:          '',
          isFunctionCall:   true,
          funcCallKey:      fc.call_key,
          internalNodeIds:  fc.internal_node_ids,
          returnNodeId:     fc.return_node_id,
          x:                fc.x ?? null,
          y:                fc.y ?? null,
        });
      }

      // A pipeline "has layout" when any main node carries a stored position.
      const _hasLayout = nodes.some(n => !n.isSearchNode && !n.isListNode && n.x != null && n.y != null);
      return {name, schedule: graph.schedule || '', comment: graph.comment || '', nodes, _hasLayout};
    });
    ve.nextId = ve.graphs.flatMap(g => g.nodes).reduce((max, n) => {
      const m = n.id.match(/_(\d+)$/);
      return m ? Math.max(max, parseInt(m[1]) + 1) : max;
    }, 0);
    ve.activeGraph    = 0;
    ve.selectedNodeId = null;
    ve_panX = 0; ve_panY = 0; // reset pan so freshly laid-out nodes are visible
    ve.syncing = false;
    // Place all pipelines in order: stored relative positions for those that
    // have a layout comment, auto-layout for those that don't.
    initLayout();
    veRender();
    setSyncNote(entries.length > 1 ? `Showing ${entries.length} pipelines` : '');
    // Write computed positions back so they survive the next round-trip.
    // Skip when there are no pipelines (would overwrite a non-DAG config).
    if (ve.graphs.length > 0) onModelChange();
  } catch (e) {
    ve.syncing = false;
    setSyncNote('✗ ' + String(e));
  }
}

function setSyncNote(msg) {
  const el = document.getElementById('ve-sync-note');
  if (el) el.textContent = msg;
}

// ── text pop-up editor ────────────────────────────────────────────────────────
// Generic multi-line text popup reusable for comments, email body, etc.

function openTextPopup(title, placeholder, initialValue, onSave) {
  let modal = document.getElementById('ve-text-popup');
  if (!modal) {
    modal = document.createElement('div');
    modal.id = 've-text-popup';
    modal.className = 've-text-popup';
    modal.innerHTML =
      '<div class="ve-text-popup-inner">' +
        '<div class="ve-text-popup-header">' +
          '<span class="ve-text-popup-title"></span>' +
          '<button class="ve-text-popup-close" title="Close">×</button>' +
        '</div>' +
        '<textarea class="ve-text-popup-ta"></textarea>' +
        '<div class="ve-text-popup-footer">' +
          '<button class="ve-text-popup-cancel">Cancel</button>' +
          '<button class="ve-text-popup-save">Save</button>' +
        '</div>' +
      '</div>';
    document.body.appendChild(modal);
  }

  modal.querySelector('.ve-text-popup-title').textContent = title;
  const ta = modal.querySelector('.ve-text-popup-ta');
  ta.placeholder = placeholder ?? '';
  ta.value       = initialValue ?? '';

  const close = () => { modal.style.display = 'none'; };
  modal.querySelector('.ve-text-popup-close').onclick  = close;
  modal.querySelector('.ve-text-popup-cancel').onclick = close;
  modal.querySelector('.ve-text-popup-save').onclick   = () => { onSave(ta.value); close(); };
  modal.onclick = e => { if (e.target === modal) close(); };
  modal.onkeydown = e => { if (e.key === 'Escape') close(); };

  modal.style.display = 'flex';
  ta.focus();
  ta.setSelectionRange(ta.value.length, ta.value.length);
}

// ── pipeline bounds from stored positions ─────────────────────────────────────
// Used when node positions are loaded from a pipeliner:layout comment so we
// can compute label/region bounds without running autoLayout.

function computePipelineBoundsFromNodes() {
  // Sort graphs by their topmost node Y so labels stack top-to-bottom.
  let prevBottom = 40;
  for (const g of ve.graphs) {
    const nonVia = g.nodes.filter(n => !n.isSearchNode && !n.isListNode);
    if (!nonVia.length) {
      g._labelY  = prevBottom + 10;
      g._regionY = g._labelY - 8;
      g._regionH = 80;
      prevBottom = g._regionY + g._regionH + 60;
      continue;
    }
    const minY = Math.min(...nonVia.map(n => n.y ?? 0));
    const maxY = Math.max(...g.nodes.map(n => (n.y ?? 0) + NODE_H + 80));
    g._labelY  = Math.max(prevBottom + 10, minY - 30);
    g._regionY = g._labelY - 8;
    g._regionH = maxY - g._regionY + 20;
    prevBottom = g._regionY + g._regionH + 60;
  }
}

// ── utility ───────────────────────────────────────────────────────────────────

function esc(s) {
  return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
