// ── DAG visual pipeline editor ────────────────────────────────────────────────
//
// Supports only DAG-style pipelines (input / process / merge / output / pipeline).
// Serialises to and from Starlark DAG syntax and keeps the text editor in sync.

const NODE_W = 200;  // node card width (px)
const NODE_H = 80;   // fallback node height for edge midpoints

const ve = {
  plugins:  [],       // [{name, role, description, schema, produces, requires}]
  syncing:  false,
  // All loaded pipelines. ve.model is a live alias for ve.graphs[ve.activeGraph].
  graphs:   [{name: 'my-pipeline', schedule: '', nodes: []}],
  nextId:   0,
  activeGraph:    0,
  selectedNodeId: null,
  dragSrc: null,      // {type:'palette', plugin:''}
  get model() { return this.graphs[this.activeGraph] || this.graphs[0]; },
};

let ve_canvasInited  = false;
let ve_zoom          = 1.0;
let ve_dragging      = null;   // truthy while a node is being moved
let ve_connecting    = null;   // {srcId, curX, curY} while drawing a live edge
let ve_viaConnecting  = null;   // {discoverNodeId, curX, curY} while drawing a via edge
let ve_fromConnecting = null;   // {parentNodeId, curX, curY} while drawing a from edge
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

// Disconnect a via-connected node from its parent discover node.
function disconnectVia(discoverNodeId, viaNodeId) {
  const disc = findNode(discoverNodeId);
  const vn   = findNode(viaNodeId);
  if (disc) disc.viaNodeIds = (disc.viaNodeIds || []).filter(id => id !== viaNodeId);
  if (vn)  { vn.isViaNode = false; delete vn.viaParentId; }
  veRender();
  onModelChange();
}

function disconnectFrom(parentNodeId, fromNodeId) {
  const parent = findNode(parentNodeId);
  const fn     = findNode(fromNodeId);
  if (parent) parent.fromNodeIds = (parent.fromNodeIds || []).filter(id => id !== fromNodeId);
  if (fn)  { fn.isFromNode = false; delete fn.fromParentId; }
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
      const searchBadge = p.is_search_plugin ? ' <span class="ve-chip-via-badge">via</span>'
                        : p.is_from_plugin  ? ' <span class="ve-chip-from-badge">from</span>' : '';
      const extraCls = p.is_search_plugin ? ' ve-chip-search' : p.is_from_plugin ? ' ve-chip-from' : '';
      const extraTip = p.is_search_plugin ? '\n(drag onto a discover node\'s via port to use as a search backend)'
                     : p.is_from_plugin   ? '\n(drag onto a series/movies node\'s from port as a list source)' : '';
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
  const id   = genId(pluginName);
  const {x, y} = newNodePos(g);
  g.nodes.push({id, plugin: pluginName, config: {}, upstreams: [], x, y, comment: '', viaNodeIds: [], fromNodeIds: []});
  ve.selectedNodeId = id;
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
  for (const g of ve.graphs) {
    const idx = g.nodes.findIndex(n => n.id === id);
    if (idx < 0) continue;
    const [removed] = g.nodes.splice(idx, 1);
    // Clean up upstreams and via references.
    for (const n of g.nodes) {
      n.upstreams     = (n.upstreams   || []).filter(u => u !== id);
      n.viaNodeIds    = (n.viaNodeIds  || []).filter(u => u !== id);
      n.fromNodeIds   = (n.fromNodeIds || []).filter(u => u !== id);
    }
    // If this was a via/from node, remove its parent connection.
    if (removed.viaParentId) {
      const parent = g.nodes.find(n => n.id === removed.viaParentId);
      if (parent) parent.viaNodeIds = (parent.viaNodeIds || []).filter(u => u !== id);
    }
    if (removed.fromParentId) {
      const parent = g.nodes.find(n => n.id === removed.fromParentId);
      if (parent) parent.fromNodeIds = (parent.fromNodeIds || []).filter(u => u !== id);
    }
    break;
  }
  if (ve.selectedNodeId === id) ve.selectedNodeId = null;
  veRender();
  onModelChange();
}

