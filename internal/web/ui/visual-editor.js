// ── DAG visual pipeline editor ────────────────────────────────────────────────
//
// Supports only DAG-style pipelines (input / process / merge / output / pipeline).
// Serialises to and from Starlark DAG syntax and keeps the text editor in sync.

const NODE_W = 200;  // node card width (px)
const NODE_H = 80;   // fallback node height for edge midpoints

// nodeCardHeight returns the actual rendered height of a node card by reading
// its DOM offsetHeight.  Falls back to NODE_H when the element doesn't exist yet
// (e.g. before the first renderCanvas call).  Used for region-height computation
// so that warning badges (which make the card taller) don't overflow the pipeline box.
function nodeCardHeight(nd) {
  const div = document.querySelector(`.ve-node[data-id="${CSS.escape(nd.id)}"]`);
  return (div && div.offsetHeight > 0) ? div.offsetHeight : NODE_H;
}

const ve = {
  plugins:          [],   // [{name, role, description, schema, produces, requires}]
  fieldRegistry:    [],   // [{name, type, description, set_by, known_values}] from /api/fields
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
// True when the text editor has been edited by the user since the last
// textToVisualSync. Used to skip the re-sync (and viewport reset) when the
// user switches text → visual without typing anything. Starts true so the
// initial load triggers a sync.
let ve_textDirty     = true;
// True while two-finger pinch/pan is active. Single-finger handlers (rubber-
// band, node drag) read this and abort cleanly so a second finger landing on
// the canvas takes over without leaving a half-finished marquee behind.
let ve_pinching      = false;

// ── undo ─────────────────────────────────────────────────────────────────────

const VE_MAX_UNDO = 50;
let ve_undoStack = [];

// pushUndo snapshots the current model state. Call before any destructive action.
function pushUndo() {
  veDebugLog('pushUndo', snapshotGraphPositions(ve.graphs));
  ve_undoStack.push(JSON.stringify({
    graphs:        ve.graphs,
    userFunctions: ve.userFunctions,
    nextId:        ve.nextId,
  }));
  if (ve_undoStack.length > VE_MAX_UNDO) ve_undoStack.shift();
  updateUndoButton();
}

function undo() {
  if (!ve_undoStack.length) return;
  const snap = JSON.parse(ve_undoStack.pop());
  ve.graphs        = snap.graphs;
  ve.userFunctions = snap.userFunctions;
  ve.nextId        = snap.nextId;
  ve.selectedNodeId = null;
  clearMultiSelect();
  initLayoutFromAbsolute('undo');
  veRender();
  onModelChange();
  updateUndoButton();
  setSyncNote('↩ Undone');
  setTimeout(() => setSyncNote(''), 1500);
}

// initLayoutFromAbsolute runs initLayout on graphs whose nodes already hold
// ABSOLUTE y values (an undo snapshot, fnEditor.savedGraphs, or anything
// captured live). initLayout()'s stored-position branch expects RELATIVE y
// and adds _regionY to convert; passing absolute coords directly would
// re-add _regionY and drift every node down by it (cumulatively worse for
// pipelines further down the canvas). This helper subtracts the saved
// _regionY first so the round-trip is a no-op. The optional tag namespaces
// the veDebug logs so drift bugs can be traced back to the trigger site.
function initLayoutFromAbsolute(tag) {
  for (const g of ve.graphs) {
    const regionY = g._regionY ?? 0;
    if (regionY === 0) continue;
    for (const n of g.nodes) {
      if (n.y != null) n.y -= regionY;
    }
  }
  if (tag) veDebugLog(`${tag}:beforeInitLayout`, snapshotGraphPositions(ve.graphs));
  initLayout();
  if (tag) veDebugLog(`${tag}:afterInitLayout`, snapshotGraphPositions(ve.graphs));
}

function updateUndoButton() {
  const btn = document.getElementById('ve-undo-btn');
  if (btn) btn.disabled = ve_undoStack.length === 0;
}

// veDebugLog is a no-op in production. To enable, set localStorage.veDebug='1'
// in the browser devtools and reload. It logs node y/regionY transitions at
// the spots that mutate layout (initLayout, undo, drag end, tightenPipeline)
// so positional drift can be diagnosed when it next happens.
function veDebugLog(tag, payload) {
  if (typeof window === 'undefined') return;
  try {
    if (window.localStorage?.getItem('veDebug') !== '1') return;
  } catch { return; }
  // eslint-disable-next-line no-console
  console.log(`[veDebug:${tag}]`, payload);
}

// snapshotGraphPositions returns a compact summary of every main node's y and
// each graph's region metadata — the minimum needed to diagnose layout drift.
// Cheap enough to call from log sites; only allocates when veDebug is on.
function snapshotGraphPositions(graphs) {
  return graphs.map((g, gi) => ({
    gi,
    labelY:  g._labelY  ?? null,
    regionY: g._regionY ?? null,
    regionH: g._regionH ?? null,
    nodes:   (g.nodes || [])
      .filter(n => !n.isSearchNode && !n.isListNode && !n.isRoutePort)
      .map(n => ({id: n.id, x: n.x, y: n.y})),
  }));
}

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

// ── shared edge geometry helpers ─────────────────────────────────────────────

// edgePath returns a cubic bezier SVG path string between two points.
// exitDir  — direction the curve leaves the source port:
//   'right' (default) left-to-right side port
//   'down'  bottom port (route ports, search-port, list-node)
//   'up'    top port (list cursor dragging upward)
// entryDir — direction the curve arrives at the target port:
//   'left'  (default) left-side input
//   'top'   top input (search/list target ports)
//   'bottom' bottom (list cursor target)
// Shorthand aliases: 'h' = right+left, 'v' = down+top, 'v-up' = up+bottom
function edgePath(x1, y1, x2, y2, exitDir = 'right', entryDir) {
  // Resolve shorthand aliases.
  if (exitDir === 'h')    { exitDir = 'right'; entryDir = entryDir ?? 'left'; }
  else if (exitDir === 'v')    { exitDir = 'down';  entryDir = entryDir ?? 'top';  }
  else if (exitDir === 'v-up') { exitDir = 'up';    entryDir = entryDir ?? 'bottom'; }
  else { entryDir = entryDir ?? 'left'; }

  const dx = Math.max(60, Math.abs(x2 - x1) * 0.5);
  const dy = Math.max(50, Math.abs(y2 - y1) * 0.5);

  let cp1x, cp1y;
  if (exitDir === 'right') { cp1x = x1 + dx; cp1y = y1; }
  else if (exitDir === 'down') { cp1x = x1; cp1y = y1 + dy; }
  else if (exitDir === 'up')   { cp1x = x1; cp1y = y1 - Math.max(40, Math.abs(y1 - y2) * 0.4); }
  else                         { cp1x = x1 - dx; cp1y = y1; } // 'left'

  let cp2x, cp2y;
  if (entryDir === 'left')   { cp2x = x2 - dx; cp2y = y2; }
  else if (entryDir === 'top')    { cp2x = x2; cp2y = y2 - dy; }
  else if (entryDir === 'bottom') { cp2x = x2; cp2y = y2 + Math.max(40, Math.abs(y1 - y2) * 0.4); }
  else                            { cp2x = x2 + dx; cp2y = y2; } // 'right'

  return `M${x1},${y1} C${cp1x},${cp1y} ${cp2x},${cp2y} ${x2},${y2}`;
}

// toCanvasCoords converts a pointer event's client coordinates to canvas-space.
function toCanvasCoords(e) {
  const rect = document.getElementById('ve-graph-canvas')
                      ?.getBoundingClientRect() ?? {left: 0, top: 0};
  return {
    x: (e.clientX - rect.left) / ve_zoom,
    y: (e.clientY - rect.top)  / ve_zoom,
  };
}

// startDragFlow wires the shared pointermove/pointerup/pointercancel boilerplate
// for all drag-connect interactions.  The caller provides:
//   stateSet(curX, curY)  — called on every move to update state
//   onRelease(ev)         — called on pointerup to commit or cancel
//   canvasCls             — CSS class added to the canvas while dragging
function startDragFlow(e, stateSet, onRelease, canvasCls) {
  const canvas = document.getElementById('ve-graph-canvas');
  const {x, y} = toCanvasCoords(e);
  stateSet(x, y);
  canvas?.classList.add(canvasCls);

  function onMove(ev) {
    if (ve_pinching) return; // second finger took over — freeze the live edge
    const {x: cx, y: cy} = toCanvasCoords(ev);
    stateSet(cx, cy);
    renderEdges();
  }
  function cleanup() {
    document.removeEventListener('pointermove', onMove);
    canvas?.classList.remove(canvasCls);
  }
  function onUp(ev) { cleanup(); onRelease(ev); }
  function onCancel() { cleanup(); onRelease(null); }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp,    {once: true});
  document.addEventListener('pointercancel', onCancel, {once: true});
}

// Disconnect a search-connected node from its parent discover node.
function disconnectSearch(discoverNodeId, searchNodeId) {
  pushUndo();
  const disc = findNode(discoverNodeId);
  const sn   = findNode(searchNodeId);
  if (disc) disc.searchNodeIds = (disc.searchNodeIds || []).filter(id => id !== searchNodeId);
  if (sn)  { sn.isSearchNode = false; delete sn.searchParentId; }
  veRender();
  onModelChange();
}

function disconnectList(parentNodeId, listNodeId) {
  pushUndo();
  const parent = findNode(parentNodeId);
  const ln     = findNode(listNodeId);
  if (parent) parent.listNodeIds = (parent.listNodeIds || []).filter(id => id !== listNodeId);
  if (ln)  { ln.isListNode = false; delete ln.listParentId; }
  veRender();
  onModelChange();
}

// ── view switching ─────────────────────────────────────────────────────────────

let currentView = 'visual'; // open in visual mode by default

function switchView(view) {
  // Don't switch views while the function body editor is active.
  if (ve.fnEditor?.active) return;
  if (view === 'text') {
    if (view === currentView) return;
    doSwitchView('text');
  } else {
    // Visual: re-sync from the text editor only if the user has actually
    // edited it since the last sync. Skipping the sync preserves the current
    // zoom/pan — textToVisualSync resets the viewport unconditionally.
    const load = ve.plugins.length ? Promise.resolve() : loadPalette();
    load.then(() => {
      doSwitchView('visual');
      if (!ve_canvasInited) {
        initCanvasEvents();
        veInitPaletteState();  // restore collapsed/expanded state from localStorage
        veInitPanelDrag();     // wire up the floating param panel drag handle
        ve_canvasInited = true;
      }
      if (ve_textDirty) textToVisualSync();
    });
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
  if (view === 'text')   fitTextEditor();
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
    const [pr, fr] = await Promise.all([fetch('/api/plugins'), fetch('/api/fields')]);
    if (pr.ok) ve.plugins = await pr.json();
    if (fr.ok) ve.fieldRegistry = await fr.json();
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
        chipHtml.push(`<button class="ve-chip" data-role="${role}" disabled data-tip="${esc(p.description)}">${esc(p.name)}</button>`);
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
    html.push(`<div class="ve-role-header" data-role="function" onclick="toggleRoleGroup(this)">Functions</div>`);
    html.push(`<div class="ve-role-chips">`);
    for (const fd of userFuncList) {
      // A plugin can be both search and list (e.g. bluray_releases) — render
      // both badges (and classes), not just one.
      const fnBadge   = (fd.is_search_plugin ? ' <span class="ve-chip-search-badge">search</span>' : '')
                      + (fd.is_list_plugin   ? ' <span class="ve-chip-list-badge">list</span>'     : '');
      const fnCls     = (fd.is_search_plugin ? ' ve-chip-search' : '')
                      + (fd.is_list_plugin   ? ' ve-chip-list'   : '');
      html.push(`<span class="ve-chip-fn-wrap" data-role="${fd.role}">
        <button class="ve-chip ve-chip-fn${fnCls}" data-role="${fd.role}"
          data-plugin="${esc(fd.name)}"
          data-tip="${esc(fd.comment || fd.description || fd.name)}">
          ${esc(fd.name)}${fnBadge}</button
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
      // A plugin can be both search and list (e.g. bluray_releases) — render
      // both badges, both hover-accent classes, and both tooltip lines.
      const capBadges = (p.is_search_plugin ? ' <span class="ve-chip-search-badge">search</span>' : '')
                      + (p.is_list_plugin   ? ' <span class="ve-chip-list-badge">list</span>'     : '');
      const extraCls = (p.is_search_plugin ? ' ve-chip-search' : '')
                     + (p.is_list_plugin   ? ' ve-chip-list'   : '');
      let extraTip = '';
      if (p.is_search_plugin) extraTip += '\n(drag onto a discover node\'s search port to use as a search backend)';
      if (p.is_list_plugin)   extraTip += '\n(drag onto a series/movies node\'s list port as a list source)';
      html.push(`<button class="ve-chip${extraCls}" data-role="${role}"
        data-plugin="${esc(p.name)}"
        data-tip="${esc(p.description + extraTip)}">${esc(p.name)}${capBadges}</button>`);
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
  pushUndo();
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

// setActiveGraph updates ve.activeGraph AND syncs the pipeline label/region
// DOM. Use this whenever the active pipeline changes — never assign
// ve.activeGraph directly. The DOM sync is idempotent, so call it even when
// gi may equal the current activeGraph (defensive against stale labels).
function setActiveGraph(gi) {
  if (gi < 0 || gi >= ve.graphs.length) return false;
  ve.activeGraph = gi;
  document.querySelectorAll('.ve-pipeline-label[data-graph-idx]').forEach(el => {
    el.classList.toggle('active', parseInt(el.dataset.graphIdx) === gi);
  });
  renderPipelineRegions();
  return true;
}

// selectNode: toggle selection WITHOUT rebuilding DOM (keeps drag div ref valid).
// Clears any multi-selection when called (single-click behaviour).
function selectNode(id) {
  clearMultiSelect();
  const prev = ve.selectedNodeId;
  ve.selectedNodeId = (ve.selectedNodeId === id) ? null : id;

  if (ve.selectedNodeId) {
    const gi = findNodeGraph(ve.selectedNodeId);
    if (gi >= 0) setActiveGraph(gi);
  }

  // Just toggle CSS class — no full DOM rebuild.
  if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
  if (ve.selectedNodeId) document.querySelector(`.ve-node[data-id="${ve.selectedNodeId}"]`)?.classList.add('selected');

  renderEdges();
  renderParamPanel();
}

// toggleMultiSelect: Cmd/Ctrl+click adds/removes a node from the multi-selection
// used for "Extract to function". The param panel and extract button are updated.
// Associated search/list sub-nodes are highlighted together with their parent.
function toggleMultiSelect(id) {
  const n = findNode(id);
  if (!n || n.isSearchNode || n.isListNode) return;
  const adding = !ve.selectedNodeIds.has(id);
  if (adding) {
    ve.selectedNodeIds.add(id);
  } else {
    ve.selectedNodeIds.delete(id);
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
  document.querySelector(`.ve-node[data-id="${id}"]`)?.classList.toggle('multi-selected', adding);
  // Mirror the highlight on associated search/list sub-nodes.
  for (const sid of [...(n.searchNodeIds || []), ...(n.listNodeIds || [])]) {
    // When deselecting, only un-highlight if no other selected main node still owns this sub-node.
    if (!adding) {
      const stillOwned = [...ve.selectedNodeIds].some(mainId => {
        const mn = findNode(mainId);
        return mn?.searchNodeIds?.includes(sid) || mn?.listNodeIds?.includes(sid);
      });
      if (stillOwned) continue;
    }
    document.querySelector(`.ve-node[data-id="${sid}"]`)?.classList.toggle('multi-selected', adding);
  }
  updateExtractButton();
  renderParamPanel();
}

function clearMultiSelect() {
  // Remove from all nodes including sub-nodes (which are not in the set).
  document.querySelectorAll?.('.ve-node.multi-selected')?.forEach(el => el.classList.remove('multi-selected'));
  ve.selectedNodeIds.clear();
  updateExtractButton();
}

function updateExtractButton() {
  const btn = document.getElementById('ve-extract-fn-btn');
  if (!btn) return;
  btn.style.display = ve.selectedNodeIds.size >= 1 && multiSelectIsValid() ? '' : 'none';
  updateCopyButton();
}

function updateCopyButton() {
  const hasSelection = ve.selectedNodeIds.size > 0 || !!ve.selectedNodeId;
  const copyBtn = document.getElementById('ve-copy-btn');
  const cutBtn  = document.getElementById('ve-cut-btn');
  if (copyBtn) copyBtn.style.display = hasSelection ? '' : 'none';
  if (cutBtn)  cutBtn.style.display  = hasSelection ? '' : 'none';
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
  veTooltipHide();
  // Empty-hint only shows when there are zero pipelines.
  const hint = document.getElementById('ve-empty-hint');
  if (hint) hint.style.display = ve.graphs.length === 0 ? '' : 'none';

  renderPipelineRegions(); // drawn first so they sit behind nodes
  renderGraphNodes();
  renderPipelineLabels();
  updateCanvasSize(); // size canvas/SVG BEFORE writing paths — avoids browser
  renderEdges();     // discarding SVG innerHTML when dimensions change after write
}

// ── graph node rendering ───────────────────────────────────────────────────────

function renderGraphNodes() {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return;
  canvas.querySelectorAll('.ve-node').forEach(el => el.remove());

  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      // Route selector nodes are virtual — their port circles are drawn on the
      // parent route node card. Skip rendering them as standalone canvas nodes.
      if (n.isRoutePort) continue;

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
      const isSearch   = !!n.isSearchNode;
      const isList     = !!n.isListNode;
      const isFn       = !!n.isFunctionCall;
      const isRoutePort = !!n.isRoutePort;
      // Sub-connected nodes show a badge in place of the role badge.
      const badgeHtml = isSearch    ? '<span class="ve-node-search-badge">search</span>'
                      : isList      ? '<span class="ve-node-list-badge">list</span>'
                      : isRoutePort ? `<span class="ve-node-route-badge">${esc(n.routePortName || 'port')}</span>`
                      : isFn        ? `<span class="ve-node-role-badge ve-role-${role}">${role}</span><span class="ve-node-fn-badge">fn</span>`
                      : `<span class="ve-node-role-badge ve-role-${role}">${role}</span>`;

      // Sub-nodes (search/list/route-port) are multi-selected when their parent is selected.
      const multiSel = ve.selectedNodeIds.has(n.id) ||
        (isSearch    && n.searchParentId && ve.selectedNodeIds.has(n.searchParentId)) ||
        (isList      && n.listParentId   && ve.selectedNodeIds.has(n.listParentId))   ||
        (isRoutePort && n.routeParentId  && ve.selectedNodeIds.has(n.routeParentId));
      const div = document.createElement('div');
      div.className = `ve-node${sel ? ' selected' : ''}${multiSel ? ' multi-selected' : ''}${isSearch ? ' ve-node-search' : ''}${isList ? ' ve-node-list' : ''}${isRoutePort ? ' ve-node-route-port' : ''}${isFn ? ' ve-node-fn' : ''}${n.autoMigrated ? ' ve-node-auto-migrated' : ''}`;
      div.dataset.role       = role;
      div.dataset.id         = n.id;
      div.dataset.isSearch   = nodeIsSearchPlugin(n) ? 'true' : 'false';
      div.dataset.isList     = nodeIsListPlugin(n)   ? 'true' : 'false';
      div.dataset.isSearchNd = isSearch ? 'true' : 'false';
      div.dataset.isListNd   = isList   ? 'true' : 'false';
      div.style.left = (n.x ?? 60) + 'px';
      div.style.top  = (n.y ?? 60) + 'px';
      const nodeTip = nodeTooltipText(n, meta);
      if (nodeTip) {
        div.addEventListener('mouseenter', e => veTooltipShow(nodeTip, e));
        div.addEventListener('mousemove',  veTooltipMove);
        div.addEventListener('mouseleave', veTooltipHide);
      }
      const commentPreview = n.comment?.trim()
        ? `<div class="ve-node-comment-preview">${esc(n.comment.trim().split('\n')[0])}</div>` : '';
      const commentBtnCls = n.comment?.trim() ? ' has-comment' : '';
      const autoMigratedBadge = n.autoMigrated
        ? `<div class="ve-node-auto-migrated-badge" title="Auto-migrated from a deprecated config shape (${esc(n.autoMigrated)}). This node is not in the text source — edits here are lost unless you also update the text, or save from this visual editor to persist the migration.">auto-migrated</div>`
        : '';
      div.innerHTML = [
        '<div class="ve-node-role-bar"></div>',
        '<div class="ve-node-body">',
          `<div class="ve-node-name">${esc(n.plugin)}</div>`,
          badgeHtml,
          autoMigratedBadge,
          preview        ? `<div class="ve-node-preview">${esc(preview)}</div>` : '',
          commentPreview,
          warns.some(w=>w.level==='error') ? `<div class="ve-node-warn">⚠ ${esc(warns.find(w=>w.level==='error').msg)}</div>`
          : warns.some(w=>w.level==='warn') ? `<div class="ve-node-soft-warn">~ ${esc(warns.find(w=>w.level==='warn').msg)}</div>` : '',
        '</div>',
        `<button class="ve-node-remove" tabindex="-1" title="Remove">×</button>`,
        `<button class="ve-node-comment-btn${commentBtnCls}" tabindex="-1" title="Edit comment">#</button>`,
        // Route nodes show named port circles instead of a single out-port.
        // Always suppress the generic out-port on route nodes.
        n.plugin === 'route' ? (() => {
          const portList = (n.portNodeIds || []).map(portId => {
            const port = g.nodes.find(x => x.id === portId);
            if (!port?.isRoutePort) return '';
            return `<div class="ve-route-port" data-port="${esc(port.routePortName)}" data-port-id="${esc(portId)}"
              title="${esc(port.routePortName)}"></div>`;
          }).join('');
          return portList ? `<div class="ve-route-ports">${portList}</div>` : '';
        })() : (!isSearch && !isList && !isRoutePort) ? `<div class="ve-node-out-port${role === 'sink' ? ' ve-node-chain-port' : ''}" title="${role === 'sink' ? 'Drag to chain to another output node' : 'Drag to connect'}"></div>` : '',
        // Input port indicator: shown on valid drop-targets while dragging an output port.
        (role !== 'source' && !isSearch && !isList && !isRoutePort) ? '<div class="ve-node-in-port"></div>' : '',
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
          text => { n.comment = text; veRender(); onModelChange(); }
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
        } else if (ve.selectedNodeIds.size > 1 && ve.selectedNodeIds.has(n.id)) {
          // Node is already part of a multi-selection.  Defer the single-select
          // decision: if the pointer moves it's a drag (keep all selected nodes
          // moving together); if it lifts without movement it's a click (collapse
          // to single selection).
          const startX = e.clientX, startY = e.clientY;
          function onMoveDecide(ev) {
            if (Math.hypot(ev.clientX - startX, ev.clientY - startY) < 5) return;
            document.removeEventListener('pointermove', onMoveDecide);
            document.removeEventListener('pointerup',   onUpDecide);
            startNodeDrag(e, n);
          }
          function onUpDecide() {
            document.removeEventListener('pointermove', onMoveDecide);
            selectNode(n.id); // collapse multi → single
          }
          document.addEventListener('pointermove', onMoveDecide);
          document.addEventListener('pointerup',     onUpDecide, {once: true});
          document.addEventListener('pointercancel', onUpDecide, {once: true});
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
        if (ve_searchConnecting && ve_searchConnecting.discoverNodeId !== n.id && nodeIsSearchPlugin(n) && !isSearch && !isList) finishSearchConnect(n.id);
        // Receive list-port drop.
        if (ve_listConnecting && ve_listConnecting.parentNodeId !== n.id && nodeIsListPlugin(n) && !isSearch && !isList) finishListConnect(n.id);
      });

      const outPort = div.querySelector('.ve-node-out-port');
      if (outPort) {
        outPort.addEventListener('pointerdown', e => {
          e.stopPropagation();
          e.preventDefault();
          startConnect(e, n.id);
        });
      }

      // Route port circles: each circle starts a connect using the selector's ID.
      // Pass the port's canvas position as the visual anchor so the live
      // cursor line starts from the circle, not the invisible selector node.
      div.querySelectorAll('.ve-route-port').forEach(port => {
        port.addEventListener('pointerdown', e => {
          e.stopPropagation();
          e.preventDefault();
          const canvas  = document.getElementById('ve-graph-canvas');
          const cRect   = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
          const pRect   = port.getBoundingClientRect();
          const anchorX = (pRect.left + pRect.width  / 2 - cRect.left) / ve_zoom;
          const anchorY = (pRect.top  + pRect.height / 2 - cRect.top)  / ve_zoom;
          startConnect(e, port.dataset.portId, anchorX, anchorY);
        });
      });

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
    // Constrain the label to the region width so the × button stays inside the box.
    // Regions are rendered before labels in renderCanvas(), so the element exists.
    const regionEl = canvas.querySelector(`.ve-pipeline-region[data-graph-idx="${i}"]`);
    if (regionEl) label.style.width = regionEl.style.width;
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
      // Clicking a pipeline label both focuses that pipeline AND dismisses
      // any open node param panel — matching the empty-region click behaviour
      // so the two ways to "select a pipeline" stay consistent.
      const prev = ve.selectedNodeId;
      ve.selectedNodeId = null;
      if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
      clearMultiSelect();
      setActiveGraph(gi);
      renderEdges();
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
    if (!ve.fnEditor.active) {
      label.querySelector('.ve-pl-delete').addEventListener('click', e => {
        e.stopPropagation();
        deletePipeline(gi);
      });
    }

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

    // ── _regionH: MUST use identical formula to startNodeDrag.onMove so that
    //    delta = new - prev is zero during normal rendering (no spurious cascade).
    let regionH = 80;
    for (const n of g.nodes) {
      const nodeBot = (n.y ?? 0) + nodeCardHeight(n) + (n.searchNodeIds?.length ? 100 : 24);
      regionH = Math.max(regionH, nodeBot - (g._regionY ?? 0) + 24);
    }
    g._regionH = regionH;

    // ── Visual bounds: tight horizontal (right edge only) and tight vertical top.
    const PAD_X = 24;
    let minX = Infinity, maxX = 0, minY = Infinity;
    for (const n of g.nodes) {
      minX = Math.min(minX, n.x ?? 0);
      maxX = Math.max(maxX, (n.x ?? 0) + NODE_W);
      minY = Math.min(minY, n.y ?? 0);
    }
    if (minX === Infinity) { minX = 0; maxX = 500; minY = (g._regionY ?? 0) + 40; }

    // Top and left are anchored (label position / x=0).
    // Only the right and bottom edges shrink to hug the content.
    const labelTop   = (g._labelY ?? (g._regionY ?? 0) + 8) - 8;
    const regionTop  = labelTop;
    const dispHeight = (g._regionY ?? 0) + regionH - regionTop;
    const regionLeft  = 0;
    const regionWidth = maxX + PAD_X;

    // Update an existing element in-place (smooth during drag — no DOM churn).
    let region = canvas.querySelector(`.ve-pipeline-region[data-graph-idx="${i}"]`);
    if (region) {
      region.className    = `ve-pipeline-region${i === ve.activeGraph ? ' active' : ''}`;
      region.style.top    = regionTop + 'px';
      region.style.height = dispHeight + 'px';
      region.style.left   = regionLeft + 'px';
      region.style.width  = regionWidth + 'px';
      // Keep label width in sync so × stays at the box right edge during drag.
      const label = canvas.querySelector(`.ve-pipeline-label[data-graph-idx="${i}"]`);
      if (label) label.style.width = regionWidth + 'px';
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
    region.style.top    = regionTop + 'px';
    region.style.height = dispHeight + 'px';
    region.style.left   = regionLeft + 'px';
    region.style.width  = regionWidth + 'px';
    // Pipeline selection is handled by the canvas body pointerdown handler
    // via findGraphAtPosition — pointer-events:none on regions prevents direct
    // event handling here.
    if (!g.nodes.length) {
      region.innerHTML = '<div class="ve-region-hint">Drop plugins from the palette to build this pipeline</div>';
    }
    canvas.insertBefore(region, canvas.firstChild);
  }
}

// ── add / manage pipelines ────────────────────────────────────────────────────

function addPipeline() {
  pushUndo();
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
  pushUndo();
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
      // Route selector nodes are virtual — skip their upstream edges.
      // Their connections are represented by the port circles on the route card.
      if (n.isRoutePort) continue;
      for (const upId of (n.upstreams || [])) {
        const up = g.nodes.find(x => x.id === upId);
        if (!up) continue;

        // For route_selector upstreams, draw the edge from the port circle
        // on the parent route card rather than from the invisible node (0,0).
        let x1, y1;
        if (up.isRoutePort && up.routeParentId) {
          const routeNode = g.nodes.find(x => x.id === up.routeParentId);
          if (!routeNode) continue;
          const portIds   = routeNode.portNodeIds || [];
          const portIdx   = portIds.indexOf(upId);
          const portCount = portIds.length;
          const PORT_W  = 20, PORT_GAP = 10;
          const totalW  = portCount * PORT_W + Math.max(0, portCount - 1) * PORT_GAP;
          const portCX  = (routeNode.x ?? 0) + NODE_W / 2 - totalW / 2
                          + portIdx * (PORT_W + PORT_GAP) + PORT_W / 2;
          x1 = portCX;
          y1 = (routeNode.y ?? 0) + NODE_H + PORT_W / 2;
        } else {
          x1 = (up.x ?? 0) + NODE_W;
          y1 = nodeMidY(up.id, up.y);
        }
        const x2 = (n.x ?? 0);
        const y2 = nodeMidY(n.id, n.y);
        const sel = n.id === ve.selectedNodeId || up.id === ve.selectedNodeId;
        // Route port circles are at the bottom (exit down) but processors accept
        // connections on their left side (entry left).
        const d   = up.isRoutePort
          ? edgePath(x1, y1, x2, y2, 'down', 'left')
          : edgePath(x1, y1, x2, y2, 'h');
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
        hit += `<path d="${d}" class="ve-edge-hit" data-src="${upId}" data-dst="${n.id}"
            onmouseenter="showEdgeFieldTooltip(event,'${upId}')"
            onmouseleave="hideEdgeFieldTooltip()"><title>Click to disconnect</title></path>`;
      }
    }
  }

  if (ve_connecting) {
    let x1, y1;
    if (ve_connecting.anchorX !== undefined) {
      // Explicit anchor (e.g. route port circle) overrides node position.
      x1 = ve_connecting.anchorX;
      y1 = ve_connecting.anchorY;
    } else {
      const src = findNode(ve_connecting.srcId);
      if (!src) { /* skip */ } else {
        x1 = (src.x ?? 0) + NODE_W;
        y1 = nodeMidY(src.id, src.y);
      }
    }
    if (x1 !== undefined) {
      const x2 = ve_connecting.curX, y2 = ve_connecting.curY;
      // Route port anchor exits downward, target processors accept from the left.
      const d = ve_connecting.anchorX !== undefined
        ? edgePath(x1, y1, x2, y2, 'down', 'left')
        : edgePath(x1, y1, x2, y2, 'h');
      vis += `<path d="${d}" class="ve-edge connecting"/>`;
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
        const d   = edgePath(x1, y1, x2, y2, 'v');
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
      vis += `<path d="${edgePath(x1, y1, x2, y2, 'v')}" class="ve-search-edge connecting"/>`;
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
        const sel  = ve.selectedNodeId === n.id || ve.selectedNodeId === lnId;
        const mEnd = sel ? '#arrow-list-sel' : '#arrow-list';
        const d    = edgePath(x1, y1, x2, y2, 'v');
        vis += `<path d="${d}" class="ve-list-edge${sel ? ' selected' : ''}" marker-end="url(${mEnd})"` +
               ` data-src="${n.id}" data-dst="${lnId}" data-list="true"/>`;
        hit += `<path d="${d}" class="ve-edge-hit" data-src="${n.id}" data-dst="${lnId}" data-list="true"><title>Click to disconnect</title></path>`;
      }
    }
  }

  // Route-port edges: route_selector nodes are now virtual (invisible).
  // Connections FROM a port go directly from the route card's port position
  // to the downstream node via the normal upstream edge drawing below.

  // Live cursor line while dragging from a list-port.
  // Drawn FROM the list-port (top of parent) UPWARD TO the cursor — same
  // convention as startConnect/startSearchConnect so the fixed anchor is obvious.
  if (ve_listConnecting) {
    const par = findNode(ve_listConnecting.parentNodeId);
    if (par) {
      const x1 = (par.x ?? 0) + NODE_W / 2;
      const y1 = (par.y ?? 0);  // top of parent = list-port
      const x2 = ve_listConnecting.curX, y2 = ve_listConnecting.curY;
      vis += `<path d="${edgePath(x1, y1, x2, y2, 'v-up')}" class="ve-list-edge connecting"/>`;
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
  if (!canvas) return;
  canvas.style.transform = `translate(${ve_panX}px,${ve_panY}px) scale(${ve_zoom})`;
  const pct = Math.round(ve_zoom * 100) + '%';
  const l1 = document.getElementById('ve-zoom-pct');    // pipeline bar
  const l2 = document.getElementById('ve-zoom-pct-fn'); // function bar
  if (l1) l1.textContent = pct;
  if (l2) l2.textContent = pct;
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
  // Route ports are virtual (not rendered as standalone nodes) so exclude from layout.
  const isSub = n => n.isSearchNode || n.isListNode || n.isRoutePort;
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
  // Minimum Y is just below the label (globalY + 40), NOT startY (which includes
  // listPad and would push the list node down to the same level as the parent).
  for (const n of g.nodes) {
    if (!n.listNodeIds?.length) continue;
    const LIST_GAP = 18;
    const totalW   = n.listNodeIds.length * NODE_W + (n.listNodeIds.length - 1) * LIST_GAP;
    const startLX  = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
    n.listNodeIds.forEach((id, i) => {
      const ln = g.nodes.find(x => x.id === id);
      if (ln) {
        ln.x = Math.max(0, startLX + i * (NODE_W + LIST_GAP));
        ln.y = Math.max(globalY + 40, (n.y ?? 0) - NODE_H - 65);
      }
    });
  }

  // Position route-port nodes in a row below their route parent.
  for (const n of g.nodes) {
    if (!n.portNodeIds?.length) continue;
    const PORT_GAP = 18;
    const totalW   = n.portNodeIds.length * NODE_W + (n.portNodeIds.length - 1) * PORT_GAP;
    const startLX  = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
    n.portNodeIds.forEach((id, i) => {
      const port = g.nodes.find(x => x.id === id);
      if (port && (port.x == null || port.y == null)) {
        port.x = Math.max(0, startLX + i * (NODE_W + PORT_GAP));
        port.y = (n.y ?? 0) + NODE_H + 50;
        maxY = Math.max(maxY, port.y + NODE_H + 20);
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
//
// IMPORTANT: callers must pass node y values in RELATIVE form (i.e. as written
// into # pipeliner:pos comments). initLayout's stored-position branch adds
// _regionY to every node y, so calling this on already-absolute coordinates
// produces a per-call drift of _regionY pixels. The undo path subtracts
// _regionY before calling initLayout for this reason.
function initLayout() {
  veDebugLog('initLayout:enter', snapshotGraphPositions(ve.graphs));
  const PIPELINE_GAP = 60;
  let globalY = 40;

  for (const g of ve.graphs) {
    const mainNodes = g.nodes.filter(n => !n.isSearchNode && !n.isListNode && !n.isRoutePort);
    const withPos   = mainNodes.filter(n => n.x != null && n.y != null);

    if (!withPos.length) {
      // No stored positions — full auto-layout.
      globalY = layoutGraph(g, globalY);
      // Normalize so the topmost node sits just below the label and the
      // leftmost is at PAD_X, eliminating wasted space from listPad etc.
      globalY = tightenPipeline(g, globalY, PIPELINE_GAP);
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
      if ((n.isSearchNode || n.isListNode || n.isRoutePort) && n.x != null && n.y != null) {
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
          n.y = (g._labelY ?? g._regionY + 8) + 36 + stackIdx * 120;
          stackIdx++;
        }
        posById[n.id] = n;
        maxAbsX = Math.max(maxAbsX, (n.x ?? 0) + NODE_W + 60);
        maxAbsY = Math.max(maxAbsY, n.y);
      }
    }

    // Derive positions for sub-nodes that don't yet have stored positions.
    placeSubNodes(g, g._regionY + 36);

    // Compute _regionH using the same formula as renderPipelineRegions /
    // startNodeDrag so the region box always fits.  Do NOT call tightenPipeline
    // here — it would shift user-positioned nodes to the tightest possible
    // layout on every re-parse (triggered by save/validate), causing the subtle
    // drift the user sees.  Stored positions are the user's intent and must be
    // preserved exactly.
    let storedRegionH = 80;
    for (const n of g.nodes) {
      const nodeBot = (n.y ?? 0) + NODE_H + (n.searchNodeIds?.length ? 100 : 24);
      storedRegionH = Math.max(storedRegionH, nodeBot - (g._regionY ?? 0) + 24);
    }
    g._regionH = storedRegionH;
    globalY = (g._regionY ?? 0) + storedRegionH + PIPELINE_GAP;
  }
  veDebugLog('initLayout:exit', snapshotGraphPositions(ve.graphs));
}

// tightenPipeline shifts all nodes in g so the topmost node sits just
// below the pipeline label and the leftmost node is at PAD_X.
// Returns the updated globalY for the next pipeline.
//
// WARNING: this mutates n.x and n.y on every node — call only on freshly
// laid-out pipelines (no stored positions) where the user has no intent to
// preserve. The withPos branch of initLayout intentionally does NOT call this
// (see commit bd463fc).
function tightenPipeline(g, currentGlobalY, pipelineGap) {
  const PAD_X    = 50;  // left margin inside the pipeline box
  const LABEL_H  = 36;  // height of the label bar + small gap below it

  let minX = Infinity, minY = Infinity, maxY = 0;
  for (const n of g.nodes) {
    if (n.x != null) minX = Math.min(minX, n.x);
    if (n.y != null) { minY = Math.min(minY, n.y); maxY = Math.max(maxY, n.y + NODE_H); }
  }
  if (minX === Infinity) return currentGlobalY;

  const targetMinY = (g._labelY ?? 40) + LABEL_H;
  const shiftX = minX - PAD_X;
  const shiftY = minY - targetMinY;
  veDebugLog('tightenPipeline', {graphIdx: ve.graphs?.indexOf?.(g), shiftX, shiftY, minX, minY, targetMinY});

  for (const n of g.nodes) {
    if (n.x != null) n.x -= shiftX;
    if (n.y != null) n.y -= shiftY;
  }

  // Recompute _regionH using the SAME formula as the drag handler so the
  // delta in startNodeDrag.onMove starts at zero — no spurious cascade.
  let regionH = 80;
  for (const n of g.nodes) {
    const nodeBot = (n.y ?? 0) + NODE_H + (n.searchNodeIds?.length ? 100 : 24);
    regionH = Math.max(regionH, nodeBot - (g._regionY ?? 0) + 24);
  }
  g._regionH = regionH;
  return (g._regionY ?? 0) + regionH + pipelineGap;
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

// marqueeSelect commits a rubber-band selection given canvas-space corners.
// The graph under the marquee's center is activated first — without this, a
// drag inside an inactive pipeline would scan the wrong graph's nodes and
// select nothing. Returns the array of node IDs added to the multi-selection.
function marqueeSelect(x1, y1, x2, y2) {
  const cx = (x1 + x2) / 2, cy = (y1 + y2) / 2;
  const gi = findGraphAtPosition(cx, cy);
  if (gi < 0) { updateExtractButton(); return []; }
  if (gi !== ve.activeGraph) setActiveGraph(gi);
  const g = ve.graphs[gi];
  const added = [];
  for (const n of g.nodes) {
    if (n.isSearchNode || n.isListNode || n.isRoutePort || n.isUpstreamPseudo) continue;
    const nx = n.x ?? 0, ny = n.y ?? 0;
    if (nx < x2 && nx + NODE_W > x1 && ny < y2 && ny + NODE_H > y1) {
      ve.selectedNodeIds.add(n.id);
      document.querySelector(`.ve-node[data-id="${n.id}"]`)?.classList.add('multi-selected');
      // Mirror Ctrl+Click: highlight any owned search/list sub-nodes too.
      for (const sid of [...(n.searchNodeIds || []), ...(n.listNodeIds || [])]) {
        document.querySelector(`.ve-node[data-id="${sid}"]`)?.classList.add('multi-selected');
      }
      added.push(n.id);
    }
  }
  updateExtractButton(); // also calls updateCopyButton
  return added;
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
  // getBoundingClientRect().top is viewport-relative — no scrollY needed.
  // "height to fill the viewport from this element down" = innerHeight - top.
  const top = layout.getBoundingClientRect().top;
  const padBottom = parseFloat(getComputedStyle(document.body).paddingBottom) || 0;
  const h = Math.max(400, Math.floor(window.innerHeight - padBottom - top));
  layout.style.height = h + 'px';
  // The floating panel uses max-height:calc(100vh - 96px) in CSS — no JS needed.
}

// Set .editor-wrap height so the text editor fills the available viewport
// height, mirroring the visual editor's behaviour.

function fitTextEditor() {
  const wrap = document.querySelector('#view-text .editor-wrap');
  if (!wrap) return;
  // getBoundingClientRect().top is viewport-relative — no scrollY needed.
  const top = wrap.getBoundingClientRect().top;
  const padBottom = parseFloat(getComputedStyle(document.body).paddingBottom) || 0;
  const h = Math.max(300, Math.floor(window.innerHeight - padBottom - top));
  wrap.style.height = h + 'px';
  wrap.style.minHeight = h + 'px';
}

// ── canvas event wiring (once) ─────────────────────────────────────────────────

function initCanvasEvents() {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return;

  // Palette chip tooltips via event delegation (chips are re-rendered on every
  // filter change so individual listeners would need to be re-added each time).
  const paletteBody = document.getElementById('ve-palette-body');
  if (paletteBody) {
    paletteBody.addEventListener('mouseover', e => {
      const chip = e.target.closest('[data-tip]');
      if (chip && chip.dataset.tip) veTooltipShow(chip.dataset.tip, e);
    });
    paletteBody.addEventListener('mousemove', e => {
      if (e.target.closest('[data-tip]')) veTooltipMove(e);
    });
    paletteBody.addEventListener('mouseout', e => {
      if (!e.relatedTarget?.closest('[data-tip]')) veTooltipHide();
    });

    // Palette → canvas drag is pointer-based so it works on mouse and touch
    // alike. The handler distinguishes tap (add at active pipeline) from drag
    // (drop at pointer location) via a small movement threshold.
    paletteBody.addEventListener('pointerdown', e => {
      const chip = e.target.closest('.ve-chip');
      if (!chip) return;
      // The fn-edit / fn-remove buttons sit beside the chip in the wrap,
      // not inside it; closest('.ve-chip') already excludes them. Belt-and-
      // braces in case the markup grows.
      if (e.target.closest('.ve-chip-fn-edit, .ve-chip-fn-remove')) return;
      _beginPaletteDrag(e, chip);
    });
  }

  // Re-fit whichever editor is currently visible on every window resize.
  window.addEventListener('resize', () => {
    if (currentView === 'text') fitTextEditor();
    else fitVisualEditor();
  });

  // Keyboard shortcuts — all suppressed when focus is in a text input.
  window.addEventListener('keydown', e => {
    const inInput = ['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName) ||
                    e.target.isContentEditable;
    if (e.key === 'Escape' && !inInput) {
      if (ve.selectedNodeIds.size > 0) {
        clearMultiSelect();
        renderParamPanel();
      } else if (ve.selectedNodeId) {
        const prev = ve.selectedNodeId;
        ve.selectedNodeId = null;
        document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
        renderParamPanel();
      }
    }
    if ((e.key === 'Delete' || e.key === 'Backspace') && !inInput && ve.selectedNodeId) {
      e.preventDefault();
      removeNode(ve.selectedNodeId);
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'z' && !inInput) {
      e.preventDefault();
      if (!ve.fnEditor.active) undo();
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'x' && !inInput) {
      e.preventDefault();
      if (!ve.fnEditor.active) cutSelected();
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
      hideEdgeFieldTooltip();
      if (hit.dataset.search === 'true') {
        disconnectSearch(hit.dataset.src, hit.dataset.dst);
      } else if (hit.dataset.list === 'true') {
        disconnectList(hit.dataset.src, hit.dataset.dst);
      } else {
        // Regular edge: remove the upstream.
        const tgt = findNode(hit.dataset.dst);
        if (tgt) {
          pushUndo();
          tgt.upstreams = tgt.upstreams.filter(u => u !== hit.dataset.src);
          veRender();
          onModelChange();
        }
      }
    });
  }

  // Wheel to zoom (Ctrl+wheel also works for trackpad pinch-to-zoom).
  canvas.addEventListener('wheel', e => {
    e.preventDefault();
    setZoom(ve_zoom * (e.deltaY < 0 ? 1.1 : 1 / 1.1));
  }, {passive: false});

  // Middle-mouse drag to pan (unlimited, no scrollbars).
  const body = document.getElementById('ve-canvas-body');
  if (body) {
    // Prevent OS context menu on canvas so Ctrl+click doesn't open it on Mac.
    body.addEventListener('contextmenu', e => e.preventDefault());

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

    // Two-finger pan + pinch zoom for touch/pen input. Mouse keeps its
    // existing wheel-zoom + middle-button-pan model and never enters this
    // path. The handler runs in capture phase so it can short-circuit the
    // single-finger rubber-band handler (added next, in bubble phase) when
    // a second pointer lands.
    const touchPointers = new Map(); // pointerId → {x, y}
    body.addEventListener('pointerdown', e => {
      if (e.pointerType === 'mouse') return;
      touchPointers.set(e.pointerId, {x: e.clientX, y: e.clientY});
      if (touchPointers.size !== 2 || ve_pinching) return;

      // Second finger landed → take over.
      e.preventDefault();
      e.stopPropagation();
      ve_pinching = true;
      // The single-finger handler may have already created a marquee; sweep
      // any stray ones from the DOM so they don't linger when pinch ends.
      document.querySelectorAll('.ve-marquee').forEach(el => el.remove());

      const pts = [...touchPointers.values()];
      let lastMidX = (pts[0].x + pts[1].x) / 2;
      let lastMidY = (pts[0].y + pts[1].y) / 2;
      let lastDist = Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y) || 1;

      function onMove(ev) {
        if (!touchPointers.has(ev.pointerId)) return;
        touchPointers.set(ev.pointerId, {x: ev.clientX, y: ev.clientY});
        if (touchPointers.size !== 2) return;
        const p = [...touchPointers.values()];
        const midX = (p[0].x + p[1].x) / 2;
        const midY = (p[0].y + p[1].y) / 2;
        const dist = Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y) || 1;

        // 1. Pan by midpoint screen delta.
        ve_panX += midX - lastMidX;
        ve_panY += midY - lastMidY;

        // 2. Zoom anchored on the new midpoint so the world point under the
        //    midpoint stays under the user's fingers across the zoom.
        const newZoom = Math.max(0.2, Math.min(3.0, ve_zoom * (dist / lastDist)));
        if (newZoom !== ve_zoom) {
          const br = body.getBoundingClientRect();
          const cx = midX - br.left;
          const cy = midY - br.top;
          const r  = newZoom / ve_zoom;
          ve_panX = cx - (cx - ve_panX) * r;
          ve_panY = cy - (cy - ve_panY) * r;
          ve_zoom = newZoom;
        }
        applyZoom();
        lastMidX = midX; lastMidY = midY; lastDist = dist;
      }
      function onUp(ev) {
        touchPointers.delete(ev.pointerId);
        if (touchPointers.size >= 2) return; // still pinching with other pair
        document.removeEventListener('pointermove',   onMove);
        document.removeEventListener('pointerup',     onUp);
        document.removeEventListener('pointercancel', onUp);
        ve_pinching = false;
      }
      document.addEventListener('pointermove',   onMove);
      document.addEventListener('pointerup',     onUp);
      document.addEventListener('pointercancel', onUp);
    }, true /* capture */);

    // Always drop touch pointers from the tracking map on release, even when
    // pinch never engaged (single-finger taps still want their bookkeeping).
    function _dropTouchPointer(e) {
      if (e.pointerType === 'mouse') return;
      touchPointers.delete(e.pointerId);
    }
    body.addEventListener('pointerup',     _dropTouchPointer, true);
    body.addEventListener('pointercancel', _dropTouchPointer, true);

    // Left-button on empty canvas: rubber-band selection or deselect-on-click.
    // Node and pipeline-label pointerdown handlers call stopPropagation() so
    // this only fires when the user clicks/drags on truly empty space.
    body.addEventListener('pointerdown', e => {
      if (e.button !== 0) return;
      if (e.target.closest('.ve-node') || e.target.closest('.ve-pipeline-label')) return;

      const startX = e.clientX, startY = e.clientY;
      let isDragging = false;
      let marquee = null;

      function onMove(ev) {
        // A second finger landing turns this into a pinch gesture; bail out
        // and let the pinch handler take the lead.
        if (ve_pinching) {
          if (marquee) { marquee.remove(); marquee = null; }
          body.removeEventListener('pointermove', onMove);
          return;
        }
        if (!isDragging && Math.hypot(ev.clientX - startX, ev.clientY - startY) < 6) return;
        if (!isDragging) {
          isDragging = true;
          // Clear any selection when a drag starts.
          const prev = ve.selectedNodeId;
          ve.selectedNodeId = null;
          if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
          clearMultiSelect();
          renderEdges();
          renderParamPanel();
          // Create the marquee overlay (in body/viewport coordinates).
          marquee = document.createElement('div');
          marquee.className = 've-marquee';
          body.appendChild(marquee);
        }
        const br = body.getBoundingClientRect();
        marquee.style.left   = Math.min(startX, ev.clientX) - br.left + 'px';
        marquee.style.top    = Math.min(startY, ev.clientY) - br.top  + 'px';
        marquee.style.width  = Math.abs(ev.clientX - startX) + 'px';
        marquee.style.height = Math.abs(ev.clientY - startY) + 'px';
      }

      function onUp(ev) {
        body.removeEventListener('pointermove', onMove);
        // If pinch took over partway through, neither commit a selection nor
        // treat this as a plain click — the user is doing a two-finger gesture.
        if (ve_pinching) { if (marquee) marquee.remove(); return; }
        if (isDragging && marquee) {
          marquee.remove();
          // Convert marquee from body coords to canvas coords and select nodes.
          const br = body.getBoundingClientRect();
          const x1 = (Math.min(startX, ev.clientX) - br.left - ve_panX) / ve_zoom;
          const y1 = (Math.min(startY, ev.clientY) - br.top  - ve_panY) / ve_zoom;
          const x2 = (Math.max(startX, ev.clientX) - br.left - ve_panX) / ve_zoom;
          const y2 = (Math.max(startY, ev.clientY) - br.top  - ve_panY) / ve_zoom;
          marqueeSelect(x1, y1, x2, y2);
        } else {
          // Pure click on empty canvas → deselect and select the pipeline region
          // that was clicked (pipeline regions have pointer-events:none so they
          // can't receive events directly; we detect via canvas coordinates).
          const prev = ve.selectedNodeId;
          ve.selectedNodeId = null;
          if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
          clearMultiSelect();
          const br = body.getBoundingClientRect();
          const cx = (startX - br.left - ve_panX) / ve_zoom;
          const cy = (startY - br.top  - ve_panY) / ve_zoom;
          // Always re-sync labels/regions: a prior selectNode may have moved
          // ve.activeGraph without updating the DOM, so checking
          // gi !== ve.activeGraph here would leave stale highlights forever.
          setActiveGraph(findGraphAtPosition(cx, cy));
          renderEdges();
          renderParamPanel();
        }
      }

      body.addEventListener('pointermove', onMove);
      body.addEventListener('pointerup',     onUp, {once: true});
      body.addEventListener('pointercancel', onUp, {once: true});
    });
  }

  // Palette → canvas dropping is wired in _beginPaletteDrag (see palette
  // section). The HTML5 Drag-and-Drop listeners that used to live here were
  // removed because they never fire on touch input.
}

// ── node drag ─────────────────────────────────────────────────────────────────
// selectNode no longer rebuilds the DOM, so div (found by data-id) stays valid.

function startNodeDrag(e, n) {
  pushUndo();

  // Multi-node drag: when dragging a node that's part of a multi-selection,
  // move all selected main nodes together by the same delta.
  const isMultiDrag = ve.selectedNodeIds.size > 1 && ve.selectedNodeIds.has(n.id);
  if (isMultiDrag) {
    const dragIds = [...ve.selectedNodeIds].filter(id => {
      const nd = findNode(id);
      return nd && !nd.isSearchNode && !nd.isListNode && !nd.isRoutePort && !nd.isUpstreamPseudo;
    });
    // Sub-nodes (search/list/route ports) are not in selectedNodeIds — they
    // only inherit the 'multi-selected' highlight from their parent — but they
    // must follow that parent during the drag. subParent maps sub-id → main-id
    // so each sub-node mirrors its parent's *clamped* delta and never drifts.
    const subParent = new Map();
    for (const id of dragIds) {
      const nd = findNode(id);
      if (!nd) continue;
      for (const sid of [...(nd.searchNodeIds || []), ...(nd.listNodeIds || []), ...(nd.portNodeIds || [])]) {
        subParent.set(sid, id);
      }
    }
    const origPos = new Map([...dragIds, ...subParent.keys()].map(id => {
      const nd = findNode(id);
      return [id, {x: nd?.x ?? 0, y: nd?.y ?? 0}];
    }));
    // Pre-compute each parent's effective clamp so sub-nodes (typically
    // positioned above their parent) stay inside the pipeline region when
    // the parent hits the top/left boundary.
    const effMinByParent = new Map();
    for (const id of dragIds) {
      const orig = origPos.get(id);
      if (!orig) continue;
      const gIdx = findNodeGraph(id);
      const gm   = gIdx >= 0 ? ve.graphs[gIdx] : null;
      const baseMinX = 50;
      const baseMinY = gm ? (gm._labelY ?? (gm._regionY ?? 0) + 8) + 36 : 40;
      let mx = baseMinX, my = baseMinY;
      for (const [sid, pid] of subParent) {
        if (pid !== id) continue;
        const subOrig = origPos.get(sid);
        if (!subOrig) continue;
        mx = Math.max(mx, baseMinX - (subOrig.x - orig.x));
        my = Math.max(my, baseMinY - (subOrig.y - orig.y));
      }
      effMinByParent.set(id, {x: mx, y: my});
    }
    const startX = e.clientX, startY = e.clientY;
    ve_dragging = true;

    function onMoveMulti(ev) {
      if (ve_pinching) return; // second finger took over — leave nodes alone
      const dx = (ev.clientX - startX) / ve_zoom;
      const dy = (ev.clientY - startY) / ve_zoom;
      for (const id of dragIds) {
        const nd = findNode(id);
        const orig = origPos.get(id);
        if (!nd || !orig) continue;
        const eff = effMinByParent.get(id) || {x: 50, y: 40};
        nd.x = Math.max(eff.x, orig.x + dx);
        nd.y = Math.max(eff.y, orig.y + dy);
        const div = document.querySelector(`.ve-node[data-id="${id}"]`);
        if (div) { div.style.left = nd.x + 'px'; div.style.top = nd.y + 'px'; }
      }
      // Sub-nodes mirror their parent's effective (post-clamp) delta so they
      // stay anchored even when the parent group hits a region boundary.
      for (const [sid, pid] of subParent) {
        const sub        = findNode(sid);
        const subOrig    = origPos.get(sid);
        const parent     = findNode(pid);
        const parentOrig = origPos.get(pid);
        if (!sub || !subOrig || !parent || !parentOrig) continue;
        sub.x = subOrig.x + (parent.x - parentOrig.x);
        sub.y = subOrig.y + (parent.y - parentOrig.y);
        const sdiv = document.querySelector(`.ve-node[data-id="${sid}"]`);
        if (sdiv) { sdiv.style.left = sub.x + 'px'; sdiv.style.top = sub.y + 'px'; }
      }
      renderEdges();
      renderPipelineRegions();
      updateCanvasSize();
    }
    function onUpMulti() {
      ve_dragging = null;
      document.removeEventListener('pointermove', onMoveMulti);
      onModelChange();
    }
    document.addEventListener('pointermove', onMoveMulti);
    document.addEventListener('pointerup',     onUpMulti, {once: true});
    document.addEventListener('pointercancel', onUpMulti, {once: true});
    return;
  }

  // Single-node drag (original logic).
  const origX = n.x ?? 0, origY = n.y ?? 0;
  const startX = e.clientX, startY = e.clientY;
  const giSingle = findNodeGraph(n.id);
  const gSingle  = giSingle >= 0 ? ve.graphs[giSingle] : null;
  // Minimum position: keep nodes inside their pipeline region.
  // x=50 and y=labelY+36 match tightenPipeline's PAD_X/LABEL_H so that
  // fresh auto-layouts and drag positions are always consistent — no shift
  // on re-parse after save/validate.
  const minDragX = 50;
  const minDragY = gSingle ? (gSingle._labelY ?? (gSingle._regionY ?? 0) + 8) + 36 : 40;
  ve_dragging = true;
  // Snapshot sub-node (search/list/route-port) origins so they can be
  // translated by the parent's effective delta on every pointermove, keeping
  // them anchored to the parent rather than drifting when the parent clamps
  // against a region boundary.
  const subIds = [...(n.searchNodeIds || []), ...(n.listNodeIds || []), ...(n.portNodeIds || [])];
  const subOrig = new Map(subIds.map(sid => {
    const sn = findNode(sid);
    return [sid, {x: sn?.x ?? 0, y: sn?.y ?? 0}];
  }));
  // Expand the parent's clamp so the most top-left sub-node also stays inside
  // the pipeline region. Sub-nodes (list/search) are typically positioned
  // above the parent — without this, dragging the parent to the top of the
  // region would push the list sub-node above the pipeline label.
  let effMinX = minDragX;
  let effMinY = minDragY;
  for (const [, so] of subOrig) {
    effMinX = Math.max(effMinX, minDragX - (so.x - origX));
    effMinY = Math.max(effMinY, minDragY - (so.y - origY));
  }

  function onMove(ev) {
    if (ve_pinching) return; // second finger took over — leave node alone
    n.x = Math.max(effMinX, origX + (ev.clientX - startX) / ve_zoom);
    n.y = Math.max(effMinY, origY + (ev.clientY - startY) / ve_zoom);
    const div = document.querySelector(`.ve-node[data-id="${n.id}"]`);
    if (div) { div.style.left = n.x + 'px'; div.style.top = n.y + 'px'; }
    const subDx = n.x - origX, subDy = n.y - origY;
    for (const sid of subIds) {
      const sn = findNode(sid);
      const so = subOrig.get(sid);
      if (!sn || !so) continue;
      sn.x = so.x + subDx;
      sn.y = so.y + subDy;
      const sdiv = document.querySelector(`.ve-node[data-id="${sid}"]`);
      if (sdiv) { sdiv.style.left = sn.x + 'px'; sdiv.style.top = sn.y + 'px'; }
    }

    // Recompute this pipeline's region height, then cascade any change in
    // height (up or down) to all subsequent pipelines so they never overlap.
    const gi = findNodeGraph(n.id);
    if (gi >= 0) {
      const g = ve.graphs[gi];
      const prevH = g._regionH ?? 80;
      let regionH = 80;
      for (const nd of g.nodes) {
        const nodeBot = (nd.y ?? 0) + nodeCardHeight(nd) + (nd.searchNodeIds?.length ? 100 : 24);
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
    veDebugLog('nodeDrag:end', {nodeId: n.id, dx: (n.x ?? 0) - origX, dy: (n.y ?? 0) - origY, graphs: snapshotGraphPositions(ve.graphs)});
    onModelChange();
  }
  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp, {once: true});
  document.addEventListener('pointercancel', onUp, {once: true});
}

// ── via-node drag ─────────────────────────────────────────────────────────────



// ── connect interaction ────────────────────────────────────────────────────────

// startConnect begins a drag-to-connect operation.
// srcAnchorX/Y optionally override the visual start position of the line
// (used when the source is a virtual node like a route port circle whose canvas
// position is the port circle rather than the invisible selector node).
function startConnect(e, srcId, srcAnchorX, srcAnchorY) {
  ve_connecting = { srcId, anchorX: srcAnchorX, anchorY: srcAnchorY };
  // Mark source and cycle-risk nodes for CSS highlighting.
  document.querySelector(`.ve-node[data-id="${srcId}"]`)?.classList.add('is-connect-source');
  const gi = findNodeGraph(srcId);
  const g  = gi >= 0 ? ve.graphs[gi] : null;
  if (g) ancestorIds(g.nodes, srcId).forEach(id =>
    document.querySelector(`.ve-node[data-id="${id}"]`)?.classList.add('would-create-cycle'));

  startDragFlow(e,
    (cx, cy) => { ve_connecting.curX = cx; ve_connecting.curY = cy; },
    ev => {
      document.querySelectorAll('.ve-node.is-connect-source').forEach(el => el.classList.remove('is-connect-source'));
      document.querySelectorAll('.ve-node.would-create-cycle').forEach(el => el.classList.remove('would-create-cycle'));
      if (!ev || !ve_connecting) { ve_connecting = null; renderEdges(); return; }
      const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
      const tid = el?.dataset?.id;
      const tgtMeta = tid ? pluginMeta(findNode(tid)?.plugin) : null;
      if (tid && tid !== srcId && tgtMeta?.role !== 'source') finishConnect(tid);
      else { ve_connecting = null; renderEdges(); }
    },
    'is-connecting'
  );
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
      pushUndo();
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
  ve_searchConnecting = { discoverNodeId };
  startDragFlow(e,
    (cx, cy) => { ve_searchConnecting.curX = cx; ve_searchConnecting.curY = cy; },
    ev => {
      if (!ev || !ve_searchConnecting) { ve_searchConnecting = null; renderEdges(); return; }
      const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
      const tid = el?.dataset?.id;
      if (tid && tid !== discoverNodeId && el.dataset.isSearch === 'true' && el.dataset.isSearchNd !== 'true') {
        finishSearchConnect(tid);
      } else { ve_searchConnecting = null; renderEdges(); }
    },
    'is-searchconnecting'
  );
}

// nodeIsListPlugin / nodeIsSearchPlugin check both registered plugins and
// user-defined functions so that function calls can serve as list/search sources.
function nodeIsListPlugin(n) {
  return !!(pluginMeta(n.plugin)?.is_list_plugin || ve.userFunctions[n.plugin]?.is_list_plugin);
}
function nodeIsSearchPlugin(n) {
  return !!(pluginMeta(n.plugin)?.is_search_plugin || ve.userFunctions[n.plugin]?.is_search_plugin);
}

function finishSearchConnect(targetNodeId) {
  const disc   = findNode(ve_searchConnecting?.discoverNodeId);
  const target = findNode(targetNodeId);
  if (disc && target && nodeIsSearchPlugin(target) && !target.isSearchNode) {
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
  ve_listConnecting = { parentNodeId };
  startDragFlow(e,
    (cx, cy) => { ve_listConnecting.curX = cx; ve_listConnecting.curY = cy; },
    ev => {
      if (!ev || !ve_listConnecting) { ve_listConnecting = null; renderEdges(); return; }
      const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
      const tid = el?.dataset?.id;
      if (tid && tid !== parentNodeId && el.dataset.isList === 'true' && el.dataset.isListNd !== 'true') finishListConnect(tid);
      else { ve_listConnecting = null; renderEdges(); }
    },
    'is-listconnecting'
  );
}

function finishListConnect(targetNodeId) {
  const parent = findNode(ve_listConnecting?.parentNodeId);
  const target = findNode(targetNodeId);
  // Also block if the target is already a regular upstream of the parent
  // (one connection type per node).
  if (parent && target && nodeIsListPlugin(target) &&
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
//
// Pointer-based palette → canvas drag. The earlier implementation relied on
// the HTML5 Drag-and-Drop API (`draggable=true`, `dragstart`/`dragover`/`drop`),
// which is mouse-only — touch devices never fire those events. The handler
// below replaces it with pointerdown tracking that works for mouse, pen, and
// touch uniformly. A short movement threshold separates a tap (add to active
// pipeline) from a drag (drop at a specific location).

const PALETTE_DRAG_THRESHOLD = 5; // px before pointerdown becomes a drag

// Build the floating preview card shown under the pointer while dragging.
function _createPalettePreview(name) {
  const meta = pluginMeta(name);
  const role = meta?.role || ve.userFunctions[name]?.role || 'processor';
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
  return preview;
}

function _movePalettePreview(preview, clientX, clientY) {
  preview.style.left = (clientX - NODE_W / 2) + 'px';
  preview.style.top  = (clientY - 44) + 'px';
}

// Highlight the pipeline region under the pointer (if any), the same way the
// old dragover handler did. Returns the matched graph index for caller use.
function _updatePaletteHighlight(clientX, clientY) {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return -1;
  const rect = canvas.getBoundingClientRect();
  const cx = (clientX - rect.left) / ve_zoom;
  const cy = (clientY - rect.top)  / ve_zoom;
  const gi = findGraphAtPosition(cx, cy);
  canvas.querySelectorAll('.ve-pipeline-region').forEach(el => {
    el.classList.toggle('drag-over', parseInt(el.dataset.graphIdx) === gi);
  });
  return gi;
}

function _clearPaletteHighlight() {
  document.querySelectorAll('.ve-pipeline-region.drag-over')
    .forEach(el => el.classList.remove('drag-over'));
}

// Drop the palette item at the given client coordinates. Mirrors the old
// canvas `drop` listener. Returns true if a node was created.
function _dropPaletteAt(clientX, clientY, name) {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return false;
  const rect = canvas.getBoundingClientRect();
  const x = Math.max(0, (clientX - rect.left) / ve_zoom - NODE_W / 2);
  let   y = Math.max(0, (clientY - rect.top)  / ve_zoom - NODE_H / 2);
  const gi = findGraphAtPosition(x + NODE_W / 2, y + NODE_H / 2);
  if (gi < 0) return false;
  const g = ve.graphs[gi];
  const labelBottom = (g._labelY ?? (g._regionY ?? 0) + 8) + 30;
  y = Math.max(y, labelBottom + 4);
  pushUndo();
  const id      = genId(name);
  const dragFd  = ve.userFunctions[name];
  const dragCfg = {};
  if (dragFd) {
    for (const p of (dragFd.params || [])) {
      dragCfg[p.key] = p.default != null ? p.default : emptyForType(p.type);
    }
  }
  g.nodes.push({
    id, plugin: name, config: dragCfg, upstreams: [], x, y, comment: '',
    searchNodeIds: [], listNodeIds: [],
    ...(dragFd ? {isFunctionCall: true, funcCallKey: id} : {}),
  });
  ve.selectedNodeId = id;
  ve.activeGraph    = gi;
  expandRegionForNode(gi, x, y);
  veRender(); onModelChange();
  return true;
}

// Begin a palette drag from a pointerdown on a chip. Movement under the
// threshold falls through to a "tap = add to active pipeline" path so chips
// remain clickable on touch and on mouse without an explicit drag.
function _beginPaletteDrag(downEv, chip) {
  const name = chip.dataset.plugin;
  if (!name) return;
  // Ignore non-primary mouse buttons; touch/pen always proceed.
  if (downEv.pointerType === 'mouse' && downEv.button !== 0) return;

  const startX = downEv.clientX;
  const startY = downEv.clientY;
  let dragging = false;
  let preview  = null;

  function onMove(ev) {
    if (!dragging) {
      const dx = ev.clientX - startX, dy = ev.clientY - startY;
      if (Math.hypot(dx, dy) < PALETTE_DRAG_THRESHOLD) return;
      dragging       = true;
      preview        = _createPalettePreview(name);
      ve.dragSrc     = {type: 'palette', plugin: name};
      veTooltipHide();
    }
    _movePalettePreview(preview, ev.clientX, ev.clientY);
    _updatePaletteHighlight(ev.clientX, ev.clientY);
  }
  function cleanup() {
    document.removeEventListener('pointermove', onMove);
    if (preview) preview.remove();
    _clearPaletteHighlight();
    ve.dragSrc = null;
  }
  function onUp(ev) {
    if (!dragging) {
      // Tap without movement → behave like the old onclick.
      cleanup();
      addNodeFromPalette(name);
      return;
    }
    _dropPaletteAt(ev.clientX, ev.clientY, name);
    cleanup();
  }
  function onCancel() { cleanup(); }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp,     {once: true});
  document.addEventListener('pointercancel', onCancel, {once: true});
}

// ── zoom buttons (called from HTML) ───────────────────────────────────────────

function zoomIn()    { setZoom(ve_zoom * 1.25); }
function zoomOut()   { setZoom(ve_zoom / 1.25); }

// zoomToFitHorizontal sets the zoom so that all pipeline nodes fit within
// the canvas viewport width. Only zooms out (never zooms in beyond 100%).
function zoomToFitHorizontal() {
  const body = document.getElementById('ve-canvas-body');
  if (!body || !body.clientWidth) return;
  let maxX = 0;
  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      if (!n.isSearchNode && !n.isListNode) {
        maxX = Math.max(maxX, (n.x ?? 0) + NODE_W);
      }
    }
  }
  if (maxX <= 0) return;
  const targetZoom = (body.clientWidth - 40) / maxX; // 40px right margin
  ve_zoom = Math.max(0.2, Math.min(1.0, targetZoom));
  applyZoom();
}
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

  // No node selected → hide the floating panel entirely.
  if (!node || !g) {
    veHideParamPanel();
    empty.style.display = ''; title.style.display = 'none';
    body.innerHTML = ''; footer.style.display = 'none';
    return;
  }

  // Node selected → show the floating panel.
  veShowParamPanel();

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

  const meta = pluginMeta(node.plugin) || {role: 'processor', schema: [], produces: [], may_produce: [], requires: []};
  empty.style.display = 'none'; title.style.display = '';
  nameEl.textContent = node.plugin;
  roleEl.textContent = node.isSearchNode ? 'search' : node.isListNode ? 'list' : node.isRoutePort ? `port: ${node.routePortName}` : meta.role;

  if (node.isSearchNode && node.searchParentId) {
    const parentName = findNode(node.searchParentId)?.plugin ?? node.searchParentId;
    footer.innerHTML = [
      `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">Search backend for <b>${esc(parentName)}</b></div>`,
      `<button class="ve-remove-btn" onclick="disconnectSearch(${esc(JSON.stringify(node.searchParentId))},${esc(JSON.stringify(node.id))})">Disconnect from search</button>`,
    ].join('');
  } else if (node.isRoutePort && node.routeParentId) {
    const parentName = findNode(node.routeParentId)?.plugin ?? node.routeParentId;
    footer.innerHTML = [
      `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">Port <b>${esc(node.routePortName)}</b> of route node <b>${esc(parentName)}</b></div>`,
      `<div style="font-size:11px;color:var(--muted)">Connect downstream nodes to this port's output.</div>`,
    ].join('');
  } else if (node.isListNode && node.listParentId) {
    const parentName = findNode(node.listParentId)?.plugin ?? node.listParentId;
    const miniNote = node.isMiniPipeline
      ? `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">Mini-pipeline — edit steps in the config text editor</div>` : '';
    footer.innerHTML = [
      `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">List source for <b>${esc(parentName)}</b></div>`,
      miniNote,
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
    const others = g.nodes.filter(n => {
      if (n.id === node.id || n.isSearchNode || n.isListNode) return false;
      // Don't show a route node's own port chips as upstream candidates.
      if (n.isRoutePort && n.routeParentId === node.id) return false;
      return true;
    });
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
    const searchNodes = g.nodes.filter(nd => nodeIsSearchPlugin(nd));
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
    const listNodes = g.nodes.filter(nd => nodeIsListPlugin(nd));
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

  if (node.isFunctionCall) {
    const cert  = node.outputFields?.certain  || [];
    const reach = node.outputFields?.reachable || [];
    if (cert.length || reach.length) {
      html.push('<div class="ve-field-sep"></div>');
      if (cert.length)
        html.push(`<div class="ve-field-hint-block"><b>Produces:</b> ${cert.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
      if (reach.length)
        html.push(`<div class="ve-field-hint-block ve-field-hint-maybe"><b>May produce:</b> ${reach.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
    }
  } else if (meta.produces?.length || meta.may_produce?.length || meta.requires?.length) {
    html.push('<div class="ve-field-sep"></div>');
    if (meta.produces?.length)
      html.push(`<div class="ve-field-hint-block"><b>Produces:</b> ${meta.produces.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
    if (meta.may_produce?.length)
      html.push(`<div class="ve-field-hint-block ve-field-hint-maybe"><b>May produce:</b> ${meta.may_produce.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
    if (meta.requires?.length)
      html.push(`<div class="ve-field-hint-block"><b>Requires:</b> ${meta.requires.map(f=>`<code>${esc(f)}</code>`).join(' ')}</div>`);
  }

  const warns = fieldWarnings(node);
  const hardWarns = warns.filter(w => w.level === 'error');
  const softWarns = warns.filter(w => w.level === 'warn');
  if (hardWarns.length)
    html.push(`<div class="ve-conn-warn">${hardWarns.map(w => `⚠ ${esc(w.msg)}`).join('<br>')}</div>`);
  if (softWarns.length)
    html.push(`<div class="ve-conn-soft-warn">${softWarns.map(w => `~ ${esc(w.msg)}`).join('<br>')}</div>`);

  // Fields available at this node's input (from upstream pipeline).
  html.push(renderNodeFieldsSection(node));

  html.push('<div class="ve-field-sep"></div>');
  // Wrap main-node fields in a scoped container so collectParams can be wired
  // independently from sub-node (list/search) fields below.
  html.push(`<div class="ve-node-fields" data-node-id="${esc(node.id)}">`);
  if (node.plugin === 'condition') {
    html.push(renderCondRulesWidget(node));
  } else if (node.plugin === 'route') {
    html.push(renderRouteRulesWidget(node));
  } else if (meta.schema?.length) {
    for (const f of meta.schema) {
      // Skip 'search' and 'list' fields — managed visually by the sections above.
      if ((f.key === 'search' && meta.accepts_search) || (f.key === 'list' && meta.accepts_list)) continue;
      html.push(renderField(f, node.config, node));
    }
  } else {
    html.push(renderGenericKV(node.config));
  }
  html.push('</div>');

  // Append parameter sections for connected list and search sub-nodes so their
  // fields can also be promoted to function params (or hardcoded) from here.
  for (const subIds of [node.listNodeIds || [], node.searchNodeIds || []]) {
    for (const sid of subIds) {
      const sn    = findNode(sid);
      const sMeta = sn ? pluginMeta(sn.plugin) : null;
      if (!sn || (!sMeta?.schema?.length && !sn.isMiniPipeline)) continue;
      const badge = sn.isListNode ? '<span class="ve-node-list-badge">list</span>'
                                  : '<span class="ve-node-search-badge">search</span>';
      html.push(`<div class="ve-field-sep"></div>`);
      if (sn.isMiniPipeline) {
        const stepNames = (sn.miniPipelineSteps || []).map(s => esc(s.plugin)).join(' → ');
        html.push(`<div class="ve-sub-node-header">${badge} <span class="ve-sub-node-plugin ve-mini-pipeline-label" title="Mini-pipeline — edit in the config text editor">${stepNames}</span></div>`);
      } else {
        html.push(`<div class="ve-sub-node-header">${badge} <span class="ve-sub-node-plugin">${esc(sn.plugin)}</span></div>`);
        html.push(`<div class="ve-node-fields" data-node-id="${esc(sn.id)}">`);
        for (const f of sMeta.schema) {
          html.push(renderField(f, sn.config, sn));
        }
        html.push('</div>');
      }
    }
  }

  body.innerHTML = html.join('');

  // Wire collectParams for the main node and each sub-node independently.
  body.querySelectorAll('.ve-node-fields').forEach(container => {
    const nid = container.dataset.nodeId;
    const n   = findNode(nid);
    if (!n) return;
    const m = pluginMeta(n.plugin) || {schema: []};
    if (n.plugin !== 'condition' && n.plugin !== 'route' && m.schema?.length) {
      container.querySelectorAll('[data-field]').forEach(el => {
        // input: save model + sync text editor without re-rendering param panel
        // (re-rendering on every keystroke destroys focus mid-typing).
        el.addEventListener('input',  () => { collectParams(n, m.schema, container); onModelChange(); });
        // change (blur): full re-render so canvas node preview updates.
        el.addEventListener('change', () => { collectParams(n, m.schema, container); veRender(); onModelChange(); });
      });
    } else if (n.plugin !== 'condition' && n.plugin !== 'route' && nid === node.id) {
      wireGenericKV(container, n);
    }
  });
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
    if (!nodeIsSearchPlugin(target) || target.isSearchNode) { renderParamPanel(); return; }
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
  veRender(); onModelChange();
}

function toggleList(nodeId, listId, checked) {
  const node   = findNode(nodeId);
  const target = findNode(listId);
  if (!node || !target) return;
  if (checked) {
    if (!nodeIsListPlugin(target) || target.isListNode) { renderParamPanel(); return; }
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
  veRender(); onModelChange();
}

// ── field widgets ─────────────────────────────────────────────────────────────

// ── field registry helpers ────────────────────────────────────────────────────

function getFieldMeta(name) {
  return ve.fieldRegistry.find(f => f.name === name) || null;
}

function getFieldType(name) {
  return getFieldMeta(name)?.type || 'string';
}

function getKnownValues(name) {
  return getFieldMeta(name)?.known_values || null;
}

// deprecationTitle returns a human-readable tooltip string for a deprecated
// field, or '' if the field is not deprecated. Combines the deprecation flag,
// the replacement field (if any), and any free-form deprecation note.
function deprecationTitle(meta) {
  if (!meta || !meta.deprecated) return '';
  let msg = `deprecated — ${meta.description || meta.name}`;
  if (meta.replaced_by) msg += `\nuse "${meta.replaced_by}" instead`;
  if (meta.deprecation_note) msg += `\n${meta.deprecation_note}`;
  return msg;
}

// Operators available for each field type.
// Each op: {id, label, noValue?}
// noValue=true means the value input is hidden (the op is self-contained, e.g. "!= \"\"")
const COND_OPS_BY_TYPE = {
  string: [
    {id: '==',       label: 'equals'},
    {id: '!=',       label: 'not equals'},
    {id: 'contains', label: 'contains'},
    {id: 'matches',  label: 'matches (regex)'},
    {id: '!= ""',    label: 'is set',     noValue: true},
    {id: '== ""',    label: 'is not set', noValue: true},
  ],
  int: [
    {id: '==',   label: '='},
    {id: '!=',   label: '≠'},
    {id: '>',    label: '>'},
    {id: '>=',   label: '≥'},
    {id: '<',    label: '<'},
    {id: '<=',   label: '≤'},
    {id: '> 0',  label: 'is set',     noValue: true},
    {id: '== 0', label: 'is not set', noValue: true},
  ],
  int64: [
    {id: '==',   label: '='},
    {id: '!=',   label: '≠'},
    {id: '>',    label: '>'},
    {id: '>=',   label: '≥'},
    {id: '<',    label: '<'},
    {id: '<=',   label: '≤'},
    {id: '> 0',  label: 'is set',     noValue: true},
    {id: '== 0', label: 'is not set', noValue: true},
  ],
  float: [
    {id: '==',  label: '='},
    {id: '!=',  label: '≠'},
    {id: '>',   label: '>'},
    {id: '>=',  label: '≥'},
    {id: '<',   label: '<'},
    {id: '<=',  label: '≤'},
    {id: '> 0', label: 'is set',     noValue: true},
  ],
  bool: [
    {id: '== true',  label: 'is true',  noValue: true},
    {id: '== false', label: 'is false', noValue: true},
  ],
  string_list: [
    {id: 'contains', label: 'contains'},
    {id: '!= ""',    label: 'is not empty', noValue: true},
    {id: '== ""',    label: 'is empty',     noValue: true},
  ],
  time: [
    {id: '>',  label: 'after'},
    {id: '<',  label: 'before'},
  ],
};

function getOpsForType(type) {
  return COND_OPS_BY_TYPE[type] || COND_OPS_BY_TYPE.string;
}

function opIsNoValue(op) {
  return ['!= ""', '== ""', '> 0', '== 0', '== true', '== false'].includes(op);
}

// ── expression ↔ flat-clause model ───────────────────────────────────────────
//
// The builder represents each rule as a flat list of clauses joined by a single
// combinator (AND or OR).  Mixed combinators fall back to raw mode.

// Parse an expression string into [{field, op, value}] + combinator.
// Returns {clauses, combinator} or null (fall back to raw mode).
function exprToFlatModel(s) {
  if (!s || !s.trim()) return {clauses: [], combinator: 'and'};
  s = s.trim();

  // Split on top-level ' or ' first, then ' and '.
  const orParts  = topLevelSplit(s, ' or ');
  const andParts = topLevelSplit(s, ' and ');

  let parts, combinator;
  if (orParts.length > 1 && andParts.length > 1) return null; // mixed — raw mode
  if (orParts.length > 1)  { parts = orParts;  combinator = 'or'; }
  else if (andParts.length > 1) { parts = andParts; combinator = 'and'; }
  else                          { parts = [s];       combinator = 'and'; }

  const clauses = parts.map(p => parseClause(p.trim())).filter(Boolean);
  if (clauses.length !== parts.length) return null; // at least one unparseable clause

  return {clauses, combinator};
}

// Split s on sep, respecting paren nesting and quoted strings.
function topLevelSplit(s, sep) {
  const parts = [];
  let depth = 0, inStr = false, strChar = '', start = 0;
  const sl = sep.toLowerCase();
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (inStr) { if (c === strChar && s[i-1] !== '\\') inStr = false; i++; continue; }
    if (c === '"' || c === "'") { inStr = true; strChar = c; i++; continue; }
    if (c === '(') { depth++; i++; continue; }
    if (c === ')') { depth--; i++; continue; }
    if (depth === 0 && s.slice(i).toLowerCase().startsWith(sl)) {
      parts.push(s.slice(start, i));
      i += sep.length;
      start = i;
      continue;
    }
    i++;
  }
  parts.push(s.slice(start));
  return parts;
}

// Parse "field op value" or a "no-value" compound operator pattern.
// Returns {field, op, value} or null.
function parseClause(s) {
  s = s.trim();
  if (!s) return null;

  // Remove outer parens
  while (s.startsWith('(') && s.endsWith(')') && isFullyWrapped(s)) {
    s = s.slice(1, -1).trim();
  }

  // Try each op (longer ops first to avoid partial matches)
  const OPS = ['contains', 'matches', '!= ""', '== ""', '!=', '==', '<=', '>=', '<', '>'];
  for (const op of OPS) {
    const opLow = op.toLowerCase();
    const idx = findOpInClause(s, op);
    if (idx === -1) continue;

    const lhs = s.slice(0, idx).trim();
    const rhs = s.slice(idx + opLow.length).trim();

    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(lhs)) continue;

    // For no-value operators the rhs must be empty (consumed by the op)
    if (opIsNoValue(op)) {
      if (rhs === '') return {field: lhs, op, value: ''};
      continue;
    }
    const value = parseExprLiteral(rhs);
    if (value === null) continue;
    return {field: lhs, op, value};
  }
  return null;
}

// Find op at top level in s. Returns index or -1.
function findOpInClause(s, op) {
  const opLow = op.toLowerCase();
  let depth = 0, inStr = false, strChar = '';
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (inStr) { if (c === strChar && s[i-1] !== '\\') inStr = false; continue; }
    if (c === '"' || c === "'") { inStr = true; strChar = c; continue; }
    if (c === '(') { depth++; continue; }
    if (c === ')') { depth--; continue; }
    if (depth !== 0) continue;
    const slice = s.slice(i).toLowerCase();
    if (!slice.startsWith(opLow)) continue;
    // For symbol ops, just check position
    if (!/^[a-z]/.test(opLow)) return i;
    // For word ops, require word boundaries
    const prevOk = i === 0 || /\s/.test(s[i-1]);
    const nextOk = i + opLow.length >= s.length || /\s/.test(s[i + opLow.length]);
    if (prevOk && nextOk) return i;
  }
  return -1;
}

// Parse a literal value token: string, number, or boolean.
function parseExprLiteral(s) {
  if (!s) return null;
  if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'")))
    return s.slice(1, -1);
  if (s.toLowerCase() === 'true') return true;
  if (s.toLowerCase() === 'false') return false;
  const n = Number(s);
  if (!isNaN(n) && s !== '') return n;
  return null;
}

function isFullyWrapped(s) {
  if (!s || s[0] !== '(') return false;
  let d = 0;
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '(') d++;
    else if (s[i] === ')') { d--; if (d === 0 && i < s.length - 1) return false; }
  }
  return d === 0;
}

// Build an expression string from a flat clause model.
function flatModelToExpr(clauses, combinator) {
  if (!clauses.length) return '';
  const parts = clauses.map(c => clauseToStr(c.field, c.op, c.value)).filter(Boolean);
  return parts.join(` ${combinator} `);
}

// Serialise one clause to expression string.
function clauseToStr(field, op, value) {
  if (opIsNoValue(op)) return `${field} ${op}`;
  if (typeof value === 'boolean') return `${field} == ${value}`;
  if (typeof value === 'number')  return `${field} ${op} ${value}`;
  const str = String(value);
  // Use single-quotes for an empty string value so the output cannot be
  // confused with the no-value compound operators '== ""' / '!= ""'.
  // parseExprLiteral handles both quote styles, so round-trips correctly.
  if (str === '') return `${field} ${op} ''`;
  const escaped = str.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
  return `${field} ${op} "${escaped}"`;
}

// ── condition narrowing (client-side, mirrors condnarrow.go) ──────────────────

// Returns true when an op+value pair indicates "field is present/set".
// Handles both the builder's combined no-value form ('!= ""', '> 0') and the
// parsed form produced by exprToFlatModel ('!=' with value='', '>' with value=0).
function isPresenceCheck(op, value) {
  if (op === '!= ""' || op === '> 0') return true;       // builder no-value form
  if (op === '!=' && (value === '' || value == null)) return true; // parsed form
  if (op === '>'  && value === 0)                 return true; // parsed form
  return false;
}

// isAbsenceCheck returns true when an op+value pair tests that a field is
// absent/empty — the logical inverse of isPresenceCheck.
function isAbsenceCheck(op, value) {
  if (op === '== ""' || op === '== 0') return true;       // builder no-value form
  if (op === '==' && (value === '' || value == null)) return true; // parsed form
  if (op === '==' && value === 0)                return true; // parsed numeric form
  return false;
}

// condRejectPromotedFields handles the "absence-check rejection" case:
//   reject: field == ""   →  passing entries have field SET  →  promote to certain
//
// This is the mirror of condRejectedFields (presence ops remove) but for
// absence ops (which promote).  AND/OR semantics are the same:
//   single / OR-of-absence-ops: promote the fields
//   AND of multiple clauses:    NOT(A∧B) = ¬A∨¬B — can't guarantee either present
function condRejectPromotedFields(exprStr, nodeFields) {
  if (!exprStr || !nodeFields) return [];
  const {certain = [], reachable = []} = nodeFields;
  const certSet  = new Set(certain);
  const reachSet = new Set(reachable);

  const model = exprToFlatModel(exprStr);
  if (!model || !model.clauses.length) return [];

  const isMultiAnd = model.combinator === 'and' && model.clauses.length > 1;
  if (isMultiAnd) return []; // NOT(A∧B) = ¬A∨¬B — ambiguous

  const isOr       = model.combinator === 'or' && model.clauses.length > 1;
  const absClause  = model.clauses.filter(c => isAbsenceCheck(c.op, c.value));

  if (isOr) {
    // All clauses must be absence ops: NOT(A∨B) = ¬A∧¬B → both present.
    if (absClause.length !== model.clauses.length) return [];
    return absClause.map(c => c.field).filter(f => reachSet.has(f) && !certSet.has(f));
  }

  // Single absence-op clause: passing entries have the field set.
  return absClause.map(c => c.field).filter(f => reachSet.has(f) && !certSet.has(f));
}

// condAcceptAbsenceRemovedFields handles the "absence-check accept" case for
// ROUTE PORT conditions:
//   accept: field == ""   →  entries reaching this port lack the field  →  remove from reachable
//
// Only valid for route ports (all entries satisfy the condition).
// AND semantics: both fields removed (all clauses hold → both absent).
// OR semantics:  intersection only — field must be absent in every branch.
function condAcceptAbsenceRemovedFields(exprStr, nodeFields) {
  if (!exprStr || !nodeFields) return [];
  const {reachable = []} = nodeFields;
  const reachSet = new Set(reachable);

  const model = exprToFlatModel(exprStr);
  if (!model || !model.clauses.length) return [];

  const absClause = model.clauses.filter(c => isAbsenceCheck(c.op, c.value));

  if (model.combinator === 'and' && model.clauses.length > 1) {
    // AND: all absence-op fields removed.
    return absClause.map(c => c.field).filter(f => reachSet.has(f));
  }

  if (model.combinator === 'or' && model.clauses.length > 1) {
    // OR: intersection — only if the same field appears absent in ALL clauses.
    if (absClause.length !== model.clauses.length) return []; // mixed ops — unsafe
    const fieldCounts = {};
    for (const c of absClause) fieldCounts[c.field] = (fieldCounts[c.field] || 0) + 1;
    return Object.entries(fieldCounts)
      .filter(([, n]) => n === model.clauses.length)
      .map(([f]) => f)
      .filter(f => reachSet.has(f));
  }

  // Single clause.
  return absClause.map(c => c.field).filter(f => reachSet.has(f));
}

// condRejectedFields is the dual of condNarrowedFields.
// For REJECT rules: entries that PASS through are those where the condition
// evaluated to false.  When the condition tests whether a field is present
// (using a presence op), the passing entries are guaranteed NOT to have that
// field set — so it should be removed from the reachable set downstream.
//
// AND semantics: NOT(A∧B) = ¬A∨¬B — at least one is absent, but we can't
//   say which, so we cannot safely remove either field.  Returns [].
// OR semantics:  NOT(A∨B) = ¬A∧¬B — both are absent. Returns all fields
//   whose clauses use presence ops.
// Single clause: NOT(field is set) → field is absent. Returns [field].
//
// Non-presence ops (e.g. == "x", > 5) do not guarantee absence, so they
// are excluded.
function condRejectedFields(exprStr, nodeFields) {
  if (!exprStr || !nodeFields) return [];
  const {reachable = []} = nodeFields;
  const reachSet = new Set(reachable);

  const model = exprToFlatModel(exprStr);
  if (!model || !model.clauses.length) return [];

  const isMultiAnd = model.combinator === 'and' && model.clauses.length > 1;
  if (isMultiAnd) {
    // NOT(A AND B) = NOT A OR NOT B — can't guarantee any specific field absent.
    return [];
  }

  const isOr = model.combinator === 'or' && model.clauses.length > 1;
  const presenceClauses = model.clauses.filter(c => isPresenceCheck(c.op, c.value));

  if (isOr) {
    // For OR: all clauses must use presence ops, otherwise we can't safely
    // remove anything (a non-presence clause means a field may still be present).
    if (presenceClauses.length !== model.clauses.length) return [];
    return presenceClauses.map(c => c.field).filter(f => reachSet.has(f));
  }

  // Single clause: only remove if it's a presence op.
  return presenceClauses.map(c => c.field).filter(f => reachSet.has(f));
}

// Simple client-side field narrowing for the UI preview.
// Returns array of field names promoted from reachable → certain.
function condNarrowedFields(exprStr, nodeFields) {
  if (!exprStr || !nodeFields) return [];
  const {certain = [], reachable = []} = nodeFields;
  const certSet  = new Set(certain);
  const reachSet = new Set(reachable);

  const model = exprToFlatModel(exprStr);
  if (!model) return [];

  const promoted = new Set();
  const isOr = model.combinator === 'or' && model.clauses.length > 1;

  if (isOr) {
    // OR: we only know at least one clause is true, not which one.
    // Only promote a field if it appears in EVERY clause (intersection) —
    // those are the only fields guaranteed regardless of which branch matched.
    // e.g. "description != '' OR rss_category != ''" → neither promoted.
    // e.g. "description != '' OR description contains 'x'" → description promoted.
    const fieldSets = model.clauses.map(c => new Set([c.field]));
    const first = new Set(fieldSets[0]);
    for (let i = 1; i < fieldSets.length; i++) {
      for (const f of first) { if (!fieldSets[i].has(f)) first.delete(f); }
    }
    for (const f of first) {
      if (reachSet.has(f) && !certSet.has(f)) promoted.add(f);
    }
    // Semantic groups require specific values (e.g. enriched==true), so they
    // are never safe to fire for OR conditions.
  } else {
    // AND (or single clause): all referenced fields are true simultaneously.
    for (const c of model.clauses) {
      if (reachSet.has(c.field) && !certSet.has(c.field)) promoted.add(c.field);
    }
    // Semantic sentinel promotions.
    const allFields = model.clauses.map(c => c.field);
    for (const sg of SEMANTIC_GROUPS) {
      if (allFields.includes(sg.sentinel)) {
        for (const f of sg.promotes) {
          if (reachSet.has(f) && !certSet.has(f)) promoted.add(f);
        }
      }
    }
  }

  return [...promoted].sort();
}

const SEMANTIC_GROUPS = [
  {sentinel: 'enriched',         promotes: ['video_year','video_language','video_genres','video_rating','video_popularity','video_imdb_id']},
  {sentinel: 'series_episode_id', promotes: ['series_season','series_episode']},
  {sentinel: 'torrent_link_type', promotes: ['torrent_seeds','torrent_leechers','torrent_file_size','torrent_info_hash']},
];

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

// collectOutputFields recursively collects all fields produced by node and
// its upstream chain into the certain/reachable sets.
// graphNodes is the node list for the CURRENT graph — traversal is restricted
// to that graph so we never bleed fields in from other pipelines.
function collectOutputFields(node, certain, reachable, visited, graphNodes) {
  if (!node || visited.has(node.id)) return;
  visited.add(node.id);

  // Route port nodes (route_selector): apply inferred field narrowing from the
  // port's accept expression, then recurse into upstreams.
  // Both NarrowCertain (presence ops → promote) and AcceptAbsenceRemoved (absence
  // ops → remove) apply since only matched entries reach a route port.
  if (node.isRoutePort) {
    // Recurse into upstreams first to collect their fields.
    for (const upId of (node.upstreams || [])) {
      const up = graphNodes.find(n => n.id === upId);
      if (up) collectOutputFields(up, certain, reachable, visited, graphNodes);
    }
    // Apply inferred narrowing from the port's accept expression.
    const expr = node.portAcceptExpr;
    if (expr) {
      const certArr  = [...certain];
      const reachArr = [...certain, ...reachable];
      for (const f of condNarrowedFields(expr, {certain: certArr, reachable: reachArr})) {
        certain.add(f);
        reachable.add(f);
      }
      for (const f of condAcceptAbsenceRemovedFields(expr, {reachable: reachArr})) {
        reachable.delete(f);
        certain.delete(f);
      }
    }
    return;
  }

  // Function-call nodes: output fields are pre-computed in textToVisualSync()
  // from the return node's server-provided field sets plus its own produces.
  // Use those directly — no recursion needed since outputFields already accounts
  // for the entire internal chain and its external upstreams.
  // Fallback to transparent pass-through when outputFields isn't available yet
  // (e.g. a node just dropped from the palette before a parse round-trip).
  if (node.isFunctionCall) {
    if (node.outputFields) {
      for (const f of node.outputFields.certain)  { certain.add(f); reachable.add(f); }
      for (const f of node.outputFields.reachable) { reachable.add(f); }
    } else {
      for (const upId of (node.upstreams || [])) {
        const up = graphNodes.find(n => n.id === upId);
        if (up) collectOutputFields(up, certain, reachable, visited, graphNodes);
      }
    }
    return;
  }

  // Collect this node's own declared fields from plugin metadata.
  const meta = pluginMeta(node.plugin);
  for (const f of (meta?.produces    || [])) { certain.add(f); reachable.add(f); }
  for (const f of (meta?.may_produce || [])) { reachable.add(f); }

  // Recurse into upstreams (within the same graph only).
  for (const upId of (node.upstreams || [])) {
    const up = graphNodes.find(n => n.id === upId);
    if (up) collectOutputFields(up, certain, reachable, visited, graphNodes);
  }

  // For condition nodes: apply narrowing from the rules config.
  if (node.plugin === 'condition') {
    const rules    = condRulesFromConfig(node.config);
    const certArr  = [...certain];
    const reachArr = [...certain, ...reachable];  // full set (certain ⊆ reachable)

    // Accept rules: promote fields to certain.
    // e.g. "accept: description != ''" → description guaranteed set downstream.
    for (const rule of rules) {
      if (rule.type !== 'accept') continue;
      for (const f of condNarrowedFields(rule.expr, {certain: certArr, reachable: reachArr})) {
        certain.add(f);
      }
    }

    // Reject rules: remove fields from both reachable and certain.
    // Reject rules have TWO effects depending on the operator:
    //
    // (a) Presence ops  (reject: field != "")  → passing entries lack the field
    //     → remove from reachable/certain
    //
    // (b) Absence ops   (reject: field == "")  → passing entries HAVE the field
    //     → promote to certain  (mirror of "accept: field != """)
    for (const rule of rules) {
      if (rule.type !== 'reject') continue;
      for (const f of condRejectedFields(rule.expr, {reachable: reachArr})) {
        reachable.delete(f);
        certain.delete(f);
      }
      for (const f of condRejectPromotedFields(rule.expr, {certain: certArr, reachable: reachArr})) {
        certain.add(f);
      }
    }
  }
}

// computeFnCallOutputFields derives the output field sets for a function-call
// node from its return node's server-provided input fields plus that node's own
// plugin produces/may_produce.  rawById maps node ID → raw server node (includes
// internal nodes filtered from the display graph).
function computeFnCallOutputFields(fc, rawById) {
  const returnRaw = rawById[fc.return_node_id];
  if (!returnRaw) return {certain: [], reachable: []};

  // Server NodeFieldSets: Certain ⊆ Reachable (reachable is the full combined set).
  const certain  = new Set(returnRaw.fields?.certain  || []);
  const reachable = new Set(returnRaw.fields?.reachable || []);
  for (const f of certain) reachable.add(f); // guarantee certain ⊆ reachable

  // Add the return node's own plugin produces / may_produce.
  const retMeta = ve.plugins.find(p => p.name === returnRaw.plugin);
  if (retMeta) {
    for (const f of (retMeta.produces    || [])) { certain.add(f); reachable.add(f); }
    for (const f of (retMeta.may_produce || [])) { reachable.add(f); }
  }

  // If the return node is a condition, apply its narrowing rules.
  if (returnRaw.plugin === 'condition' && returnRaw.config) {
    const rules   = condRulesFromConfig(returnRaw.config);
    const certArr  = [...certain];
    const reachArr = [...reachable];
    for (const rule of rules) {
      if (rule.type === 'accept') {
        for (const f of condNarrowedFields(rule.expr, {certain: certArr, reachable: reachArr}))
          certain.add(f);
      } else if (rule.type === 'reject') {
        for (const f of condRejectedFields(rule.expr, {reachable: reachArr}))
          { reachable.delete(f); certain.delete(f); }
        for (const f of condRejectPromotedFields(rule.expr, {certain: certArr, reachable: reachArr}))
          certain.add(f);
      }
    }
  }

  const certFinal = [...certain].sort();
  return {
    certain:  certFinal,
    reachable: [...reachable].filter(f => !certain.has(f)).sort(),
  };
}

// computeInputFields computes the field sets available at the INPUT of node
// by walking its full upstream chain using plugin metadata from ve.plugins.
function computeInputFields(node) {
  // Find the graph this node belongs to so traversal stays within it.
  const gi = findNodeGraph(node.id);
  const graphNodes = gi >= 0 ? (ve.graphs[gi]?.nodes || []) : [];

  const certain  = new Set();
  const reachable = new Set();
  for (const upId of (node.upstreams || [])) {
    const up = graphNodes.find(n => n.id === upId);
    if (up) collectOutputFields(up, certain, reachable, new Set(), graphNodes);
  }
  return {
    certain:  [...certain].sort(),
    reachable: [...reachable].filter(f => !certain.has(f)).sort(),
  };
}

// renderNodeFieldsSection renders a collapsible "Fields available" section
// showing what's certain and reachable at this node's input.
function renderNodeFieldsSection(node) {
  // Always recompute from the current visual model. Never fall back to
  // node.fields (server-supplied data from the last textToVisualSync) because
  // it becomes stale the moment any node or connection changes in-memory.
  // If the node has no reachable upstreams the section is correctly empty.
  const nf = computeInputFields(node);

  if (!nf || (!nf.certain?.length && !nf.reachable?.length)) return '';

  const certain  = nf.certain  || [];
  const reachOnly = (nf.reachable || []).filter(f => !certain.includes(f));

  const certHtml = certain.length
    ? certain.map(f => {
        const m = getFieldMeta(f);
        const cls = m?.deprecated ? 've-f-tag ve-f-certain ve-f-deprecated' : 've-f-tag ve-f-certain';
        return `<span class="${cls}" title="${esc(deprecationTitle(m) || m?.description || f)}">${esc(f)}</span>`;
      }).join('')
    : '';
  const reachHtml = reachOnly.length
    ? reachOnly.map(f => {
        const m = getFieldMeta(f);
        const cls = m?.deprecated ? 've-f-tag ve-f-reachable ve-f-deprecated' : 've-f-tag ve-f-reachable';
        return `<span class="${cls}" title="${esc(deprecationTitle(m) || m?.description || f)}">${esc(f)}</span>`;
      }).join('')
    : '';

  const id = `ve-fields-${esc(node.id)}`;
  return `<details class="ve-fields-section" open>
    <summary class="ve-fields-summary">
      Fields available at input
      <span class="ve-fields-count">${certain.length} certain, ${reachOnly.length} reachable</span>
    </summary>
    <div class="ve-fields-body">
      ${certHtml ? `<div class="ve-fields-row"><span class="ve-f-label">✓ certain:</span>${certHtml}</div>` : ''}
      ${reachHtml ? `<div class="ve-fields-row"><span class="ve-f-label">◐ reachable:</span>${reachHtml}</div>` : ''}
      <div class="ve-fields-legend">
        <span class="ve-f-tag ve-f-certain">✓</span> guaranteed on every entry
        &nbsp;
        <span class="ve-f-tag ve-f-reachable">◐</span> may not be present
      </div>
    </div>
  </details>`;
}

// Tracks rules the user has explicitly forced into raw mode.
// Keys: "${nodeId}:${ruleIdx}". Persists across re-renders but is
// intentionally in-memory only — it resets on page reload.
const _forcedRaw = new Set();

// renderCondRulesWidget renders the condition editor for a condition node.
// Each rule is either in "builder mode" (structured field/op/value picker) or
// "raw mode" (a plain text input for complex expressions).
function renderCondRulesWidget(node) {
  const rules = condRulesFromConfig(node.config);
  const nf    = node.fields || {certain: [], reachable: []};

  const rowsHtml = rules.map((r, i) => renderCondRule(r, i, nf)).join('');
  const body = rowsHtml || '<div class="ve-cond-empty">No rules yet</div>';

  return `<div class="ve-field">
    <div class="ve-field-label">Rules
      <span class="ve-field-hint">— top to bottom; first match wins; reject beats accept</span>
    </div>
    <div class="ve-cond-rules" id="ve-cond-rules">${body}</div>
    <div style="display:flex;gap:6px;margin-top:6px">
      <button class="ve-add-kv" onclick="addCondRule('reject')">+ Reject</button>
      <button class="ve-add-kv" onclick="addCondRule('accept')">+ Accept</button>
    </div>
  </div>`;
}

// renderRouteRulesWidget renders the route port editor for a route node.
// Each port has a name input and a full expression builder for its accept condition.
function renderRouteRulesWidget(node) {
  const rules = Array.isArray(node.config?.rules) ? node.config.rules : [];
  const nid   = esc(JSON.stringify(node.id));
  const rowsHtml = rules.map((r, i) => renderRouteRuleRow(r, i, node)).join('');
  return `<div class="ve-field">
    <div class="ve-field-label">Ports
      <span class="ve-field-hint">— top to bottom; first match wins; unmatched entries are rejected</span>
    </div>
    <div class="ve-cond-rules" id="ve-route-rules">${rowsHtml || '<div class="ve-cond-empty">No ports yet</div>'}</div>
    <div style="display:flex;gap:6px;margin-top:6px">
      <button class="ve-add-kv" onclick="addRule(${nid},'rules')">+ Add port</button>
    </div>
  </div>`;
}

// Render a single route port row at index ruleIdx.
function renderRouteRuleRow(rule, ruleIdx, node) {
  const nid    = esc(JSON.stringify(node.id));
  const expr   = rule.accept || '';
  const model  = exprToFlatModel(expr);
  const fkey   = _builderRawKey('route', node.id, ruleIdx);
  const rawMode = _forcedRaw.has(fkey) || !model || expr.trim() === '';

  const nf = node.fields || {certain: [], reachable: []};

  const builderHtml = rawMode ? '' : renderBuilderBody(model, ruleIdx, nf, 'route');
  const rawHtml = rawMode
    ? `<textarea class="ve-cond-expr ve-cond-raw-ta" rows="2"
           placeholder="condition expression…"
           oninput="routeRawInput(${ruleIdx},this.value)"
           onchange="routeRawChanged(${ruleIdx},this.value)">${esc(expr)}</textarea>`
    : '';

  const rawBtn = `<button class="ve-cond-raw-btn"
      title="${rawMode ? 'Switch to builder' : 'Switch to raw mode'}"
      onclick="toggleRouteRawMode(${ruleIdx})">${rawMode ? '≡ builder' : '⋮ raw'}</button>`;

  // Route accept rules can both promote (presence ops) and remove (absence ops).
  // Use node.fields if pre-computed; fall back to computeInputFields.
  const inputFields = node.fields || computeInputFields(node);
  const reachAll = {
    certain:   inputFields.certain,
    reachable: [...inputFields.certain, ...inputFields.reachable],
  };
  const promoted = condNarrowedFields(expr, reachAll);
  const removed  = condAcceptAbsenceRemovedFields(expr, {reachable: reachAll.reachable});
  // Show promoted first; if both exist, show both on separate lines
  const narrowHtml = [
    promoted.length ? `<div class="ve-cond-narrow">↳ Promotes to certain: ${promoted.map(f => `<code>${esc(f)}</code>`).join(', ')}</div>` : '',
    removed.length  ? `<div class="ve-cond-narrow ve-cond-narrow-remove">↳ Removes from available: ${removed.map(f => `<code>${esc(f)}</code>`).join(', ')}</div>` : '',
  ].join('');

  const previewHtml = (!rawMode && expr)
    ? `<div class="ve-cond-preview">${esc(expr)}</div>`
    : '';

  return `<div class="ve-cond-rule" data-rule-idx="${ruleIdx}">
    <div class="ve-cond-rule-header">
      <input class="ve-rule-name ve-cond-port-name" placeholder="port name"
        value="${esc(rule.name || '')}"
        oninput="updateRuleNameOnly(${nid},'rules',${ruleIdx},this.value)"
        onblur="syncRoutePortsForNode(${nid})">
      ${rawBtn}
      <button class="ve-cond-del" onclick="removeRule(${nid},'rules',${ruleIdx})" title="Remove">×</button>
    </div>
    ${rawMode ? rawHtml : builderHtml}
    ${previewHtml}
    ${narrowHtml}
  </div>`;
}

// Render a single condition rule at index i.
function renderCondRule(rule, i, nodeFields) {
  const {type, expr} = rule;
  const nodeId  = findNode(ve.selectedNodeId)?.id ?? '';
  const fkey    = `${nodeId}:${i}`;
  const model   = exprToFlatModel(expr);
  const rawMode = _forcedRaw.has(fkey) || !model || expr.trim() === '';

  const builderHtml = rawMode ? '' : renderCondBuilderBody(model, i, nodeFields);
  const rawHtml     = rawMode
    ? `<textarea class="ve-cond-expr ve-cond-raw-ta" rows="2"
           placeholder="expression…"
           oninput="condRawInput(${i},this.value)"
           onchange="condRawChanged(${i},this.value)">${esc(expr)}</textarea>`
    : '';

  const rawBtn = `<button class="ve-cond-raw-btn" title="${rawMode ? 'Switch to builder' : 'Switch to raw mode'}"
      onclick="toggleCondRawMode(${i})">${rawMode ? '≡ builder' : '⋮ raw'}</button>`;

  const effectiveFields = computeInputFields(findNode(ve.selectedNodeId));
  const reachAll = {certain: effectiveFields.certain,
                    reachable: [...effectiveFields.certain, ...effectiveFields.reachable]};
  // For reject rules, two effects are possible depending on the operator:
  //   reject presence op (!= "")  → removes field (shown in red)
  //   reject absence  op (== "")  → promotes field (shown in green, same as accept)
  const promoted = type === 'accept'
    ? condNarrowedFields(expr, reachAll)
    : type === 'reject' ? condRejectPromotedFields(expr, reachAll) : [];
  const removed  = type === 'reject' ? condRejectedFields(expr, reachAll) : [];
  const narrowHtml = promoted.length
    ? `<div class="ve-cond-narrow">↳ Promotes to certain: ${promoted.map(f => `<code>${esc(f)}</code>`).join(', ')}</div>`
    : removed.length
    ? `<div class="ve-cond-narrow ve-cond-narrow-remove">↳ Removes from available: ${removed.map(f => `<code>${esc(f)}</code>`).join(', ')}</div>`
    : '';

  const previewHtml = (!rawMode && expr)
    ? `<div class="ve-cond-preview">${esc(expr)}</div>`
    : '';

  return `<div class="ve-cond-rule" data-rule-idx="${i}">
    <div class="ve-cond-rule-header">
      <button class="ve-cond-type ${type}" onclick="toggleCondType2(${i})"
              title="Click to toggle accept / reject">${type}</button>
      ${rawBtn}
      <button class="ve-cond-del" onclick="deleteCondRule2(${i})" title="Remove">×</button>
    </div>
    ${rawMode ? rawHtml : builderHtml}
    ${previewHtml}
    ${narrowHtml}
  </div>`;
}

// Render the builder body (field picker + op + value rows) for a rule.
// mode='cond' uses cond* handler names; mode='route' uses route* handler names.
function renderBuilderBody(model, ruleIdx, nodeFields, mode) {
  mode = mode || 'cond';
  const {clauses, combinator} = model;
  const nf = computeInputFields(findNode(ve.selectedNodeId));
  const certain  = new Set(nf.certain);
  const reachable = new Set(nf.reachable);

  // Collect fields: certain (green), reachable-only (amber), all known (grey),
  // deprecated (separate optgroup, so they don't pollute the main lists).
  const deprecatedNames = new Set(ve.fieldRegistry.filter(f => f.deprecated).map(f => f.name));
  const allKnown  = ve.fieldRegistry.filter(f => !f.deprecated).map(f => f.name);
  const extraKnown = allKnown.filter(f => !reachable.has(f));

  function fieldOption(name, selected) {
    const meta = getFieldMeta(name);
    let prefix = '';
    if (meta?.deprecated) prefix = '⚠ ';
    else if (certain.has(name))  prefix = '✓ ';
    else if (reachable.has(name)) prefix = '◐ ';
    const tip = deprecationTitle(meta);
    const titleAttr = tip ? ` title="${esc(tip)}"` : '';
    return `<option value="${esc(name)}" ${selected===name?'selected':''}${titleAttr}>${prefix}${esc(name)}</option>`;
  }

  const p = mode; // handler prefix: 'cond' or 'route'
  // The combinator toggle has an irregular name: toggleCombinator (cond) vs routeToggleCombinator (route)
  const combToggleFn = mode === 'cond' ? 'toggleCombinator' : 'routeToggleCombinator';

  const clauseRows = clauses.map((c, ci) => {
    const fieldType = getFieldType(c.field);
    const ops       = getOpsForType(fieldType);
    const noVal     = opIsNoValue(c.op);
    const knownVals = getKnownValues(c.field);

    // Build field selector options
    const certFields  = [...certain].filter(f => !deprecatedNames.has(f)).sort();
    const reachFields = [...reachable].filter(f => !certain.has(f) && !deprecatedNames.has(f)).sort();
    const deprecFields = [...deprecatedNames].sort();
    const optsCert   = certFields.map(f  => fieldOption(f, c.field)).join('');
    const optsReach  = reachFields.map(f => fieldOption(f, c.field)).join('');
    const optsExtra  = extraKnown.map(f  => fieldOption(f, c.field)).join('');
    const optsDeprec = deprecFields.map(f => fieldOption(f, c.field)).join('');
    const fieldSel  = `<select class="ve-cb-field" data-rule="${ruleIdx}" data-clause="${ci}"
        onchange="${p}FieldChanged(${ruleIdx},${ci},this.value)">
        ${certFields.length   ? `<optgroup label="✓ certain">${optsCert}</optgroup>` : ''}
        ${reachFields.length  ? `<optgroup label="◐ reachable">${optsReach}</optgroup>` : ''}
        ${extraKnown.length   ? `<optgroup label="other">${optsExtra}</optgroup>` : ''}
        ${deprecFields.length ? `<optgroup label="⚠ deprecated">${optsDeprec}</optgroup>` : ''}
        ${!reachable.has(c.field) && !allKnown.includes(c.field) && !deprecatedNames.has(c.field)
          ? `<option value="${esc(c.field)}" selected>${esc(c.field)}</option>` : ''}
      </select>`;

    const opsSel = `<select class="ve-cb-op" data-rule="${ruleIdx}" data-clause="${ci}"
        onchange="${p}OpChanged(${ruleIdx},${ci},this.value)">
        ${ops.map(o => `<option value="${esc(o.id)}" ${c.op===o.id?'selected':''}>${esc(o.label)}</option>`).join('')}
      </select>`;

    let valWidget = '';
    if (!noVal) {
      const inputType = (fieldType === 'int' || fieldType === 'int64' || fieldType === 'float') ? 'number'
                      : (fieldType === 'time') ? 'date' : 'text';
      const valStr = c.value == null ? '' : String(c.value);
      const listAttr = knownVals?.length
        ? `list="ve-cb-datalist-${ruleIdx}-${ci}"`
        : '';
      const datalist = knownVals?.length
        ? `<datalist id="ve-cb-datalist-${ruleIdx}-${ci}">${knownVals.map(v=>`<option value="${esc(v)}">`).join('')}</datalist>`
        : '';
      valWidget = `<input class="ve-cb-val" type="${inputType}" value="${esc(valStr)}"
          ${listAttr}
          data-rule="${ruleIdx}" data-clause="${ci}"
          placeholder="value"
          oninput="${p}ValChanged(${ruleIdx},${ci},this.value,this)"
          onchange="${p}ValChanged(${ruleIdx},${ci},this.value,this)">
          ${datalist}`;
    }

    const combRow = (ci < clauses.length - 1)
      ? `<div class="ve-cb-combinator"
            onclick="${combToggleFn}(${ruleIdx})"
            title="Click to toggle AND / OR">${combinator}</div>`
      : '';

    return `<div class="ve-cb-clause">
        ${fieldSel}${opsSel}${valWidget}
        <button class="ve-cond-del ve-cb-clause-del"
                onclick="${p}DeleteClause(${ruleIdx},${ci})" title="Remove clause">×</button>
      </div>${combRow}`;
  }).join('');

  return `<div class="ve-cond-builder" data-rule="${ruleIdx}">
    ${clauseRows || '<div class="ve-cond-empty">No clauses — add one below</div>'}
    <div style="display:flex;gap:4px;margin-top:4px">
      <button class="ve-add-kv" onclick="${p}AddClause(${ruleIdx})">+ Clause</button>
    </div>
  </div>`;
}

// Backwards-compat alias
function renderCondBuilderBody(model, ruleIdx, nodeFields) {
  return renderBuilderBody(model, ruleIdx, nodeFields, 'cond');
}

// ── condition builder event handlers ─────────────────────────────────────────

// Internal: get the current rules array from config.
function _condGetRules() {
  const node = findNode(ve.selectedNodeId);
  return node ? condRulesFromConfig(node.config) : [];
}

// Internal: rebuild config from rules, sync the text editor, and re-render.
function _condSetRules(rules) {
  const node = findNode(ve.selectedNodeId);
  if (!node) return;
  node.config = buildCondConfig(rules);
  onModelChange();  // updates text editor; also schedules a debounced textToVisualSync
  veRender();       // immediate re-render so the builder UI reflects the new rules
}

// ── shared expression-builder helpers ────────────────────────────────────────
// These are the single source of truth for reading/writing clause expressions
// in both condition and route rule editors.

// _builderGetExpr: returns the expression string for rule ruleIdx.
// mode='cond' reads from condition config; mode='route' reads from route rules array.
function _builderGetExpr(mode, ruleIdx) {
  if (mode === 'cond') return _condGetRules()[ruleIdx]?.expr || '';
  const node = findNode(ve.selectedNodeId);
  const rules = Array.isArray(node?.config?.rules) ? node.config.rules : [];
  return rules[ruleIdx]?.accept || '';
}

// _builderSetExpr: writes a new expression for rule ruleIdx and persists.
function _builderSetExpr(mode, ruleIdx, expr) {
  if (mode === 'cond') {
    const rules = _condGetRules();
    if (!rules[ruleIdx]) return;
    rules[ruleIdx].expr = expr;
    _condSetRules(rules);
    return;
  }
  const node = findNode(ve.selectedNodeId);
  const rules = Array.isArray(node?.config?.rules) ? node.config.rules : [];
  if (!rules[ruleIdx]) return;
  rules[ruleIdx].accept = expr;
  syncRoutePortsForNode(node.id);
  onModelChange();
  veRender();
}

function _builderFieldChanged(mode, ruleIdx, clauseIdx, newField) {
  const model = exprToFlatModel(_builderGetExpr(mode, ruleIdx));
  if (!model) return;
  const c = model.clauses[clauseIdx];
  if (!c) return;
  c.field = newField;
  const ops = getOpsForType(getFieldType(newField));
  c.op    = ops[0]?.id || '==';
  c.value = '';
  _builderSetExpr(mode, ruleIdx, flatModelToExpr(model.clauses, model.combinator));
}

function _builderOpChanged(mode, ruleIdx, clauseIdx, newOp) {
  const model = exprToFlatModel(_builderGetExpr(mode, ruleIdx));
  if (!model) return;
  const c = model.clauses[clauseIdx];
  if (!c) return;
  c.op    = newOp;
  c.value = opIsNoValue(newOp) ? '' : c.value;
  _builderSetExpr(mode, ruleIdx, flatModelToExpr(model.clauses, model.combinator));
}

function _builderValChanged(mode, ruleIdx, clauseIdx, newVal, inputEl) {
  const model = exprToFlatModel(_builderGetExpr(mode, ruleIdx));
  if (!model) return;
  const c = model.clauses[clauseIdx];
  if (!c) return;
  const ft = getFieldType(c.field);
  if (ft === 'int' || ft === 'int64') c.value = parseInt(newVal, 10) || 0;
  else if (ft === 'float')             c.value = parseFloat(newVal)  || 0;
  else                                 c.value = newVal;
  const newExpr = flatModelToExpr(model.clauses, model.combinator);
  // Update preview directly for smooth typing
  const ruleEl  = inputEl?.closest('.ve-cond-rule');
  const preview = ruleEl?.querySelector('.ve-cond-preview');
  if (preview) preview.textContent = newExpr;
  // For condition, update config without re-render (avoids losing focus)
  if (mode === 'cond') {
    const rules = _condGetRules();
    if (rules[ruleIdx]) { rules[ruleIdx].expr = newExpr; }
    const node = findNode(ve.selectedNodeId);
    if (node) { node.config = buildCondConfig(rules); onModelChange(); }
    return;
  }
  _builderSetExpr(mode, ruleIdx, newExpr);
}

function _builderAddClause(mode, ruleIdx) {
  const node  = findNode(ve.selectedNodeId);
  const nf    = node?.fields || {certain: [], reachable: []};
  const model = exprToFlatModel(_builderGetExpr(mode, ruleIdx));
  const defaultField = nf.reachable[0] || nf.certain[0] || 'title';
  const ops          = getOpsForType(getFieldType(defaultField));
  const newClause    = {field: defaultField, op: ops[0]?.id || '==', value: ''};
  if (model) {
    model.clauses.push(newClause);
    _builderSetExpr(mode, ruleIdx, flatModelToExpr(model.clauses, model.combinator));
  } else {
    _builderSetExpr(mode, ruleIdx, clauseToStr(newClause.field, newClause.op, newClause.value));
  }
}

function _builderDeleteClause(mode, ruleIdx, clauseIdx) {
  const model = exprToFlatModel(_builderGetExpr(mode, ruleIdx));
  if (!model) return;
  model.clauses.splice(clauseIdx, 1);
  _builderSetExpr(mode, ruleIdx, flatModelToExpr(model.clauses, model.combinator));
}

function _builderToggleCombinator(mode, ruleIdx) {
  const model = exprToFlatModel(_builderGetExpr(mode, ruleIdx));
  if (!model) return;
  model.combinator = model.combinator === 'and' ? 'or' : 'and';
  _builderSetExpr(mode, ruleIdx, flatModelToExpr(model.clauses, model.combinator));
}

// Named shortcuts for inline HTML onclick — condition (keep same names, no HTML changes)
function condFieldChanged(r, c, f)     { _builderFieldChanged('cond', r, c, f); }
function condOpChanged(r, c, o)        { _builderOpChanged('cond', r, c, o); }
function condValChanged(r, c, v, el)   { _builderValChanged('cond', r, c, v, el); }
function condAddClause(r)              { _builderAddClause('cond', r); }
function condDeleteClause(r, c)        { _builderDeleteClause('cond', r, c); }
function toggleCombinator(r)           { _builderToggleCombinator('cond', r); }

// Named shortcuts for inline HTML onclick — route
function routeFieldChanged(r, c, f)    { _builderFieldChanged('route', r, c, f); }
function routeOpChanged(r, c, o)       { _builderOpChanged('route', r, c, o); }
function routeValChanged(r, c, v, el)  { _builderValChanged('route', r, c, v, el); }
function routeAddClause(r)             { _builderAddClause('route', r); }
function routeDeleteClause(r, c)       { _builderDeleteClause('route', r, c); }
function routeToggleCombinator(r)      { _builderToggleCombinator('route', r); }

function addCondRule(type) {
  const rules = _condGetRules();
  const node  = findNode(ve.selectedNodeId);
  const nf    = computeInputFields(node);

  // Pick the first reachable (then certain, then hardcoded fallback) field so
  // the new rule starts in builder mode with a parseable default expression.
  // "source != ''" is used as the ultimate fallback because source is always
  // produced by every source plugin and serialises cleanly without quoting issues.
  const defaultField = nf.reachable[0] || nf.certain[0] || 'source';
  const ops     = getOpsForType(getFieldType(defaultField));
  const isSetOp = ops.find(o => o.noValue) || ops[0];
  const defaultExpr = clauseToStr(defaultField, isSetOp?.id || '!= ""', '');

  rules.push({type, expr: defaultExpr});
  _condSetRules(rules);
}

function deleteCondRule2(ruleIdx) {
  const node = findNode(ve.selectedNodeId);
  const nodeId = node?.id ?? '';
  // Clean up any force-raw entries for this and later rules.
  const rules = _condGetRules();
  for (let i = ruleIdx; i < rules.length; i++) _forcedRaw.delete(`${nodeId}:${i}`);
  rules.splice(ruleIdx, 1);
  _condSetRules(rules);
}

function toggleCondType2(ruleIdx) {
  const rules = _condGetRules();
  if (!rules[ruleIdx]) return;
  rules[ruleIdx].type = rules[ruleIdx].type === 'reject' ? 'accept' : 'reject';
  _condSetRules(rules);
}

// _builderRawKey: returns the _forcedRaw set key for a given mode/nodeId/ruleIdx.
// cond uses "${nodeId}:${ruleIdx}" (legacy format preserved for backwards compat).
// route uses "route:${nodeId}:${ruleIdx}" to prevent collisions.
function _builderRawKey(mode, nodeId, ruleIdx) {
  return mode === 'cond' ? `${nodeId}:${ruleIdx}` : `route:${nodeId}:${ruleIdx}`;
}

function toggleCondRawMode(ruleIdx) {
  const node = findNode(ve.selectedNodeId);
  if (!node) return;
  const rules = _condGetRules();
  if (!rules[ruleIdx]) return;

  const fkey = _builderRawKey('cond', node.id, ruleIdx);
  const expr  = rules[ruleIdx].expr;
  const model = exprToFlatModel(expr);

  if (_forcedRaw.has(fkey)) {
    // Explicitly forced raw → remove the force flag to switch back to builder.
    _forcedRaw.delete(fkey);
  } else if (model) {
    // Currently in builder mode (parseable) → force raw.
    _forcedRaw.add(fkey);
  } else {
    // In raw mode because expression is unparseable → try to switch to builder
    // by replacing the expression with a default parseable one.
    const nf = computeInputFields(node);
    const defaultField = nf.reachable[0] || nf.certain[0] || 'title';
    const ops      = getOpsForType(getFieldType(defaultField));
    const isSetOp  = ops.find(o => o.noValue) || ops[0];
    rules[ruleIdx].expr = clauseToStr(defaultField, isSetOp?.id || '!= ""', '');
    // Clear any stale force-raw flag so it renders in builder mode.
    _forcedRaw.delete(fkey);
  }

  _condSetRules(rules);
}

function condRawInput(ruleIdx, val) {
  // Update preview in real-time without full re-render
  const rule = document.querySelector(`.ve-cond-rule[data-rule-idx="${ruleIdx}"]`);
  if (!rule) return;
  const pre = rule.querySelector('.ve-cond-preview');
  if (pre) pre.textContent = val;
}

function condRawChanged(ruleIdx, val) {
  const rules = _condGetRules();
  if (!rules[ruleIdx]) return;
  rules[ruleIdx].expr = val;
  _condSetRules(rules);
}

function toggleRouteRawMode(ruleIdx) {
  const node = findNode(ve.selectedNodeId);
  if (!node) return;
  const rules = Array.isArray(node.config?.rules) ? node.config.rules : [];
  if (!rules[ruleIdx]) return;
  const fkey  = _builderRawKey('route', node.id, ruleIdx);
  const expr  = rules[ruleIdx].accept || '';
  const model = exprToFlatModel(expr);

  // Determine the current display mode using the same logic as renderRouteRuleRow.
  const isCurrentlyRaw = _forcedRaw.has(fkey) || !model || expr.trim() === '';

  if (!isCurrentlyRaw) {
    // Builder → switch to raw.
    _forcedRaw.add(fkey);
    veRender();
    return;
  }

  // Raw → switch to builder. Remove any forced-raw flag; if the expression is
  // empty or unparseable, replace it with a parseable default so the builder
  // has something to show (mirrors addCondRule behaviour).
  _forcedRaw.delete(fkey);
  if (!model || expr.trim() === '') {
    const nf = computeInputFields(node);
    const df  = nf.reachable[0] || nf.certain[0] || 'title';
    const ops = getOpsForType(getFieldType(df));
    rules[ruleIdx].accept = clauseToStr(df, ops.find(o => o.noValue)?.id || '!= ""', '');
  }
  _builderSetExpr('route', ruleIdx, rules[ruleIdx].accept || '');
}

function routeRawInput(ruleIdx, val) {
  const rule = document.querySelector(`.ve-cond-rule[data-rule-idx="${ruleIdx}"]`);
  if (!rule) return;
  const pre = rule.querySelector('.ve-cond-preview');
  if (pre) pre.textContent = val;
}

function routeRawChanged(ruleIdx, val) {
  _builderSetExpr('route', ruleIdx, val);
}

// Legacy handlers kept for backward compat (may be called from old inline HTML)
function toggleCondType(btn) {
  const row = btn.closest('.ve-cond-rule');
  const idx = row ? parseInt(row.dataset.ruleIdx, 10) : -1;
  if (idx >= 0) { toggleCondType2(idx); return; }
  // Fallback: old DOM-based toggle
  const newType = btn.classList.contains('reject') ? 'accept' : 'reject';
  btn.className = `ve-cond-type ${newType}`;
  btn.textContent = newType;
  updateCondRules();
}

function deleteCondRule(btn) {
  const row = btn.closest('.ve-cond-rule');
  const idx = row ? parseInt(row.dataset.ruleIdx, 10) : -1;
  if (idx >= 0) { deleteCondRule2(idx); return; }
  // Fallback: old DOM-based delete
  const container = row?.closest('.ve-cond-rules');
  if (!row || !container) return;
  row.remove();
  if (!container.querySelector('.ve-cond-row')) container.innerHTML = '<div class="ve-cond-empty">No rules yet</div>';
  updateCondRules();
}

function updateCondRules() {
  const node = findNode(ve.selectedNodeId);
  if (!node) return;
  const rows = document.querySelectorAll('#ve-cond-rules .ve-cond-row');
  const rules = [...rows].map(row => ({
    type: row.querySelector('.ve-cond-type').classList.contains('reject') ? 'reject' : 'accept',
    expr: row.querySelector('.ve-cond-expr')?.value || '',
  }));
  node.config = buildCondConfig(rules);
  veRender(); onModelChange();
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
      case 'rule_list': {
        // Structured list of {name, accept} route rules.
        const rules = Array.isArray(val) ? val : [];
        const nid   = esc(JSON.stringify(node?.id ?? ''));
        const fkey  = esc(f.key);
        const rows  = rules.map((r, i) => {
          const rObj = (typeof r === 'object' && r) ? r : {name: '', accept: String(r ?? '')};
          return `<tr class="ve-rule-row" data-idx="${i}">
            <td><input class="ve-rule-name" placeholder="port name" value="${esc(rObj.name ?? '')}"
              oninput="updateRuleNameOnly(${nid},'${fkey}',${i},this.value)"
              onblur="syncRoutePortsForNode(${nid})"></td>
            <td><input class="ve-rule-cond" placeholder="condition expression" value="${esc(rObj.accept ?? '')}"
              oninput="updateRuleField(${nid},'${fkey}',${i},'accept',this.value)"></td>
            <td><button class="ve-rule-del" onclick="removeRule(${nid},'${fkey}',${i})">×</button></td>
          </tr>`;
        }).join('');
        widget = `<table class="ve-rule-table">
          <thead><tr><th>Name</th><th>Condition</th><th></th></tr></thead>
          <tbody>${rows}</tbody>
        </table>
        <button class="ve-add-kv" style="margin-top:4px" onclick="addRule(${nid},'${fkey}')">+ Add rule</button>`;
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
    veRender(); onModelChange();
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
  // Do NOT call veRender() here — it rebuilds body.innerHTML and destroys focus.
  // Callers decide whether a full re-render is needed (change vs. input event).
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
  document.getElementById(inputId)?.focus();
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

// ── route rule_list handlers ──────────────────────────────────────────────────

function addRule(nodeId, field) {
  const node = findNode(nodeId);
  if (!node) return;
  if (!Array.isArray(node.config[field])) node.config[field] = [];
  node.config[field].push({ name: '', accept: '' });
  renderParamPanel();
  onModelChange();
  // Focus the port-name input of the newly added row (.ve-cond-port-name in the
  // route builder widget; fall back to .ve-rule-name for any other rule_list).
  const inputs = document.querySelectorAll('.ve-cond-port-name, .ve-rule-row .ve-rule-name');
  [...inputs].at(-1)?.focus();
}

function removeRule(nodeId, field, idx) {
  const node = findNode(nodeId);
  if (!node || !Array.isArray(node.config[field])) return;
  node.config[field].splice(idx, 1);
  renderParamPanel();
  onModelChange();
}

function updateRuleField(nodeId, field, idx, key, value) {
  const node = findNode(nodeId);
  if (!node || !Array.isArray(node.config[field])) return;
  if (!node.config[field][idx]) node.config[field][idx] = {};
  node.config[field][idx][key] = value;
  onModelChange();
}

// Update the rule name in the model WITHOUT syncing ports — prevents focus loss
// on every keystroke. syncRoutePortsForNode is called onblur to finalize.
function updateRuleNameOnly(nodeId, field, idx, value) {
  const node = findNode(nodeId);
  if (!node || !Array.isArray(node.config[field])) return;
  if (!node.config[field][idx]) node.config[field][idx] = {};
  node.config[field][idx].name = value;
  // Update text editor without triggering port sync / veRender.
  const el = document.getElementById('config-editor');
  if (el) { el.value = dagToStarlark(); syncHighlight(); }
}

// Called onblur of the port name input: sync port chips and re-render canvas.
function syncRoutePortsForNode(nodeId) {
  const node = findNode(nodeId);
  if (!node) return;
  const g = ve.graphs.find(gr => gr.nodes.some(n => n.id === nodeId));
  if (!g) return;
  const changed = syncRoutePorts(node, g);
  if (changed) {
    veRender();
    const el = document.getElementById('config-editor');
    if (el) { el.value = dagToStarlark(); syncHighlight(); }
  }
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
  const save = () => {
    const cfg = {};
    body.querySelectorAll('.ve-kv-row').forEach(row => {
      const k = row.querySelector('[data-kv-key]')?.value.trim();
      const v = row.querySelector('[data-kv-val]')?.value;
      if (k) { try { cfg[k] = JSON.parse(v); } catch { cfg[k] = v; } }
    });
    node.config = cfg;
  };
  body.addEventListener('input',  () => { save(); onModelChange(); });
  body.addEventListener('change', () => {
    save();
    onModelChange();
    // Skip veRender if Tab moved focus to another field inside this container —
    // veRender rebuilds the whole param panel and would destroy the new focus target.
    if (!body.contains(document.activeElement)) veRender();
  });
  body.querySelectorAll('[data-kv-del]').forEach(btn =>
    btn.addEventListener('click', () => { btn.closest('.ve-kv-row').remove(); save(); veRender(); onModelChange(); }));
  body.querySelector('#ve-kv-add')?.addEventListener('click', () => {
    const row = document.createElement('div');
    row.className = 've-kv-row';
    row.innerHTML = `<input class="ve-kv-key" placeholder="key">
      <input class="ve-kv-val" placeholder="value"><button class="ve-kv-del">×</button>`;
    row.querySelector('.ve-kv-del').addEventListener('click', () => { row.remove(); save(); veRender(); onModelChange(); });
    body.querySelector('#ve-kv-add').before(row);
  });
}

// ── helpers ───────────────────────────────────────────────────────────────────

function configPreview(cfg) {
  const entries = Object.entries(cfg || {}).slice(0, 2);
  return entries.map(([k, v]) => {
    let vs;
    if (Array.isArray(v)) {
      // Array of objects (e.g. route rules): show count summary.
      if (v.length > 0 && typeof v[0] === 'object' && v[0] !== null) {
        vs = `${v.length} rule${v.length !== 1 ? 's' : ''}`;
      } else {
        vs = `[${v.slice(0,2).join(', ')}${v.length>2?'…':''}]`;
      }
    } else if (typeof v === 'string') {
      vs = v.length > 28 ? v.slice(0,28)+'…' : v;
    } else {
      vs = String(v);
    }
    return `${k}: ${vs}`;
  }).join('  ');
}

// fieldWarnings returns [{level:'error'|'warn', msg:string}] for a node.
// 'error' = required field not reachable from any upstream at all.
// 'warn'  = required field reachable but not certain (conditionally produced).
//
// Uses computeInputFields so that condition-node narrowing is respected:
// a condition accept rule that tests "field != ''" promotes that field to
// certain, clearing the warning for downstream nodes that require it.
function fieldWarnings(node) {
  const meta = pluginMeta(node.plugin);
  if (!meta?.requires?.length) return [];

  const nf       = computeInputFields(node);
  const certain  = new Set(nf.certain);
  // reachable in computeInputFields excludes certain; rebuild the full set.
  const reachable = new Set([...nf.certain, ...nf.reachable]);

  const warns = [];
  for (const f of meta.requires) {
    if (!reachable.has(f)) {
      warns.push({level: 'error', msg: `requires "${f}" — add ${f}-producing node upstream`});
    } else if (!certain.has(f)) {
      warns.push({level: 'warn', msg: `requires "${f}" — only conditionally produced upstream; plugin may silently skip entries missing this field`});
    }
  }
  return warns;
}

function pluginMeta(name) {
  if (ve.userFunctions[name]) {
    const fd = ve.userFunctions[name];
    return {name: fd.name, role: fd.role, description: fd.description, schema: fd.params || [],
            produces: [], may_produce: [], requires: [], is_user_function: true};
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
  pushUndo();

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

// cutSelected copies the current selection to clipboard, then deletes the nodes.
function cutSelected() {
  const ids = ve.selectedNodeIds.size > 0
    ? [...ve.selectedNodeIds].filter(id => {
        const n = findNode(id);
        return n && !n.isSearchNode && !n.isListNode && !n.isUpstreamPseudo;
      })
    : ve.selectedNodeId ? [ve.selectedNodeId] : [];
  if (!ids.length) return;
  pushUndo();
  copySelected();
  removeNodeBatch(ids);
  if (ids.includes(ve.selectedNodeId)) ve.selectedNodeId = null;
  clearMultiSelect();
  veRender();
  onModelChange();
}

// removeNodeBatch removes multiple nodes without triggering a render per-node.
function removeNodeBatch(ids) {
  for (const id of ids) {
    if (findNode(id)?.isUpstreamPseudo) continue;
    for (const g of ve.graphs) {
      const idx = g.nodes.findIndex(n => n.id === id);
      if (idx < 0) continue;
      const [removed] = g.nodes.splice(idx, 1);
      for (const n of g.nodes) {
        n.upstreams     = (n.upstreams     || []).filter(u => u !== id);
        n.searchNodeIds = (n.searchNodeIds || []).filter(u => u !== id);
        n.listNodeIds   = (n.listNodeIds   || []).filter(u => u !== id);
      }
      if (removed.searchParentId) {
        const p = g.nodes.find(n => n.id === removed.searchParentId);
        if (p) p.searchNodeIds = (p.searchNodeIds || []).filter(u => u !== id);
      }
      if (removed.listParentId) {
        const p = g.nodes.find(n => n.id === removed.listParentId);
        if (p) p.listNodeIds = (p.listNodeIds || []).filter(u => u !== id);
      }
      break;
    }
  }
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
// (and their connected list/search sub-nodes) as candidate function parameters.
function inferExtractionParams() {
  const params = [];
  const usedNames = new Set(['upstream']); // reserved
  let dedupCounter = 0;

  function collectNode(n) {
    for (const [key, val] of Object.entries(n.config || {})) {
      if (val === null || val === undefined || val === '') continue;
      let pName = key;
      if (usedNames.has(pName)) pName = `${n.plugin.replace(/[^a-zA-Z0-9]/g, '_')}_${key}`;
      if (usedNames.has(pName)) pName = `${pName}_${dedupCounter++}`;
      usedNames.add(pName);
      const type = Array.isArray(val) ? 'list'
                 : typeof val === 'boolean' ? 'bool'
                 : typeof val === 'number'  ? 'int' : 'string';
      params.push({nodeId: n.id, configKey: key, paramName: pName, type, defaultValue: val, include: true});
    }
  }

  for (const id of ve.selectedNodeIds) {
    const n = findNode(id);
    if (!n) continue;
    collectNode(n);
    // Also collect params from connected list and search sub-nodes.
    for (const sid of [...(n.listNodeIds || []), ...(n.searchNodeIds || [])]) {
      const sn = findNode(sid);
      if (sn) collectNode(sn);
    }
  }
  return params;
}

// nodesToFunctionSource generates the Starlark def block text for the extracted function.
function nodesToFunctionSource(funcName, params, selectedIds, validation, graph, comment = '') {
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
  if (comment?.trim()) {
    for (const cl of comment.trim().split('\n')) lines.push(`# ${cl}`);
  }
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

  const regionY = graph._regionY ?? 0;
  for (const n of ordered) {
    // Persist position so reopening the function editor restores the layout.
    if (n.x != null && n.y != null) {
      lines.push(`    # pipeliner:pos ${Math.round(n.x)} ${Math.round(n.y - regionY)}`);
    }
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
        .map(sn => viaNodeToStar(sn, paramLookup[sn.id])).join(', ');
      cfgParts.push(`search=[${items}]`);
    }
    if (n.listNodeIds?.length) {
      const items = n.listNodeIds
        .map(id => graph.nodes.find(x => x.id === id)).filter(Boolean)
        .map(ln => viaNodeToStar(ln, paramLookup[ln.id])).join(', ');
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
  if (!validation.ok) { veShowError(validation.error); return; }

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
        <div class="ve-extract-field">
          <label>Comment <span style="font-weight:400;text-transform:none;letter-spacing:0">(optional)</span></label>
          <textarea id="ve-extract-comment" rows="2" placeholder="Describe what this function does…"></textarea>
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
    const comment = document.getElementById('ve-extract-comment').value.trim();
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
    performExtraction(name, params, validation, graphIdx, comment);
  };

  document.getElementById('ve-extract-name').focus();
  document.getElementById('ve-extract-name').select();
}

// performExtraction replaces the selected nodes with a function call node and
// registers the new user function definition.
function performExtraction(funcName, params, validation, graphIdx, comment = '') {
  pushUndo();
  const {entryUpstreams, returnNodeId} = validation;
  const selectedIds = new Set(ve.selectedNodeIds); // snapshot before clearing
  const g = ve.graphs[graphIdx];

  // Generate the function source text.
  const sourceText = nodesToFunctionSource(funcName, params, selectedIds, validation, g, comment);

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

  // Determine if this function acts as a list or search source BEFORE removing
  // the selected nodes — after the filter they are no longer in g.nodes.
  const is_list_plugin   = [...selectedIds].some(id => {
    const nd = g.nodes.find(x => x.id === id);
    return nd && nodeIsListPlugin(nd);
  });
  const is_search_plugin = [...selectedIds].some(id => {
    const nd = g.nodes.find(x => x.id === id);
    return nd && nodeIsSearchPlugin(nd);
  });

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
    name:           funcName,
    role,
    description:    '',
    comment:        comment,
    params:         params.filter(p => p.include).map(p => ({
      key: p.paramName, type: p.type, required: true, default: null, hint: p.hint || '',
    })),
    _sourceText:    sourceText,
    is_list_plugin,
    is_search_plugin,
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
    if (!r.ok) { veShowError('Could not expand function: server error ' + r.status); return; }
    data = await r.json();
  } catch (e) {
    veShowError('Could not expand function: ' + e);
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
    // Pre-build a map so function→function entry upstreams resolve correctly:
    // when function B's entry node has upstream=<return_node of A>, we replace
    // it with A's call_key rather than the internal variable name.
    const returnToCallKey1 = {};
    for (const fc of keepCalls) returnToCallKey1[fc.return_node_id] = fc.call_key;

    for (const fc of keepCalls) {
      const internalSet = new Set(fc.internal_node_ids);
      const entryUpstreams = [];
      for (const n of rawNodes) {
        if (!internalSet.has(n.id)) continue;
        for (const up of (n.upstreams || [])) {
          if (!internalSet.has(up)) entryUpstreams.push(returnToCallKey1[up] ?? up);
        }
      }
      for (const n of nodes) {
        n.upstreams = n.upstreams.map(u => u === fc.return_node_id ? fc.call_key : u);
      }
      // fc.args is always empty from the server — recover call-site kwargs from source.
      const recoveredArgs = (fc.args && Object.keys(fc.args).length)
        ? fc.args : parseFunctionCallArgs(content, fc.call_key, fc.func);
      nodes.push({
        id: fc.call_key, plugin: fc.func, config: recoveredArgs,
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

// parseFunctionComment extracts user-written comment lines from a function's
// source text — lines before def that are not pipeliner: machine comments.
function parseFunctionComment(sourceText) {
  if (!sourceText) return '';
  const lines = [];
  for (const line of sourceText.split('\n')) {
    if (/^def\s/.test(line)) break;
    if (line.startsWith('#') && !line.startsWith('# pipeliner:')) {
      lines.push(line.startsWith('# ') ? line.slice(2) : line.slice(1));
    }
  }
  return lines.join('\n');
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

// fnMaybeMigrateLegacyQuality applies the legacy `quality=` rewrite on a
// freshly-parsed function-body node. Mirrors legacyQualityMigration in
// internal/config/migrations.go — when the Go side gets a config it rewrites
// it transparently; the function-body parser is JS-only so the same logic
// has to live here too. Keep these two in sync.
function fnMaybeMigrateLegacyQuality(node, nodes) {
  if (node.plugin !== 'series' && node.plugin !== 'movies' && node.plugin !== 'premiere') return;
  if (!('quality' in node.config)) return;
  const spec = node.config.quality;
  // Preserve param reference: if the original code had `quality=quality`,
  // the synthesized quality node's spec param should reference the same param.
  // The body's config.quality is just a placeholder (the param's default, or
  // an empty value when the param has no default), so the param ref — not the
  // literal value — is what actually matters at runtime.
  const paramRef = node._paramRefs?.quality;
  // Skip only when neither a literal spec nor a param ref is present:
  // nothing to migrate. An empty literal *with* a param ref still migrates.
  if ((spec === '' || spec == null) && !paramRef) return;

  delete node.config.quality;
  if (node._paramRefs) delete node._paramRefs.quality;
  if (node._paramRefs && Object.keys(node._paramRefs).length === 0) delete node._paramRefs;

  const qid = `_auto_quality_${node.id}`;
  const qNode = {
    id: qid, plugin: 'quality',
    config: {spec: spec ?? ''},
    upstreams: node.upstreams.slice(),
    searchNodeIds: [], listNodeIds: [], comment: '',
    autoMigrated: 'legacy-quality-knob',
    x: null, y: null, // layout will be recomputed
  };
  if (paramRef) qNode._paramRefs = {spec: paramRef};
  nodes.push(qNode);
  node.upstreams = [qid];
}

// fnRegenerateSourceForMigration parses funcName's body, applies any JS-side
// migrations via parseFunctionBodyNodes (which runs fnMaybeMigrateLegacyQuality
// on each node), and re-serialises the body back to Starlark via
// nodesToFunctionSource. Returns the new source text, or null if no migration
// fired (so the caller can leave _sourceText untouched).
//
// Called once per loaded user function during textToVisualSync. Without this,
// the call-site card correctly shows the auto-migrated badge (the Go-side
// migration tags the injected internal node), but fd._sourceText still holds
// the legacy form — so the next dagToStarlark() round-trips the deprecated
// `quality=` back to disk. Re-baking _sourceText on load means a plain save
// from the visual editor is enough to persist the migration.
function fnRegenerateSourceForMigration(funcName) {
  const fd = ve.userFunctions[funcName];
  if (!fd?._sourceText) return null;
  const parsed = parseFunctionBodyNodes(funcName);
  if (!parsed) return null;
  const {nodes, returnNodeId} = parsed;
  if (!nodes.some(n => n.autoMigrated)) return null;

  const mainNodes   = nodes.filter(n => !n.isSearchNode && !n.isListNode);
  const selectedIds = new Set(mainNodes.map(n => n.id));
  const entryUpstreams = mainNodes.some(n => (n.upstreams || []).includes('_upstream'))
    ? ['__entry__'] : [];

  // Map each declared param to whichever node/configKey references it via
  // _paramRefs. Mirrors saveFunctionEditor's param-binding logic.
  const fnParams = [];
  for (const p of (fd.params || [])) {
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
      fnParams.push({nodeId: null, configKey: null, paramName: p.key,
        type: p.type, defaultValue: p.default, include: true, hint: p.hint || ''});
    }
  }

  const graph      = {name: funcName, schedule: '', comment: '', nodes, _regionY: 0};
  const validation = {entryUpstreams, returnNodeId};
  return nodesToFunctionSource(funcName, fnParams, selectedIds, validation, graph, fd.comment || '');
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

  let pendingPos = null; // set by # pipeliner:pos comments inside the body
  for (let i = defIdx + 1; i < lines.length; i++) {
    const raw = lines[i];
    if (!raw.trim()) continue;
    if (!/^\s/.test(raw)) break; // end of function body

    const line = raw.trim();
    if (line.startsWith('return ')) {
      returnNodeId = line.slice(7).trim();
      continue;
    }

    // Capture position comment so the next node can use it.
    const posM = line.match(/^#\s*pipeliner:pos\s+(-?\d+)\s+(-?\d+)/);
    if (posM) { pendingPos = {x: parseInt(posM[1], 10), y: parseInt(posM[2], 10)}; continue; }

    // Match: var = input/process/output("plugin", ...kwargs...)
    // Use a non-greedy match to the last ) on the line.
    const m = line.match(/^(\w+)\s*=\s*(input|process|output)\((.+)\)\s*$/);
    if (!m) { pendingPos = null; continue; }

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
      x: pendingPos?.x ?? null, y: pendingPos?.y ?? null,
    };
    pendingPos = null; // consumed
    if (Object.keys(paramRefs).length)  node._paramRefs = paramRefs;
    if (searchRaw !== null)             node._searchRaw  = searchRaw;
    if (listRaw   !== null)             node._listRaw    = listRaw;

    // Apply the same legacy-quality migration the Go side runs at config
    // load time (see internal/config/migrations.go). The function-body
    // editor is a JS-only parse path, so without this it would show the
    // deprecated quality= knob on movies/series/premiere and would
    // round-trip it back to the source on save.
    fnMaybeMigrateLegacyQuality(node, nodes);

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
    veShowError(`Cannot open function "${funcName}": the function body could not be parsed. Try editing it in the text editor first.`);
    return;
  }

  const {nodes, returnNodeId} = parsed;
  const allNodes = [...nodes];

  // Snapshot current canvas state for restoration on exit.
  ve.fnEditor = {
    active:          true,
    funcName,
    savedGraphs:     ve.graphs,
    savedActive:     ve.activeGraph,
    savedNextId:     ve.nextId,
    savedSelected:   ve.selectedNodeId,
    returnNodeId,
    // Snapshot of params at open time (for change detection on save).
    paramsSnapshot:  JSON.parse(JSON.stringify(fd.params || [])),
    paramsOpen:      false,
    commentSnapshot: fd.comment || '',
  };

  const hasStoredLayout = allNodes.some(n => !n.isSearchNode && !n.isListNode && n.x != null && n.y != null);
  ve.graphs      = [{name: funcName, schedule: '', comment: '', nodes: allNodes, _hasLayout: hasStoredLayout}];
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
  const commentBtn = document.getElementById('ve-fn-bar-comment-btn');
  if (commentBtn) commentBtn.classList.toggle('has-comment', !!(fd.comment?.trim()));

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
    veShowError('The function body is empty. Add at least one node.');
    return;
  }
  if (terminals.length > 1) {
    veShowError('The function has multiple output nodes. Connect them into a single chain first.');
    return;
  }

  const returnNodeId      = terminals[0]?.id ?? mainNodes[mainNodes.length - 1].id;
  // A function needs an 'upstream' parameter if any body node connects to the
  // '_upstream' entry point (implicit for processor/sink functions).
  const entryUpstreams = mainNodes.some(n => (n.upstreams || []).includes('_upstream'))
    ? ['__entry__'] : [];
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

  const newSourceText = nodesToFunctionSource(ve.fnEditor.funcName, fnParams, selectedIds, validation, g, ve.fnEditor.commentSnapshot || '');

  fd._sourceText = newSourceText;
  fd.params      = currentParams;
  fd.comment     = ve.fnEditor.commentSnapshot || '';
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
  // savedGraphs holds absolute y + the previous _regionY; un-shift before
  // re-laying out or every node drifts down by _regionY on each exit.
  initLayoutFromAbsolute('exitFunctionEditor');
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
    veShowError('Function name must be a valid identifier.');
    const inp = document.getElementById('ve-fn-bar-name');
    if (inp) inp.value = oldName;
    return;
  }
  if (ve.userFunctions[newName] && newName !== oldName) {
    veShowError(`A function named "${newName}" already exists.`);
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

// fnEditorEditComment opens the text popup to edit the function's comment.
function fnEditorEditComment() {
  if (!ve.fnEditor.active) return;
  openTextPopup(
    `Comment — function "${ve.fnEditor.funcName}"`,
    'Enter a comment (shown above the function definition in the config file)…',
    ve.fnEditor.commentSnapshot || '',
    text => {
      ve.fnEditor.commentSnapshot = text;
      const btn = document.getElementById('ve-fn-bar-comment-btn');
      if (btn) btn.classList.toggle('has-comment', !!text.trim());
    }
  );
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
  const nameInputs = document.querySelectorAll('.ve-fn-param-row .ve-fn-param-name');
  nameInputs[nameInputs.length - 1]?.focus();
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
    veShowError(`Parameter "${newKey}" already exists.`);
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
// node into a function parameter reference.  A new param is created with an
// auto-generated unique name; the params panel opens so the user can rename it.
function fnEditorPromoteToParam(nodeId, configKey, fieldType) {
  if (!ve.fnEditor.active) return;
  const node = findNode(nodeId);
  if (!node) return;

  const params = ve.fnEditor.paramsSnapshot;

  // Auto-generate a unique param name from the config key.
  let paramName = configKey;
  let i = 2;
  while (params.some(p => p.key === paramName)) paramName = `${configKey}${i++}`;

  // Create the new param.
  params.push({key: paramName, type: fieldType, required: true, default: null, hint: ''});

  // Mark the field as a param reference on this node.
  if (!node._paramRefs) node._paramRefs = {};
  node._paramRefs[configKey] = paramName;
  // Seed the config with an empty value (the real value comes from the call site).
  node.config[configKey] = emptyForType(fieldType);

  renderParamPanel();

  // Open the params panel (if closed) and focus the new param's name input so
  // the user can rename it immediately.
  if (!ve.fnEditor.paramsOpen) fnEditorToggleParams();
  else renderFnEditorParams();
  const rows = document.querySelectorAll('.ve-fn-param-row .ve-fn-param-name');
  rows[rows.length - 1]?.focus();
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
  if (type === 'list')      return [];
  if (type === 'rule_list') return [];
  if (type === 'int')       return 0;
  if (type === 'bool')      return false;
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

  // Helper: stable function name for a mini-pipeline slot.
  const miniPipelineFnName = (parentId, kind, idx) =>
    `_mini_${kind}_${parentId.replace(/[^a-zA-Z0-9]/g, '_')}_${idx}`;

  const sections = [];
  for (const g of graphs) {
    const lines = [];
    // Sort so every upstream variable is assigned before it is referenced.
    // Route port nodes are excluded from the sort but their IDs appear in the
    // upstreams of downstream nodes. Build a map so the sort can resolve a port
    // node ID to the parent route node ID and register the real dependency.
    const portToRoute = new Map(
      g.nodes.filter(n => n.isRoutePort && n.routeParentId)
             .map(n => [n.id, n.routeParentId])
    );
    const mainNodes = g.nodes.filter(n => !n.isSearchNode && !n.isListNode && !n.isRoutePort);
    // Temporarily rewrite port-node upstreams to route-node IDs for the sort,
    // then restore them afterward so the rest of serialisation is unaffected.
    const savedUpstreams = new Map();
    for (const n of mainNodes) {
      const remapped = (n.upstreams || []).map(uid => portToRoute.get(uid) ?? uid);
      if (remapped.some((uid, i) => uid !== (n.upstreams || [])[i])) {
        savedUpstreams.set(n.id, n.upstreams);
        n.upstreams = remapped;
      }
    }
    const ordered = topoSortNodes(mainNodes);
    for (const [id, ups] of savedUpstreams) {
      const n = mainNodes.find(x => x.id === id);
      if (n) n.upstreams = ups;
    }
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

      // Emit mini-pipeline helper functions for any list/search slots on this node.
      for (const [kind, ids] of [['list', n.listNodeIds || []], ['search', n.searchNodeIds || []]]) {
        ids.forEach((id, idx) => {
          const sub = g.nodes.find(x => x.id === id);
          if (!sub?.isMiniPipeline) return;
          const fnName  = miniPipelineFnName(n.id, kind, idx);
          const steps   = sub.miniPipelineSteps || [];
          const defLines = [`def ${fnName}():`];
          let prevVar = null;
          steps.forEach((step, si) => {
            const vn  = `_s${si}`;
            const kw  = Object.entries(step.config || {})
              .filter(([, v]) => v !== '' && v != null)
              .map(([k, v]) => `${k}=${valToStar(v)}`)
              .join(', ');
            if (si === 0) {
              defLines.push(`    ${vn} = input(${starLit(step.plugin)}${kw ? ', ' + kw : ''})`);
            } else {
              defLines.push(`    ${vn} = process(${starLit(step.plugin)}, upstream=${prevVar}${kw ? ', ' + kw : ''})`);
            }
            prevVar = vn;
          });
          if (prevVar) defLines.push(`    return ${prevVar}`);
          lines.push(defLines.join('\n'));
          lines.push('');
        });
      }

      if (n.plugin === 'route') {
        // route(upstream, port1="cond1", port2="cond2")
        const rules = n.config?.rules || [];
        const portParts = rules
          .filter(r => r.name && r.accept)
          .map(r => `${r.name}=${starLit(r.accept)}`);
        const parts = fromStr ? [fromStr, ...portParts] : portParts;
        lines.push(`${n.id} = route(${parts.join(', ')})`);
      } else if (n.isFunctionCall || ve.userFunctions[n.plugin]) {
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
          const searchItems = n.searchNodeIds.map((id, idx) => {
            const sn = g.nodes.find(x => x.id === id);
            if (!sn) return null;
            if (sn.isMiniPipeline) return `${miniPipelineFnName(n.id, 'search', idx)}()`;
            return viaNodeToStar(sn);
          }).filter(Boolean).join(', ');
          parts.push(`search=[${searchItems}]`);
        }
        if (n.listNodeIds?.length) {
          const listItems = n.listNodeIds.map((id, idx) => {
            const ln = g.nodes.find(x => x.id === id);
            if (!ln) return null;
            if (ln.isMiniPipeline) return `${miniPipelineFnName(n.id, 'list', idx)}()`;
            return viaNodeToStar(ln);
          }).filter(Boolean).join(', ');
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
function viaNodeToStar(node, nodeLookup) {
  const entries = [`${starLit('name')}: ${starLit(node.plugin)}`];
  for (const [k, val] of Object.entries(node.config || {})) {
    if (val === '' || val == null) continue;
    const pName = nodeLookup?.[k];
    entries.push(`${starLit(k)}: ${pName ?? valToStar(val)}`);
  }
  return `{${entries.join(', ')}}`;
}

function upstreamsStr(ups) {
  if (!ups?.length) return '';
  const resolved = ups.map(id => {
    const n = findNode(id);
    // Route-selector nodes are not assigned a variable — reference via the
    // parent route node's handles object: routeNodeId.portName
    if (n?.isRoutePort) return `${n.routeParentId}.${n.routePortName}`;
    return id;
  });
  return resolved.length === 1 ? resolved[0] : `merge(${resolved.join(', ')})`;
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

// ── parse call-site args from source text ─────────────────────────────────────
// The Go server never populates fc.args in FunctionCallRecord. We recover them
// by scanning the source text for the assignment line and parsing its kwargs.
function parseFunctionCallArgs(src, callKey, funcName) {
  const lines = src.split('\n');
  for (const line of lines) {
    const t = line.trim();
    // Match: callKey = funcName(...)
    const m = t.match(new RegExp('^' + callKey.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
      + '\\s*=\\s*' + funcName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\((.*)\\)\\s*$'));
    if (!m) continue;
    // All args are kwargs (no positional plugin-name arg), so parse every part.
    const result = {};
    for (const part of fnSplitArgs(m[1])) {
      const eq = part.indexOf('=');
      if (eq < 0) continue;
      const k = part.slice(0, eq).trim();
      if (k === 'upstream') continue; // graph edge, not a config arg
      result[k] = fnParseLiteral(part.slice(eq + 1).trim());
    }
    return result;
  }
  return {};
}

// ── model change → sync to text editor ───────────────────────────────────────

// syncRoutePorts keeps the route_selector sub-nodes in sync with the rules
// configured on a route node. Called on every model change so that adding or
// removing rules immediately adds/removes the corresponding port chips, letting
// the user drag from them to connect downstream processors.
// syncRoutePorts returns true if it added or removed any port nodes.
function syncRoutePorts(routeNode, g) {
  const rules  = routeNode.config?.rules || [];
  // Include all rules — use index-based fallback name for empty names so
  // each rule gets a port immediately on "Add rule".
  const wanted = rules.map((r, i) => r?.name || `_port_${i}`);
  let changed  = false;

  // Index existing port nodes by name.
  const byName = {};
  for (const portId of (routeNode.portNodeIds || [])) {
    const port = g.nodes.find(n => n.id === portId);
    if (port?.isRoutePort) byName[port.routePortName] = port;
  }

  // Remove ports whose rule was deleted.
  for (const name of Object.keys(byName)) {
    if (wanted.includes(name)) continue;
    const port = byName[name];
    g.nodes = g.nodes.filter(n => n.id !== port.id);
    routeNode.portNodeIds = (routeNode.portNodeIds || []).filter(id => id !== port.id);
    changed = true;
  }

  // Add ports for new rules; sync portAcceptExpr on existing ones.
  for (let wi = 0; wi < wanted.length; wi++) {
    const name = wanted[wi];
    if (byName[name]) {
      // Port already exists — keep it but refresh the accept expression from the rule.
      byName[name].portAcceptExpr = rules[wi]?.accept || '';
      continue;
    }
    // Display label: use actual rule name if available, else placeholder.
    const displayName = rules[wi]?.name || '';
    const portId = `${routeNode.id}__port__${name}`;
    g.nodes.push({
      id: portId, plugin: 'route_selector', config: {},
      upstreams: [routeNode.id],
      searchNodeIds: [], listNodeIds: [], portNodeIds: [], comment: '',
      isRoutePort: true, routeParentId: routeNode.id,
      routePortName:  displayName || name,
      portAcceptExpr: rules[wi]?.accept || '',
      x: null, y: null,
    });
    if (!routeNode.portNodeIds) routeNode.portNodeIds = [];
    if (!routeNode.portNodeIds.includes(portId)) routeNode.portNodeIds.push(portId);
    changed = true;
  }
  return changed;
}

function onModelChange() {
  if (ve.syncing) return;
  // While editing a function body, ve.graphs contains only the function's
  // internal nodes — serialising it would overwrite the pipeline content in
  // the text editor.  Changes are flushed when the editor is saved/closed.
  if (ve.fnEditor.active) return;

  // Keep route port chips in sync with their node's rule list.
  // If any ports were added/removed, re-render so the layout runs immediately
  // (new ports start with x:null and need initLayout to position them).
  let portsChanged = false;
  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      if (n.plugin === 'route') portsChanged = syncRoutePorts(n, g) || portsChanged;
    }
  }
  if (portsChanged) {
    veRender();
    // Also sync the text editor after the new layout.
    const textEl = document.getElementById('config-editor');
    if (textEl) { textEl.value = dagToStarlark(); syncHighlight(); }
    return;
  }

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
    const data = await r.json();
    // graph_order lists pipeline names in the order their pipeline(…) calls
    // appear in the text config. Without it the dashboard and visual editor
    // would render alphabetically (Go's json encoder sorts map keys) while
    // the text editor stays in source order — making the three surfaces
    // disagree. Fall back to insertion order of data.graphs for safety.
    const graphsMap   = data.graphs || {};
    const orderedKeys = Array.isArray(data.graph_order) && data.graph_order.length
      ? data.graph_order.filter(k => k in graphsMap)
      : Object.keys(graphsMap);
    const entries = orderedKeys.map(k => [k, graphsMap[k]]);

    // Merge user functions into the plugin registry so the palette can show them.
    // Also extract each function's source text from the config content so
    // dagToStarlark can re-emit function definitions verbatim.
    ve.userFunctions = {};
    for (const fd of (data.functions || [])) {
      ve.userFunctions[fd.name] = fd;
      fd._sourceText = extractFunctionSource(content, fd.name);
      fd.comment = parseFunctionComment(fd._sourceText);
    }
    // Second pass: rewrite _sourceText for any function whose body still
    // carries a deprecated config shape. The first pass must populate every
    // fd._sourceText first, since parseFunctionBodyNodes reads it.
    for (const fd of (data.functions || [])) {
      const migrated = fnRegenerateSourceForMigration(fd.name);
      if (migrated) fd._sourceText = migrated;
    }

    ve.syncing = true;
    if (!entries.length) {
      ve.graphs         = [];       // no stub — user must click "+ Add pipeline"
      ve.selectedNodeId = null;
      ve.activeGraph    = 0;
      ve.syncing = false;
      ve_textDirty = false;
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
          fields: n.fields || {certain: [], reachable: []},
          autoMigrated: n.auto_migrated || '',
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
          if (s.steps) {
            const chainName = s.steps.map(st => st.plugin).join('→');
            nodes.push({
              id, plugin: chainName, config: {},
              upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
              isSearchNode: true, searchParentId: raw.id,
              isMiniPipeline: true, miniPipelineSteps: s.steps,
              x: s.x ?? null, y: s.y ?? null,
            });
          } else {
            nodes.push({
              id, plugin: s.plugin, config: s.config || {},
              upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
              isSearchNode: true, searchParentId: raw.id,
              x: s.x ?? null, y: s.y ?? null,
            });
          }
        }
        for (let li = 0; li < (raw.list || []).length; li++) {
          const l  = raw.list[li];
          const id = `${raw.id}__list__${li}`;
          if (!nodes[nodeIdx].listNodeIds) nodes[nodeIdx].listNodeIds = [];
          nodes[nodeIdx].listNodeIds.push(id);
          if (l.steps) {
            // Mini-pipeline: collapsed into a single representative node.
            const chainName = l.steps.map(s => s.plugin).join('→');
            nodes.push({
              id, plugin: chainName, config: {},
              upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
              isListNode: true, listParentId: raw.id,
              isMiniPipeline: true, miniPipelineSteps: l.steps,
              x: l.x ?? null, y: l.y ?? null,
            });
          } else {
            nodes.push({
              id, plugin: l.plugin, config: l.config || {},
              upstreams: [], searchNodeIds: [], listNodeIds: [], comment: '',
              isListNode: true, listParentId: raw.id,
              x: l.x ?? null, y: l.y ?? null,
            });
          }
        }
      }

      // Pass 2b: wire route_selector nodes as port sub-nodes on their route parent.
      for (const raw of rawNodes) {
        if (!raw.is_route_port) continue;
        const parentIdx = nodes.findIndex(n => n.id === raw.route_parent_id);
        if (parentIdx < 0) continue;
        if (!nodes[parentIdx].portNodeIds) nodes[parentIdx].portNodeIds = [];
        const existing = nodes.find(n => n.id === raw.id);
        if (existing) {
          // Already added as a regular node — convert it in place.
          existing.isRoutePort    = true;
          existing.routeParentId  = raw.route_parent_id;
          existing.routePortName  = raw.route_port_name;
          existing.portAcceptExpr = raw.port_accept_expr || '';
          nodes[parentIdx].portNodeIds.push(raw.id);
        } else {
          nodes[parentIdx].portNodeIds.push(raw.id);
          nodes.push({
            id: raw.id, plugin: raw.plugin, config: raw.config || {},
            upstreams: raw.upstreams || [],
            searchNodeIds: [], listNodeIds: [], portNodeIds: [], comment: '',
            isRoutePort: true, routeParentId: raw.route_parent_id,
            routePortName:  raw.route_port_name,
            portAcceptExpr: raw.port_accept_expr || '',
            x: raw.x ?? null, y: raw.y ?? null,
          });
        }
      }

      // Third pass: insert synthetic function-call nodes.
      // Pre-build a map so function→function entry upstreams resolve correctly:
      // when function B's entry node has upstream=<return_node of A>, replace
      // it with A's call_key rather than the internal variable name.
      const returnToCallKey2 = {};
      for (const fc of (graph.function_calls || [])) returnToCallKey2[fc.return_node_id] = fc.call_key;

      // Index ALL raw nodes (including internal) so computeFnCallOutputFields
      // can look up the return node's server-provided field sets.
      const rawById = Object.fromEntries(rawNodes.map(n => [n.id, n]));

      for (const fc of (graph.function_calls || [])) {
        // Find the entry nodes: internal nodes whose upstreams are all external.
        const internalSet = new Set(fc.internal_node_ids);
        const entryUpstreams = [];
        for (const n of rawNodes) {
          if (!internalSet.has(n.id)) continue;
          for (const up of (n.upstreams || [])) {
            if (!internalSet.has(up)) entryUpstreams.push(returnToCallKey2[up] ?? up);
          }
        }
        // Remap return_node_id → call_key in all already-added nodes.
        for (const n of nodes) {
          n.upstreams = n.upstreams.map(u => u === fc.return_node_id ? fc.call_key : u);
        }
        // fc.args is always empty from the server — recover call-site kwargs from source.
        const recoveredArgs2 = (fc.args && Object.keys(fc.args).length)
          ? fc.args : parseFunctionCallArgs(content, fc.call_key, fc.func);
        // Surface auto-migration when any internal node was injected by a
        // config-load migration. The function-call card is the only thing the
        // user sees in the visual editor (internal nodes are collapsed), so
        // the badge has to bubble up here.
        let fnAutoMigrated = '';
        for (const nid of fc.internal_node_ids) {
          const raw = rawById[nid];
          if (raw && raw.auto_migrated) {
            fnAutoMigrated = raw.auto_migrated;
            break;
          }
        }
        nodes.push({
          id:               fc.call_key,
          plugin:           fc.func,
          config:           recoveredArgs2,
          upstreams:        entryUpstreams,
          searchNodeIds:    [],
          listNodeIds:      [],
          comment:          '',
          isFunctionCall:   true,
          funcCallKey:      fc.call_key,
          internalNodeIds:  fc.internal_node_ids,
          returnNodeId:     fc.return_node_id,
          outputFields:     computeFnCallOutputFields(fc, rawById),
          autoMigrated:     fnAutoMigrated,
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
    ve_textDirty = false;
    veDebugLog('textToVisualSync:parsedFromServer', snapshotGraphPositions(ve.graphs));
    // Place all pipelines in order: stored relative positions for those that
    // have a layout comment, auto-layout for those that don't.
    initLayout();
    zoomToFitHorizontal(); // zoom out so all pipelines fit within the viewport width
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

let _veErrorTimer = null;

// veShowError displays an inline error message in the active toolbar note span
// (ve-sync-note in pipeline mode, ve-fn-note in function-editor mode) and
// auto-clears it after 6 seconds.
function veShowError(msg) {
  const id = ve.fnEditor?.active ? 've-fn-note' : 've-sync-note';
  const el = document.getElementById(id);
  if (!el) return;
  clearTimeout(_veErrorTimer);
  el.textContent = '✗ ' + msg;
  _veErrorTimer = setTimeout(() => { el.textContent = ''; }, 6000);
}

// ── canvas node tooltip ───────────────────────────────────────────────────────

// nodeTooltipText returns the hover tooltip string for a canvas node:
// the node's own comment takes priority; for function-call nodes the fallback
// is the function definition's comment then its description; for regular plugin
// nodes the fallback is the plugin's description.
function nodeTooltipText(n, meta) {
  if (n.comment?.trim()) return n.comment.trim();
  if (n.isFunctionCall) {
    const fd = ve.userFunctions[n.plugin];
    return fd?.comment?.trim() || fd?.description || '';
  }
  return meta?.description || '';
}

let _veTooltipTimer = null;

function veTooltipShow(text, e) {
  clearTimeout(_veTooltipTimer);
  _veTooltipTimer = setTimeout(() => {
    let tip = document.getElementById('ve-node-tooltip');
    if (!tip) {
      tip = document.createElement('div');
      tip.id = 've-node-tooltip';
      document.body.appendChild(tip);
    }
    tip.textContent = text;
    tip.style.display = 'block';
    veTooltipMove(e);
  }, 600);
}

function veTooltipMove(e) {
  const tip = document.getElementById('ve-node-tooltip');
  if (!tip || tip.style.display === 'none') return;
  tip.style.left = (e.clientX + 14) + 'px';
  tip.style.top  = (e.clientY + 18) + 'px';
}

function veTooltipHide() {
  clearTimeout(_veTooltipTimer);
  const tip = document.getElementById('ve-node-tooltip');
  if (tip) tip.style.display = 'none';
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

// ── edge field tooltip ────────────────────────────────────────────────────────

let _edgeTooltipEl = null;

function _ensureEdgeTooltip() {
  if (_edgeTooltipEl) return _edgeTooltipEl;
  _edgeTooltipEl = document.createElement('div');
  _edgeTooltipEl.id = 've-edge-tooltip';
  _edgeTooltipEl.className = 've-edge-tooltip';
  _edgeTooltipEl.style.display = 'none';
  document.body.appendChild(_edgeTooltipEl);
  return _edgeTooltipEl;
}

// Show a tooltip with the fields flowing through an edge on hover.
// fromNodeId is the source node; the edge carries that node's OUTPUT fields
// (its own produces + everything from its upstream chain).
function showEdgeFieldTooltip(event, fromNodeId) {
  // Find the source node within its own graph only.
  const gi         = findNodeGraph(fromNodeId);
  const graphNodes = gi >= 0 ? (ve.graphs[gi]?.nodes || []) : [];
  const fromNode   = graphNodes.find(n => n.id === fromNodeId);
  if (!fromNode) return;

  // Compute the OUTPUT fields of the source node by collecting its entire
  // upstream chain plus its own produces/condition-narrowing.
  const certain  = new Set();
  const reachable = new Set();
  collectOutputFields(fromNode, certain, reachable, new Set(), graphNodes);

  const certArr   = [...certain].sort();
  const reachOnly = [...reachable].filter(f => !certain.has(f)).sort();
  if (!certArr.length && !reachOnly.length) return;

  const label  = pluginMeta(fromNode.plugin)?.role === 'source'
    ? fromNode.plugin
    : fromNodeId;
  const tt = _ensureEdgeTooltip();
  const certStr  = certArr.map(f  => `<span class="ve-f-tag ve-f-certain">${esc(f)}</span>`).join(' ');
  const reachStr = reachOnly.map(f => `<span class="ve-f-tag ve-f-reachable">${esc(f)}</span>`).join(' ');
  tt.innerHTML = `
    <div class="ve-tt-title">Fields on this edge (out of <em>${esc(label)}</em>)</div>
    ${certStr  ? `<div class="ve-tt-row"><span class="ve-f-label">✓ certain:</span> ${certStr}</div>`   : ''}
    ${reachStr ? `<div class="ve-tt-row"><span class="ve-f-label">◐ reachable:</span> ${reachStr}</div>` : ''}`;
  tt.style.display = 'block';
  _positionEdgeTooltip(event);
}

function _positionEdgeTooltip(event) {
  const tt = _edgeTooltipEl;
  if (!tt) return;
  const x = event.clientX + 12;
  const y = event.clientY + 12;
  tt.style.left = Math.min(x, window.innerWidth  - tt.offsetWidth  - 8) + 'px';
  tt.style.top  = Math.min(y, window.innerHeight - tt.offsetHeight - 8) + 'px';
}

function hideEdgeFieldTooltip() {
  if (_edgeTooltipEl) _edgeTooltipEl.style.display = 'none';
}

// ── palette collapse ──────────────────────────────────────────────────────────

const VE_PALETTE_KEY = 'pipeliner.paletteCollapsed';

function veTogglePalette() {
  const layout = document.getElementById('ve-layout');
  if (!layout) return;
  const collapsed = layout.classList.toggle('palette-collapsed');
  localStorage.setItem(VE_PALETTE_KEY, collapsed ? '1' : '0');
  // Update collapse button icon
  const btn = document.getElementById('ve-palette-collapse-btn');
  if (btn) btn.textContent = collapsed ? '»' : '«';
  // Re-fit and re-render so edge paths update to new canvas width
  setTimeout(() => { fitVisualEditor(); updateCanvasSize(); renderEdges(); }, 0);
}

function veInitPaletteState() {
  if (localStorage.getItem(VE_PALETTE_KEY) === '1') {
    const layout = document.getElementById('ve-layout');
    if (layout) layout.classList.add('palette-collapsed');
    const btn = document.getElementById('ve-palette-collapse-btn');
    if (btn) btn.textContent = '»';
  }
}

// ── floating param panel ──────────────────────────────────────────────────────

// Restore last saved panel position/size from localStorage.
const VE_PANEL_POS_KEY = 'pipeliner.paramPanelPos'; // legacy key (ignored)

// Show/hide the floating panel.  Called from renderParamPanel() whenever the
// selected node changes.
function veShowParamPanel() {
  const panel = document.getElementById('ve-param-panel');
  if (!panel) return;
  // Restore saved position + size.
  try {
    const s = localStorage.getItem(VE_PANEL_DIM_KEY);
    if (s) {
      const dim = JSON.parse(s);
      panel.style.top    = dim.top    + 'px';
      panel.style.left   = dim.left   + 'px';
      panel.style.right  = '';
      if (dim.height) panel.style.height = dim.height + 'px';
    }
  } catch(_) {}
  panel.classList.add('visible');
  // After the panel is rendered with real dimensions, nudge it away from the
  // selected node if they overlap.
  requestAnimationFrame(veAvoidNodeOverlap);
}

// Nudge the floating panel just enough to stop it overlapping the selected
// node.  Uses the smallest-distance move in any of the four directions so the
// panel doesn't jump dramatically — it only moves as far as needed.
function veAvoidNodeOverlap() {
  if (!ve.selectedNodeId) return;
  const panel  = document.getElementById('ve-param-panel');
  const nodeEl = document.querySelector(`.ve-node[data-id="${ve.selectedNodeId}"]`);
  if (!panel || !nodeEl) return;

  const pr = panel.getBoundingClientRect();
  const nr = nodeEl.getBoundingClientRect();

  // No overlap — nothing to do.
  if (pr.right <= nr.left || pr.left >= nr.right ||
      pr.bottom <= nr.top || pr.top >= nr.bottom) return;

  const M = 12; // clearance gap
  // Four candidate moves (direction, resulting panel edge position, distance needed)
  const candidates = [
    {left: nr.right  + M,                top: pr.top,            dist: nr.right  + M - pr.left},   // push right
    {left: nr.left   - pr.width - M,     top: pr.top,            dist: pr.right  - (nr.left - M)}, // push left
    {left: pr.left,                       top: nr.bottom + M,     dist: nr.bottom + M - pr.top},    // push down
    {left: pr.left,                       top: nr.top - pr.height - M, dist: pr.bottom - (nr.top - M)}, // push up
  ];
  candidates.sort((a, b) => a.dist - b.dist);

  for (const c of candidates) {
    // Accept the move only if it keeps the panel on-screen.
    if (c.left >= 0 && c.left + pr.width  <= window.innerWidth &&
        c.top  >= 0 && c.top  + pr.height <= window.innerHeight) {
      panel.style.left  = c.left + 'px';
      panel.style.right = '';
      panel.style.top   = c.top  + 'px';
      _savePanelDim(c.top, c.left, panel.offsetHeight);
      return;
    }
  }
  // Fallback: if no clean slot exists (very small viewport), just push to right edge.
  const fallbackLeft = Math.max(0, Math.min(window.innerWidth - pr.width - 8, nr.right + M));
  panel.style.left  = fallbackLeft + 'px';
  panel.style.right = '';
  _savePanelDim(pr.top, fallbackLeft, panel.offsetHeight);
}

function veHideParamPanel() {
  const panel = document.getElementById('ve-param-panel');
  if (!panel) return;
  panel.classList.remove('visible');
  // Clear the selected node so clicking the canvas feels natural.
  if (ve.selectedNodeId) {
    const prev = ve.selectedNodeId;
    ve.selectedNodeId = null;
    clearMultiSelect();
    if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
    renderEdges();
  }
}

// ── param panel drag ──────────────────────────────────────────────────────────
//
// The panel is position:fixed — all coordinates are VIEWPORT space.
// getBoundingClientRect() on a fixed element returns viewport coords that
// map 1:1 to style.left/top, so there is no parent-offset mismatch.
// We intentionally keep left+top throughout (no left→right conversion)
// to avoid any subpixel snap on drop.

const VE_PANEL_DIM_KEY = 'pipeliner.paramPanelDim'; // stores {top,left,height}
let _panelDrag   = null; // {startX, startY, startLeft, startTop}
let _panelResize = null; // {startY, startTop, startH, edge:'top'|'bottom'}

function veInitPanelDrag() {
  const header = document.getElementById('ve-param-header');
  const panel  = document.getElementById('ve-param-panel');
  if (!header || !panel) return;

  header.addEventListener('pointerdown', e => {
    if (e.target.closest('button, input, select, textarea, a')) return;
    e.preventDefault();
    panel.classList.add('dragging');

    // For position:fixed, getBoundingClientRect() == viewport coords == style values.
    const rect = panel.getBoundingClientRect();
    panel.style.left  = rect.left + 'px';
    panel.style.right = '';
    panel.style.top   = rect.top  + 'px';

    _panelDrag = {
      startX:    e.clientX,
      startY:    e.clientY,
      startLeft: rect.left,
      startTop:  rect.top,
    };
    document.addEventListener('pointermove', _onPanelDragMove);
    document.addEventListener('pointerup',   _onPanelDragEnd, {once: true});
  });

  // Wire resize handles.
  _wireResizeHandle('ve-param-resize-top',    'top');
  _wireResizeHandle('ve-param-resize-bottom', 'bottom');
}

function _wireResizeHandle(id, edge) {
  const handle = document.getElementById(id);
  const panel  = document.getElementById('ve-param-panel');
  if (!handle || !panel) return;
  handle.addEventListener('pointerdown', e => {
    e.preventDefault();
    e.stopPropagation();
    panel.classList.add('resizing');
    const rect = panel.getBoundingClientRect();
    panel.style.height = rect.height + 'px';
    panel.style.maxHeight = 'none'; // override CSS max-height while resizing
    _panelResize = {startY: e.clientY, startTop: rect.top, startH: rect.height, edge};
    document.addEventListener('pointermove', _onPanelResizeMove);
    document.addEventListener('pointerup',   _onPanelResizeEnd, {once: true});
  });
}

function _onPanelDragMove(e) {
  if (!_panelDrag) return;
  const panel = document.getElementById('ve-param-panel');
  if (!panel) return;
  const dx    = e.clientX - _panelDrag.startX;
  const dy    = e.clientY - _panelDrag.startY;
  const GRIP  = 48; // min pixels of panel that must stay on-screen
  const newLeft = Math.max(-(panel.offsetWidth - GRIP),
                  Math.min(window.innerWidth   - GRIP, _panelDrag.startLeft + dx));
  const newTop  = Math.max(0,
                  Math.min(window.innerHeight  - GRIP, _panelDrag.startTop  + dy));
  panel.style.left = newLeft + 'px';
  panel.style.top  = newTop  + 'px';
}

function _onPanelDragEnd() {
  document.removeEventListener('pointermove', _onPanelDragMove);
  const panel = document.getElementById('ve-param-panel');
  if (!panel) { _panelDrag = null; return; }
  panel.classList.remove('dragging');
  // Keep left+top — no left→right conversion to avoid any subpixel snap.
  const rect = panel.getBoundingClientRect();
  _savePanelDim(rect.top, rect.left, panel.offsetHeight);
  _panelDrag = null;
}

// ── param panel resize ────────────────────────────────────────────────────────

const MIN_PANEL_H = 120;

function _onPanelResizeMove(e) {
  if (!_panelResize) return;
  const panel = document.getElementById('ve-param-panel');
  if (!panel) return;
  const dy = e.clientY - _panelResize.startY;

  if (_panelResize.edge === 'bottom') {
    const newH = Math.max(MIN_PANEL_H,
                 Math.min(window.innerHeight - _panelResize.startTop - 8,
                          _panelResize.startH + dy));
    panel.style.height = newH + 'px';
  } else {
    // top edge: move top boundary, keeping the bottom fixed
    const newTop = Math.max(0,
                   Math.min(_panelResize.startTop + _panelResize.startH - MIN_PANEL_H,
                            _panelResize.startTop + dy));
    const newH   = _panelResize.startH - (newTop - _panelResize.startTop);
    panel.style.top    = newTop + 'px';
    panel.style.height = newH   + 'px';
  }
}

function _onPanelResizeEnd() {
  document.removeEventListener('pointermove', _onPanelResizeMove);
  const panel = document.getElementById('ve-param-panel');
  if (!panel) { _panelResize = null; return; }
  panel.classList.remove('resizing');
  panel.style.maxHeight = ''; // restore CSS max-height
  const rect = panel.getBoundingClientRect();
  _savePanelDim(rect.top, rect.left, panel.offsetHeight);
  _panelResize = null;
}

function _savePanelDim(top, left, height) {
  localStorage.setItem(VE_PANEL_DIM_KEY, JSON.stringify({top, left, height}));
}

// ── utility ───────────────────────────────────────────────────────────────────