// selectNode: toggle selection WITHOUT rebuilding DOM (keeps drag div ref valid).
function selectNode(id) {
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

// ── canvas ─────────────────────────────────────────────────────────────────────

function veRender() { renderCanvas(); renderParamPanel(); syncPaletteState(); }

function renderCanvas() {
  // Empty-hint only shows when there are zero pipelines.
  const hint = document.getElementById('ve-empty-hint');
  if (hint) hint.style.display = ve.graphs.length === 0 ? '' : 'none';

  renderPipelineRegions(); // drawn first so they sit behind nodes
  renderGraphNodes();
  renderPipelineLabels();
  renderEdges();
  updateCanvasSize();
}

// ── graph node rendering ───────────────────────────────────────────────────────

function renderGraphNodes() {
  const canvas = document.getElementById('ve-graph-canvas');
  if (!canvas) return;
  canvas.querySelectorAll('.ve-node').forEach(el => el.remove());

  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      const meta    = pluginMeta(n.plugin) || {role: 'processor'};
      const role    = meta.role;
      const sel     = n.id === ve.selectedNodeId;
      const warns   = fieldWarnings(n);
      const preview = configPreview(n.config);
      const isVia   = !!n.isViaNode;
      const isFrom  = !!n.isFromNode;
      // Sub-connected nodes show a badge in place of the role badge.
      const badgeHtml = isVia  ? '<span class="ve-node-via-badge">via</span>'
                      : isFrom ? '<span class="ve-node-from-badge">from</span>'
                      : `<span class="ve-node-role-badge ve-role-${role}">${role}</span>`;

      const div = document.createElement('div');
      div.className = `ve-node${sel ? ' selected' : ''}${isVia ? ' ve-node-via' : ''}${isFrom ? ' ve-node-from' : ''}`;
      div.dataset.role     = role;
      div.dataset.id       = n.id;
      div.dataset.isSearch = meta.is_search_plugin  ? 'true' : 'false';
      div.dataset.isFrom   = meta.is_from_plugin    ? 'true' : 'false';
      div.dataset.isVia    = isVia  ? 'true' : 'false';
      div.dataset.isFNode  = isFrom ? 'true' : 'false';
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
        (!isVia && !isFrom) ? `<div class="ve-node-out-port${role === 'sink' ? ' ve-node-chain-port' : ''}" title="${role === 'sink' ? 'Drag to chain to another output node' : 'Drag to connect'}"></div>` : '',
        // Input port indicator: shown on valid drop-targets while dragging an output port.
        (role !== 'source' && !isVia && !isFrom) ? '<div class="ve-node-in-port"></div>' : '',
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
        if (e.target.closest('.ve-node-remove') || e.target.closest('.ve-node-out-port')   ||
            e.target.closest('.ve-node-via-port') || e.target.closest('.ve-node-from-port') ||
            e.target.closest('.ve-node-comment-btn')) return;
        e.preventDefault();
        e.stopPropagation();
        selectNode(n.id);
        startNodeDrag(e, n);
      });

      // Receive regular upstream= drop (not allowed on source / sub-nodes).
      div.addEventListener('pointerup', () => {
        if (ve_connecting && ve_connecting.srcId !== n.id && role !== 'source' && !isVia && !isFrom) finishConnect(n.id);
        // Receive via-port drop.
        if (ve_viaConnecting && ve_viaConnecting.discoverNodeId !== n.id && meta.is_search_plugin && !isVia && !isFrom) finishViaConnect(n.id);
        // Receive from-port drop.
        if (ve_fromConnecting && ve_fromConnecting.parentNodeId !== n.id && meta.is_from_plugin && !isVia && !isFrom) finishFromConnect(n.id);
      });

      const outPort = div.querySelector('.ve-node-out-port');
      if (outPort) {
        outPort.addEventListener('pointerdown', e => {
          e.stopPropagation();
          e.preventDefault();
          startConnect(e, n.id);
        });
      }

      // Via-port (bottom circle): drag FROM here to a search-plugin node.
      if (meta.accepts_via) {
        const viaPort = document.createElement('div');
        viaPort.className = 've-node-via-port';
        viaPort.title = 'Drag to a search-plugin node to add it as a via backend';
        viaPort.textContent = 'via';
        viaPort.addEventListener('pointerdown', e => {
          e.stopPropagation(); e.preventDefault();
          startViaConnect(e, n.id);
        });
        div.appendChild(viaPort);
      }

      if (meta.accepts_from) {
        const fromPort = document.createElement('div');
        fromPort.className = 've-node-from-port';
        fromPort.title = 'Drag from here to a list-source node — or drop a list-source node\'s output arrow here';
        fromPort.textContent = 'from';

        // Initiate a from-connect drag (series → list-source).
        fromPort.addEventListener('pointerdown', e => {
          e.stopPropagation(); e.preventDefault();
          startFromConnect(e, n.id);
        });

        div.appendChild(fromPort);
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
      `<button class="ve-pl-delete" tabindex="-1" title="Delete pipeline and all its nodes">×</button>`,
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
      const nodeBot = (n.y ?? 0) + NODE_H + (n.viaNodeIds?.length ? 100 : 24);
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

  // Via-edges: dashed lines from discover's via-port (bottom centre) to each via-connected node.
  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      for (const viaId of (n.viaNodeIds || [])) {
        const vn = g.nodes.find(x => x.id === viaId);
        if (!vn) continue;
        const x1 = (n.x ?? 0) + NODE_W / 2;
        const y1 = nodeBottomY(n.id, n.y);       // bottom of discover (via-port)
        const x2 = (vn.x ?? 0) + NODE_W / 2;
        const y2 = (vn.y ?? 0);                  // TOP border of via node (not mid)
        const sel = ve.selectedNodeId === n.id || ve.selectedNodeId === viaId;
        const mId = sel ? '#arrow-via-sel' : '#arrow-via';
        const d   = `M${x1},${y1} C${x1},${y1+40} ${x2},${y2-40} ${x2},${y2}`;
        vis += `<path d="${d}" class="ve-via-edge${sel ? ' selected' : ''}"` +
               ` marker-end="url(${mId})" data-src="${n.id}" data-dst="${viaId}" data-via="true"/>`;
        hit += `<path d="${d}" class="ve-edge-hit"` +
               ` data-src="${n.id}" data-dst="${viaId}" data-via="true"><title>Click to disconnect</title></path>`;
      }
    }
  }

  // Live cursor line while dragging from a via-port.
  if (ve_viaConnecting) {
    const disc = findNode(ve_viaConnecting.discoverNodeId);
    if (disc) {
      const x1 = (disc.x ?? 0) + NODE_W / 2;
      const y1 = nodeBottomY(disc.id, disc.y);
      const x2 = ve_viaConnecting.curX, y2 = ve_viaConnecting.curY;
      vis += `<path d="M${x1},${y1} C${x1},${y1+40} ${x2},${y2-40} ${x2},${y2}" class="ve-via-edge connecting"/>`;
    }
  }

  // From-edges: teal dashed lines flowing downward FROM the from-node's bottom
  // TO the series/movies node's top (from-port).  from-nodes sit above the parent.
  for (const g of ve.graphs) {
    for (const n of g.nodes) {
      for (const fnId of (n.fromNodeIds || [])) {
        const fn = g.nodes.find(x => x.id === fnId);
        if (!fn) continue;
        // From-node is above; its bottom connects to the parent's top (from-port).
        // The curve always approaches the from-port from directly above so the
        // arrowhead visually lands on the teal port, not the left-side input.
        const x1 = (fn.x ?? 0) + NODE_W / 2;
        const y1 = nodeBottomY(fnId, fn.y);
        const x2 = (n.x  ?? 0) + NODE_W / 2;
        const y2 = (n.y  ?? 0);                // top of parent = from-port
        const dy  = Math.max(50, Math.abs(y2 - y1) * 0.5);
        // cp2 uses x2 so the final approach is straight down into the from port.
        const sel  = ve.selectedNodeId === n.id || ve.selectedNodeId === fnId;
        const mEnd = sel ? '#arrow-from-sel' : '#arrow-from';
        vis += `<path d="M${x1},${y1} C${x1},${y1+dy} ${x2},${y2-dy} ${x2},${y2}"` +
               ` class="ve-from-edge${sel ? ' selected' : ''}" marker-end="url(${mEnd})"` +
               ` data-src="${n.id}" data-dst="${fnId}" data-from="true"/>`;
        hit += `<path d="M${x1},${y1} C${x1},${y1+dy} ${x2},${y2-dy} ${x2},${y2}"` +
               ` class="ve-edge-hit" data-src="${n.id}" data-dst="${fnId}" data-from="true"><title>Click to disconnect</title></path>`;
      }
    }
  }

  // Live cursor line while dragging from a from-port.
  // Drawn FROM the from-port (top of parent) UPWARD TO the cursor — same
  // convention as startConnect/startViaConnect so the fixed anchor is obvious.
  if (ve_fromConnecting) {
    const par = findNode(ve_fromConnecting.parentNodeId);
    if (par) {
      const x1 = (par.x ?? 0) + NODE_W / 2;
      const y1 = (par.y ?? 0);  // top of parent = from-port
      const x2 = ve_fromConnecting.curX, y2 = ve_fromConnecting.curY;
      const dy  = Math.max(40, Math.abs(y1 - y2) * 0.4);
      vis += `<path d="M${x1},${y1} C${x1},${y1-dy} ${x2},${y2+dy} ${x2},${y2}" class="ve-from-edge connecting"/>`;
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
      `<marker id="arrow-from" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#0d9373"/>` +
      `</marker>` +
      `<marker id="arrow-from-sel" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#2dd4b8"/>` +
      `</marker>` +
      `<marker id="arrow-via" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
        `<path d="M0,1 L0,7 L7,4 z" fill="#9a6ad8"/>` +
      `</marker>` +
      `<marker id="arrow-via-sel" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">` +
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

// ── auto layout ────────────────────────────────────────────────────────────────

function autoLayout() {
  const COL_W = 260, ROW_H = 120, PAD_X = 50;
  let globalY = 40;

  for (const g of ve.graphs) {
    if (!g.nodes.length) {
      g._labelY  = globalY;
      g._regionY = globalY - 8;
      g._regionH = 80;
      globalY += 80;
      continue;
    }

    g._labelY = globalY;
    const startY = globalY + 36; // space for pipeline label

    // ── 1. Topological depth (via/from sub-nodes are laid out separately) ───
    const isSub = n => n.isViaNode || n.isFromNode;
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
      if (isSub(n)) continue; // positioned after, below their parent
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

    // ── 3. Barycenter pass (forward): pull each non-root node to the avg Y
    //       of its upstream nodes, then re-space the column to avoid overlap.
    for (const d of depths) {
      if (d === 0) continue;
      const ids = byDepth[d];

      // Compute target Y (barycenter of upstreams) for each node in column d.
      const target = {};
      for (const id of ids) {
        const n = g.nodes.find(n => n.id === id);
        if (!n?.upstreams.length) { target[id] = null; continue; }
        const upYs = n.upstreams.map(uid => {
          const up = g.nodes.find(x => x.id === uid);
          return up?.y ?? startY;
        });
        target[id] = upYs.reduce((a, b) => a + b, 0) / upYs.length;
      }

      // Sort by target Y so we assign positions in top-to-bottom order.
      const sorted = [...ids].sort((a, b) => (target[a] ?? startY) - (target[b] ?? startY));

      // Place each node: use its target Y but push down to avoid overlap.
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
    for (const n of g.nodes) topY = Math.min(topY, n.y);
    if (topY > startY) {
      const shift = topY - startY;
      for (const n of g.nodes) n.y -= shift;
    }

    let maxY = startY + ROW_H;
    for (const n of g.nodes) {
      if (!isSub(n)) maxY = Math.max(maxY, (n.y ?? 0) + ROW_H);
    }

    // Position via-connected nodes in a row below their parent discover node.
    for (const n of g.nodes) {
      if (!n.viaNodeIds?.length) continue;
      const VIA_GAP = 18;
      const totalW  = n.viaNodeIds.length * NODE_W + (n.viaNodeIds.length - 1) * VIA_GAP;
      const startVX = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
      n.viaNodeIds.forEach((id, i) => {
        const vn = g.nodes.find(x => x.id === id);
        if (vn) {
          vn.x = Math.max(0, startVX + i * (NODE_W + VIA_GAP));
          vn.y = (n.y ?? 0) + NODE_H + 70;
          maxY = Math.max(maxY, vn.y + NODE_H + 20);
        }
      });
    }

    // Position from-connected nodes in a row ABOVE their parent (they are inputs).
    // Clamp so they don't go above the pipeline's node area (startY).
    for (const n of g.nodes) {
      if (!n.fromNodeIds?.length) continue;
      const FROM_GAP = 18;
      const totalW   = n.fromNodeIds.length * NODE_W + (n.fromNodeIds.length - 1) * FROM_GAP;
      const startFX  = (n.x ?? 0) + NODE_W / 2 - totalW / 2;
      n.fromNodeIds.forEach((id, i) => {
        const fn = g.nodes.find(x => x.id === id);
        if (fn) {
          fn.x = Math.max(0, startFX + i * (NODE_W + FROM_GAP));
          fn.y = Math.max(startY + 4, (n.y ?? 0) - NODE_H - 65);
          // from-nodes sit within the existing Y band, so maxY is unaffected.
        }
      });
    }

    // Store region bounds for background rendering and separator dragging.
    g._regionY = g._labelY - 8;
    g._regionH = maxY - g._regionY + 16;

    globalY = maxY + 60; // gap between pipelines
  }
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

  // Deselect on empty-canvas click.
  canvas.addEventListener('pointerdown', e => {
    if (!e.target.closest('.ve-node') && !e.target.closest('.ve-pipeline-label')) {
      const prev = ve.selectedNodeId;
      ve.selectedNodeId = null;
      if (prev) document.querySelector(`.ve-node[data-id="${prev}"]`)?.classList.remove('selected');
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
      if (hit.dataset.via === 'true') {
        disconnectVia(hit.dataset.src, hit.dataset.dst);
      } else if (hit.dataset.from === 'true') {
        disconnectFrom(hit.dataset.src, hit.dataset.dst);
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
    const id = genId(ve.dragSrc.plugin);
    g.nodes.push({id, plugin: ve.dragSrc.plugin, config: {}, upstreams: [], x, y, comment: '', viaNodeIds: [], fromNodeIds: []});
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
        const nodeBot = (nd.y ?? 0) + NODE_H + (nd.viaNodeIds?.length ? 100 : 24);
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
  // Sources never accept incoming edges; from-list nodes can't also be regular
  // upstreams; and adding the edge must not create a cycle in the DAG.
  // Additionally, sink nodes may only connect to other sink nodes (chaining).
  if (src && tgt && tgtRole !== 'source' && !src.isFromNode &&
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

// ── via-port connect interaction ──────────────────────────────────────────────
// Drag from a discover node's via-port to a search-plugin node on the canvas.

function startViaConnect(e, discoverNodeId) {
  const canvas = document.getElementById('ve-graph-canvas');
  const rect   = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
  ve_viaConnecting = {
    discoverNodeId,
    curX: (e.clientX - rect.left) / ve_zoom,
    curY: (e.clientY - rect.top)  / ve_zoom,
  };
  canvas?.classList.add('is-viaconnecting');

  function onMove(ev) {
    const r = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
    ve_viaConnecting.curX = (ev.clientX - r.left) / ve_zoom;
    ve_viaConnecting.curY = (ev.clientY - r.top)  / ve_zoom;
    renderEdges();
  }
  function cleanup() {
    document.removeEventListener('pointermove', onMove);
    canvas?.classList.remove('is-viaconnecting');
  }
  function onUp(ev) {
    cleanup();
    if (!ve_viaConnecting) return;
    const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
    const tid = el?.dataset?.id;
    if (tid && tid !== discoverNodeId && el.dataset.isSearch === 'true' && el.dataset.isVia !== 'true') {
      finishViaConnect(tid);
    } else { ve_viaConnecting = null; renderEdges(); }
  }
  function onCancel() { cleanup(); ve_viaConnecting = null; renderEdges(); }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp,    {once: true});
  document.addEventListener('pointercancel', onCancel, {once: true});
}

function finishViaConnect(targetNodeId) {
  const disc   = findNode(ve_viaConnecting?.discoverNodeId);
  const target = findNode(targetNodeId);
  if (disc && target && pluginMeta(target.plugin)?.is_search_plugin && !target.isViaNode) {
    if (!disc.viaNodeIds) disc.viaNodeIds = [];
    if (!disc.viaNodeIds.includes(targetNodeId)) {
      disc.viaNodeIds.push(targetNodeId);
      target.isViaNode    = true;
      target.viaParentId  = disc.id;
      onModelChange();
    }
  }
  ve_viaConnecting = null;
  veRender();
}

// ── from-port connect interaction ─────────────────────────────────────────────
// Drag from a series/movies node's "from" port to a list-source plugin node.

function startFromConnect(e, parentNodeId) {
  const canvas = document.getElementById('ve-graph-canvas');
  const rect   = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
  ve_fromConnecting = {
    parentNodeId,
    curX: (e.clientX - rect.left) / ve_zoom,
    curY: (e.clientY - rect.top)  / ve_zoom,
  };
  canvas?.classList.add('is-fromconnecting');

  function onMove(ev) {
    const r = canvas?.getBoundingClientRect() ?? {left: 0, top: 0};
    ve_fromConnecting.curX = (ev.clientX - r.left) / ve_zoom;
    ve_fromConnecting.curY = (ev.clientY - r.top)  / ve_zoom;
    renderEdges();
  }
  function cleanup() {
    document.removeEventListener('pointermove', onMove);
    canvas?.classList.remove('is-fromconnecting');
  }
  function onUp(ev) {
    cleanup();
    if (!ve_fromConnecting) return;
    const el  = document.elementFromPoint(ev.clientX, ev.clientY)?.closest('.ve-node[data-id]');
    const tid = el?.dataset?.id;
    if (tid && tid !== parentNodeId && el.dataset.isFrom === 'true' && el.dataset.isFNode !== 'true') finishFromConnect(tid);
    else { ve_fromConnecting = null; renderEdges(); }
  }
  function onCancel() { cleanup(); ve_fromConnecting = null; renderEdges(); }

  document.addEventListener('pointermove', onMove);
  document.addEventListener('pointerup',     onUp,    {once: true});
  document.addEventListener('pointercancel', onCancel, {once: true});
}

function finishFromConnect(targetNodeId) {
  const parent = findNode(ve_fromConnecting?.parentNodeId);
  const target = findNode(targetNodeId);
  // Also block if the target is already a regular upstream of the parent
  // (one connection type per node).
  if (parent && target && pluginMeta(target.plugin)?.is_from_plugin &&
      !target.isFromNode && !(parent.upstreams || []).includes(targetNodeId)) {
    if (!parent.fromNodeIds) parent.fromNodeIds = [];
    if (!parent.fromNodeIds.includes(targetNodeId)) {
      parent.fromNodeIds.push(targetNodeId);
      target.isFromNode   = true;
      target.fromParentId = parent.id;
      onModelChange();
    }
  }
  ve_fromConnecting = null;
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
  const title  = document.getElementById('ve-param-title');
  const nameEl = document.getElementById('ve-param-name');
  const roleEl = document.getElementById('ve-param-phase');
  const body   = document.getElementById('ve-param-body');
  const footer = document.getElementById('ve-param-footer');

  const node = ve.selectedNodeId ? findNode(ve.selectedNodeId) : null;
  const gi   = node ? findNodeGraph(ve.selectedNodeId) : -1;
  const g    = gi >= 0 ? ve.graphs[gi] : null;

  if (!node || !g) {
    empty.style.display = ''; title.style.display = 'none';
    body.innerHTML = ''; footer.style.display = 'none';
    return;
  }

  const meta = pluginMeta(node.plugin) || {role: 'processor', schema: [], produces: [], requires: []};
  empty.style.display = 'none'; title.style.display = '';
  nameEl.textContent = node.plugin;
  roleEl.textContent = node.isViaNode ? 'via / search' : node.isFromNode ? 'from / list' : meta.role;

  if (node.isViaNode && node.viaParentId) {
    const parentName = findNode(node.viaParentId)?.plugin ?? node.viaParentId;
    footer.innerHTML = [
      `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">Via backend for <b>${esc(parentName)}</b></div>`,
      `<button class="ve-remove-btn" onclick="disconnectVia(${esc(JSON.stringify(node.viaParentId))},${esc(JSON.stringify(node.id))})">Disconnect from via</button>`,
    ].join('');
  } else if (node.isFromNode && node.fromParentId) {
    const parentName = findNode(node.fromParentId)?.plugin ?? node.fromParentId;
    footer.innerHTML = [
      `<div style="font-size:11px;color:var(--muted);margin-bottom:6px">List source for <b>${esc(parentName)}</b></div>`,
      `<button class="ve-remove-btn" onclick="disconnectFrom(${esc(JSON.stringify(node.fromParentId))},${esc(JSON.stringify(node.id))})">Disconnect from list</button>`,
    ].join('');
  } else {
    footer.innerHTML = `<button class="ve-remove-btn" onclick="ve.selectedNodeId && removeNode(ve.selectedNodeId)">Remove node</button>`;
  }
  footer.style.display = '';

  const html = [];

  // Via-connected nodes have no pipeline upstreams (they're search backends, not DAG nodes).
  if (meta.role !== 'source' && !node.isViaNode) {
    const others = g.nodes.filter(n => n.id !== node.id && !n.isViaNode && !n.isFromNode);
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

  // ── Via section (discover and similar AcceptsVia nodes) ────────────────────
  if (meta.accepts_via) {
    const searchNodes = g.nodes.filter(nd => pluginMeta(nd.plugin)?.is_search_plugin);
    html.push(`<div class="ve-field"><div class="ve-field-label">Via (search backends)</div>`);
    if (!searchNodes.length) {
      html.push(`<div style="color:var(--muted);font-size:12px;margin-top:4px">Add search-plugin nodes (jackett, rss_search…) to the canvas, then drag the <b>via</b> port to connect them</div>`);
    } else {
      for (const sn of searchNodes) {
        const checked = (node.viaNodeIds || []).includes(sn.id);
        html.push(`<label class="ve-upstream-row">
          <input type="checkbox" ${checked ? 'checked' : ''}
            onchange="toggleVia(${esc(JSON.stringify(node.id))},${esc(JSON.stringify(sn.id))},this.checked)">
          <code class="ve-upstream-id">${esc(sn.id)}</code>
          <span class="ve-node-via-badge" style="font-size:9px">via</span>
        </label>`);
      }
    }
    html.push('</div>');
  }

  // ── From section (series, movies and similar AcceptsFrom nodes) ─────────────
  if (meta.accepts_from) {
    const fromNodes = g.nodes.filter(nd => pluginMeta(nd.plugin)?.is_from_plugin);
    html.push(`<div class="ve-field"><div class="ve-field-label">From (list sources)</div>`);
    if (!fromNodes.length) {
      html.push(`<div style="color:var(--muted);font-size:12px;margin-top:4px">Add list-source nodes (tvdb_favorites, trakt_list…) to the canvas, then drag the <b>from</b> port to connect them</div>`);
    } else {
      for (const fn of fromNodes) {
        const checked = (node.fromNodeIds || []).includes(fn.id);
        html.push(`<label class="ve-upstream-row">
          <input type="checkbox" ${checked ? 'checked' : ''}
            onchange="toggleFrom(${esc(JSON.stringify(node.id))},${esc(JSON.stringify(fn.id))},this.checked)">
          <code class="ve-upstream-id">${esc(fn.id)}</code>
          <span class="ve-node-from-badge" style="font-size:9px">from</span>
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
  if (meta.schema?.length) {
    for (const f of meta.schema) {
      // Skip 'via' and 'from' fields — managed visually by the sections above.
      if ((f.key === 'via' && meta.accepts_via) || (f.key === 'from' && meta.accepts_from)) continue;
      html.push(renderField(f, node.config));
    }
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

function toggleVia(nodeId, viaId, checked) {
  const node   = findNode(nodeId);
  const target = findNode(viaId);
  if (!node || !target) return;
  if (checked) {
    if (!pluginMeta(target.plugin)?.is_search_plugin || target.isViaNode) { renderParamPanel(); return; }
    if (!node.viaNodeIds) node.viaNodeIds = [];
    if (!node.viaNodeIds.includes(viaId)) {
      node.viaNodeIds.push(viaId);
      target.isViaNode   = true;
      target.viaParentId = nodeId;
    }
  } else {
    node.viaNodeIds   = (node.viaNodeIds || []).filter(id => id !== viaId);
    target.isViaNode  = false;
    delete target.viaParentId;
  }
  renderEdges(); renderGraphNodes(); onModelChange();
}

function toggleFrom(nodeId, fromId, checked) {
  const node   = findNode(nodeId);
  const target = findNode(fromId);
  if (!node || !target) return;
  if (checked) {
    if (!pluginMeta(target.plugin)?.is_from_plugin || target.isFromNode) { renderParamPanel(); return; }
    if ((node.upstreams || []).includes(fromId)) { renderParamPanel(); return; } // already regular upstream
    if (!node.fromNodeIds) node.fromNodeIds = [];
    if (!node.fromNodeIds.includes(fromId)) {
      node.fromNodeIds.push(fromId);
      target.isFromNode   = true;
      target.fromParentId = nodeId;
    }
  } else {
    node.fromNodeIds    = (node.fromNodeIds || []).filter(id => id !== fromId);
    target.isFromNode   = false;
    delete target.fromParentId;
  }
  renderEdges(); renderGraphNodes(); onModelChange();
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

function dagToStarlark() {
  const graphs = ve.graphs.filter(g => g.name || g.nodes.length);
  if (!graphs.length) return '';

  const sections = [];
  for (const g of graphs) {
    const lines = [];
    // Sort so every upstream variable is assigned before it is referenced.
    const ordered = topoSortNodes(g.nodes.filter(n => !n.isViaNode && !n.isFromNode));
    for (const n of ordered) {
      // (via nodes already excluded from ordered)

      const role    = pluginMeta(n.plugin)?.role || 'processor';
      const cfgKw   = configToKwargs(n.config);
      const fromStr = upstreamsStr(n.upstreams);

      // Emit user comment lines (# prefix) before the node definition.
      // Insert a blank line before the comment unless it is the very first output.
      if (n.comment?.trim()) {
        if (lines.length > 0) lines.push('');
        for (const cl of n.comment.trim().split('\n')) lines.push(`# ${cl}`);
      }

      if (role === 'source') {
        lines.push(`${n.id} = input(${[starLit(n.plugin), cfgKw].filter(Boolean).join(', ')})`);
      } else if (role === 'processor') {
        const parts = [starLit(n.plugin)];
        if (fromStr) parts.push(`upstream=${fromStr}`);
        if (n.viaNodeIds?.length) {
          const viaItems = n.viaNodeIds.map(id => g.nodes.find(x => x.id === id)).filter(Boolean).map(viaNodeToStar).join(', ');
          parts.push(`via=[${viaItems}]`);
        }
        if (n.fromNodeIds?.length) {
          const fromItems = n.fromNodeIds.map(id => g.nodes.find(x => x.id === id)).filter(Boolean).map(viaNodeToStar).join(', ');
          parts.push(`from=[${fromItems}]`);
        }
        if (cfgKw) parts.push(cfgKw);
        lines.push(`${n.id} = process(${parts.join(', ')})`);
      } else {
        const parts = [starLit(n.plugin)];
        if (fromStr) parts.push(`upstream=${fromStr}`);
        if (cfgKw)   parts.push(cfgKw);
        // If this sink has downstream sinks (chaining), assign the return value
        // so it can be referenced as upstream= by the chained output.
        const hasChainedDownstream = g.nodes.some(
          dn => !dn.isViaNode && !dn.isFromNode &&
                (dn.upstreams || []).includes(n.id) &&
                (pluginMeta(dn.plugin)?.role === 'sink')
        );
        if (hasChainedDownstream) {
          lines.push(`${n.id} = output(${parts.join(', ')})`);
        } else {
          lines.push(`output(${parts.join(', ')})`);
        }
      }
    }

    // Emit pipeline comment, then layout metadata, then pipeline() call.
    // Insert a blank line before this block when node definitions precede it.
    if (g.comment?.trim()) {
      if (lines.length > 0) lines.push('');
      for (const cl of g.comment.trim().split('\n')) lines.push(`# ${cl}`);
    }
    // Collect all node positions for the layout comment.
    const layout = {};
    for (const n of g.nodes) {
      if (n.x != null && n.y != null) layout[n.id] = [Math.round(n.x), Math.round(n.y)];
    }
    if (Object.keys(layout).length) lines.push(`# pipeliner:layout ${JSON.stringify(layout)}`);

    const schedArg = g.schedule ? `, schedule=${starLit(g.schedule)}` : '';
    lines.push(`pipeline(${starLit(g.name)}${schedArg})`);
    sections.push(lines.join('\n'));
  }
  return sections.join('\n\n') + '\n';
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
    const data    = await r.json();
    const entries = Object.entries(data.graphs || {});

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
      // First pass: build regular nodes, loading comments.
      const nodes = rawNodes.map(n => ({
        id: n.id, plugin: n.plugin, config: n.config || {}, upstreams: n.upstreams || [],
        viaNodeIds: [], comment: n.comment || '',
      }));
      // Second pass: convert via/from items to regular nodes with flags.
      for (let ni = 0; ni < rawNodes.length; ni++) {
        const raw = rawNodes[ni];
        for (let vi = 0; vi < (raw.via || []).length; vi++) {
          const v  = raw.via[vi];
          const id = `${raw.id}__via__${vi}`;
          nodes[ni].viaNodeIds.push(id);
          nodes.push({
            id, plugin: v.plugin, config: v.config || {},
            upstreams: [], viaNodeIds: [], fromNodeIds: [], comment: '',
            isViaNode: true, viaParentId: raw.id,
          });
        }
        for (let fi = 0; fi < (raw.from || []).length; fi++) {
          const f  = raw.from[fi];
          const id = `${raw.id}__from__${fi}`;
          nodes[ni].fromNodeIds.push(id);
          nodes.push({
            id, plugin: f.plugin, config: f.config || {},
            upstreams: [], viaNodeIds: [], fromNodeIds: [], comment: '',
            isFromNode: true, fromParentId: raw.id,
          });
        }
      }
      // Apply stored layout positions (if present) to all nodes.
      const layout = graph.layout || {};
      for (const n of nodes) {
        const pos = layout[n.id];
        if (pos) { n.x = pos[0]; n.y = pos[1]; }
      }
      return {name, schedule: graph.schedule || '', comment: graph.comment || '', nodes,
              _hasLayout: Object.keys(layout).length > 0};
    });
    ve.nextId = ve.graphs.flatMap(g => g.nodes).reduce((max, n) => {
      const m = n.id.match(/_(\d+)$/);
      return m ? Math.max(max, parseInt(m[1]) + 1) : max;
    }, 0);
    ve.activeGraph    = 0;
    ve.selectedNodeId = null;
    ve_panX = 0; ve_panY = 0; // reset pan so freshly laid-out nodes are visible
    ve.syncing = false;
    // Use stored positions if available; fall back to auto-layout for new nodes.
    const needsLayout = ve.graphs.some(g => !g._hasLayout);
    if (needsLayout) autoLayout();
    else computePipelineBoundsFromNodes();
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
    const nonVia = g.nodes.filter(n => !n.isViaNode && !n.isFromNode);
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
