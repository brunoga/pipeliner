/**
 * Tests for the fix/visual-editor-critical batch:
 *   1. edgeFieldSets — data path behind the edge hover tooltip.
 *   2. veShortcutsActive — window-level shortcuts gated to Config tab + visual view.
 *   3. Undo stack parking across the function-editor boundary.
 *   4. ve_parseFailed — onModelChange must not overwrite unsaved text edits
 *      after a failed text→visual parse.
 *   5. textToVisualSync — a successful parse must NOT rewrite the text buffer.
 *   6. addTag/removeTag — sub-node widgets edit the target node, not the parent.
 *   7. Incomplete route ports — kept in the model, omitted from serialisation
 *      with a warning, highlighted in the rule row.
 *   8. deleteSelection — multi-selection delete under a single undo snapshot.
 *   9. addPipeline — first free "pipeline-N" suffix.
 *  10. clampPanelPos — param panel restore clamped into the viewport.
 *  11. startNodeDrag — undo snapshot only once the drag threshold is crossed.
 *  12. deleteCondRule2 — _forcedRaw flags re-indexed after rule removal.
 */

import { describe, it, expect, beforeAll, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

let docElementsMock;   // id → element-like mock; tests populate per-case
let docListeners;      // event name → [handlers] captured from document.addEventListener
let fetchImpl;         // per-test fetch behaviour

let ve, edgeFieldSets, veShortcutsActive, deleteSelection, addPipeline,
    clampPanelPos, startNodeDrag, deleteCondRule2, addTag, removeTag,
    renderField, renderRouteRuleRow, dagToStarlark, onModelChange,
    textToVisualSync, pushUndo, undo, undoStackEnterFnEditor,
    undoStackExitFnEditor, _forcedRaw, _builderRawKey, findNode,
    _getUndoStack, _setUndoStack, _setCurrentView, _getParseFailed,
    _setParseFailed, _getDroppedPortWarnings;

// Stubs for globals defined in other UI files (esc from dashboard.js,
// syncHighlight from highlight.js).
const helperStubs = `
function esc(s) {
  return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function syncHighlight() {}
const CSS = { escape: s => String(s) };
`;

beforeAll(() => {
  const mod = new Function(
    'exports', 'document', 'fetch',
    helperStubs + src + `
exports.ve                    = ve;
exports.edgeFieldSets         = edgeFieldSets;
exports.veShortcutsActive     = veShortcutsActive;
exports.deleteSelection       = deleteSelection;
exports.addPipeline           = addPipeline;
exports.clampPanelPos         = clampPanelPos;
exports.startNodeDrag         = startNodeDrag;
exports.deleteCondRule2       = deleteCondRule2;
exports.addTag                = addTag;
exports.removeTag             = removeTag;
exports.renderField           = renderField;
exports.renderRouteRuleRow    = renderRouteRuleRow;
exports.dagToStarlark         = dagToStarlark;
exports.onModelChange         = onModelChange;
exports.textToVisualSync      = textToVisualSync;
exports.pushUndo              = pushUndo;
exports.undo                  = undo;
exports.undoStackEnterFnEditor = undoStackEnterFnEditor;
exports.undoStackExitFnEditor  = undoStackExitFnEditor;
exports._forcedRaw            = _forcedRaw;
exports._builderRawKey        = _builderRawKey;
exports.findNode              = findNode;
exports._getUndoStack         = () => ve_undoStack;
exports._setUndoStack         = s => { ve_undoStack = s; };
exports._setCurrentView       = v => { currentView = v; };
exports._getParseFailed       = () => ve_parseFailed;
exports._setParseFailed       = v => { ve_parseFailed = v; };
exports._getDroppedPortWarnings = () => ve_droppedPortWarnings;
`
  );
  const exports = {};
  docElementsMock = {};
  docListeners = {};
  const noopDoc = new Proxy({}, {
    get: (_, prop) => {
      if (prop === 'querySelectorAll') return () => [];
      if (prop === 'querySelector')    return () => null;
      if (prop === 'getElementById')   return id => docElementsMock[id] ?? null;
      if (prop === 'addEventListener') return (ev, fn) => {
        (docListeners[ev] = docListeners[ev] || []).push(fn);
      };
      if (prop === 'removeEventListener') return (ev, fn) => {
        docListeners[ev] = (docListeners[ev] || []).filter(f => f !== fn);
      };
      return () => null;
    },
  });
  const fetchDispatch = (...args) => fetchImpl(...args);
  mod(exports, noopDoc, fetchDispatch);
  ({ ve, edgeFieldSets, veShortcutsActive, deleteSelection, addPipeline,
     clampPanelPos, startNodeDrag, deleteCondRule2, addTag, removeTag,
     renderField, renderRouteRuleRow, dagToStarlark, onModelChange,
     textToVisualSync, pushUndo, undo, undoStackEnterFnEditor,
     undoStackExitFnEditor, _forcedRaw, _builderRawKey, findNode,
     _getUndoStack, _setUndoStack, _setCurrentView, _getParseFailed,
     _setParseFailed, _getDroppedPortWarnings } = exports);
});

const PLUGINS = [
  { name: 'rss',      role: 'source',    produces: ['title', 'source'], may_produce: [] },
  { name: 'seen',     role: 'processor', produces: [], may_produce: [] },
  { name: 'metainfo', role: 'processor', produces: [], may_produce: ['video_year'] },
  { name: 'discover', role: 'processor', accepts_search: true,
    schema: [{ key: 'quality', type: 'string' }] },
  { name: 'jackett',  role: 'source',    is_search_plugin: true,
    schema: [{ key: 'indexers', type: 'list' }, { key: 'notes', type: 'string', multiline: true }] },
  { name: 'route',    role: 'processor' },
  { name: 'route_selector', role: 'processor' },
  { name: 'condition', role: 'processor' },
  { name: 'print',    role: 'sink' },
];

function node(id, plugin, upstreams = [], extra = {}) {
  return { id, plugin, config: {}, upstreams,
           searchNodeIds: [], listNodeIds: [], portNodeIds: [], comment: '', ...extra };
}

function setup(nodes, name = 'p1') {
  ve.graphs         = [{ name, schedule: '', comment: '', nodes,
                         _labelY: 10, _regionY: 2, _regionH: 200 }];
  ve.activeGraph    = 0;
  ve.plugins        = PLUGINS;
  ve.userFunctions  = {};
  ve.selectedNodeId = null;
  ve.selectedNodeIds.clear();
  ve.fnEditor       = { active: false };
}

beforeEach(() => {
  for (const k of Object.keys(docElementsMock)) delete docElementsMock[k];
  for (const k of Object.keys(docListeners))    delete docListeners[k];
  fetchImpl = () => Promise.reject(new Error('fetch not stubbed'));
  _setUndoStack([]);
  _setParseFailed(false);
  _forcedRaw.clear();
  setup([]);
});

// ── 1. edge tooltip data path ────────────────────────────────────────────────

describe('edgeFieldSets', () => {
  it('returns certain + reachable fields for a node with an upstream chain', () => {
    setup([node('rss_0', 'rss'), node('meta_1', 'metainfo', ['rss_0'])]);
    const sets = edgeFieldSets('meta_1');
    expect(sets).not.toBeNull();
    expect(sets.certain).toContain('title');
    expect(sets.certain).toContain('source');
    expect(sets.reachable).toContain('video_year');
    // Disjoint: reachable excludes certain fields.
    for (const f of sets.reachable) expect(sets.certain).not.toContain(f);
  });

  it('returns the source plugin produces for a source node', () => {
    setup([node('rss_0', 'rss')]);
    const sets = edgeFieldSets('rss_0');
    expect(sets.certain).toEqual(expect.arrayContaining(['source', 'title']));
  });

  it('returns null for an unknown node id', () => {
    setup([node('rss_0', 'rss')]);
    expect(edgeFieldSets('nope')).toBeNull();
  });
});

// ── 2. shortcut gating ───────────────────────────────────────────────────────

describe('veShortcutsActive', () => {
  it('is true only when the Config tab is visible and the visual view is active', () => {
    docElementsMock['tab-config'] = { style: { display: '' } };
    _setCurrentView('visual');
    expect(veShortcutsActive()).toBe(true);
  });

  it('is false when the Config tab is hidden', () => {
    docElementsMock['tab-config'] = { style: { display: 'none' } };
    _setCurrentView('visual');
    expect(veShortcutsActive()).toBe(false);
  });

  it('is false in text view even with the Config tab visible', () => {
    docElementsMock['tab-config'] = { style: { display: '' } };
    _setCurrentView('text');
    expect(veShortcutsActive()).toBe(false);
    _setCurrentView('visual');
  });

  it('is false when the tab element does not exist', () => {
    _setCurrentView('visual');
    expect(veShortcutsActive()).toBe(false);
  });
});

// ── 3. undo stack × function editor boundary ─────────────────────────────────

describe('undo stack across the function editor boundary', () => {
  it('parks the pipeline stack on enter and restores it (dropping fn snapshots) on exit', () => {
    setup([node('rss_0', 'rss')]);
    pushUndo();
    pushUndo();
    expect(_getUndoStack()).toHaveLength(2);
    const outerTop = _getUndoStack()[1];

    ve.fnEditor = { active: true, funcName: 'f' };
    undoStackEnterFnEditor();
    expect(_getUndoStack()).toHaveLength(0);

    // Snapshots pushed inside the editor are fn-scoped.
    pushUndo();
    pushUndo();
    pushUndo();
    expect(_getUndoStack()).toHaveLength(3);

    undoStackExitFnEditor();
    ve.fnEditor = { active: false };
    expect(_getUndoStack()).toHaveLength(2);
    expect(_getUndoStack()[1]).toBe(outerTop);
  });

  it('undo after exiting the fn editor restores the pipeline model, not the fn body', () => {
    setup([node('rss_0', 'rss')]);
    pushUndo();                       // snapshot of the real pipeline
    ve.graphs[0].name = 'renamed';

    // Simulate entering the fn editor: graphs swapped for the fn body.
    ve.fnEditor = { active: true, funcName: 'f',
                    savedGraphs: ve.graphs, savedActive: 0, savedNextId: 0 };
    undoStackEnterFnEditor();
    ve.graphs = [{ name: 'f', schedule: '', nodes: [node('seen_9', 'seen')] }];
    pushUndo();                       // fn-scoped snapshot (would poison the stack)

    // Simulate exit.
    undoStackExitFnEditor();
    ve.graphs   = ve.fnEditor.savedGraphs;
    ve.fnEditor = { active: false };

    undo();
    expect(ve.graphs[0].name).toBe('p1');            // outer snapshot restored
    expect(ve.graphs[0].nodes[0].id).toBe('rss_0');  // not the fn body graph
  });
});

// ── 4. parse-failed guard ────────────────────────────────────────────────────

describe('onModelChange with ve_parseFailed set', () => {
  it('does not overwrite the textarea and surfaces a sync-note warning', () => {
    setup([node('rss_0', 'rss')]);
    docElementsMock['config-editor'] = { value: 'USER EDITS WITH SYNTAX ERROR(' };
    docElementsMock['ve-sync-note']  = { textContent: '' };

    _setParseFailed(true);
    onModelChange();
    expect(docElementsMock['config-editor'].value).toBe('USER EDITS WITH SYNTAX ERROR(');
    expect(docElementsMock['ve-sync-note'].textContent).toMatch(/parse errors/i);
  });

  it('writes normally once the flag is cleared', () => {
    setup([node('rss_0', 'rss')]);
    docElementsMock['config-editor'] = { value: 'stale' };
    _setParseFailed(false);
    onModelChange();
    expect(docElementsMock['config-editor'].value).toContain('rss_0 = input("rss")');
  });
});

// ── 5. no text rewrite on successful text→visual sync ───────────────────────

describe('textToVisualSync', () => {
  it('does not rewrite the hand-authored text buffer on a successful parse', async () => {
    const authored = 'src = input("rss")\npipeline("p1")\n';
    docElementsMock['config-editor'] = { value: authored };
    docElementsMock['ve-sync-note']  = { textContent: '' };
    ve.plugins = PLUGINS;
    fetchImpl = async () => ({
      status: 200, ok: true,
      json: async () => ({
        graphs: { p1: { nodes: [
          { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [] },
        ], schedule: '' } },
        graph_order: ['p1'],
        functions: [],
      }),
    });

    await textToVisualSync();
    expect(docElementsMock['config-editor'].value).toBe(authored);
    expect(_getParseFailed()).toBe(false);
    expect(ve.graphs[0].nodes[0].id).toBe('rss_0'); // model still synced
  });

  it('sets ve_parseFailed on a 422 so later model changes cannot clobber the text', async () => {
    const broken = 'src = input(("rss"\npipeline("p1")\n';
    docElementsMock['config-editor'] = { value: broken };
    docElementsMock['ve-sync-note']  = { textContent: '' };
    fetchImpl = async () => ({
      status: 422, ok: false,
      json: async () => ({ error: 'syntax error at line 1' }),
    });

    await textToVisualSync();
    expect(_getParseFailed()).toBe(true);

    // A visual interaction now runs onModelChange — the broken text survives.
    setup([node('rss_0', 'rss')]);
    _setParseFailed(true); // setup() does not touch the flag, be explicit
    onModelChange();
    expect(docElementsMock['config-editor'].value).toBe(broken);
  });
});

// ── 6. sub-node tag / popup widgets target the right node ───────────────────

describe('sub-node widget targeting', () => {
  function discoverWithJackett() {
    const parent = node('discover_1', 'discover', [], { searchNodeIds: ['jackett_2'] });
    const sub    = node('jackett_2', 'jackett', [],
                        { isSearchNode: true, searchParentId: 'discover_1' });
    setup([node('rss_0', 'rss'), parent, sub]);
    ve.selectedNodeId = 'discover_1'; // parent is what's selected in the panel
    return { parent, sub };
  }

  it('addTag writes into the sub-node config, not the selected parent', () => {
    const { parent, sub } = discoverWithJackett();
    docElementsMock['vef-jackett_2-indexers'] = { value: 'nyaa', focus() {} };
    addTag('vef-jackett_2-indexers', 'indexers', 'jackett_2');
    expect(sub.config.indexers).toEqual(['nyaa']);
    expect(parent.config.indexers).toBeUndefined();
  });

  it('addTag falls back to the selected node when no id is given (legacy)', () => {
    const { parent } = discoverWithJackett();
    docElementsMock['vef-x'] = { value: 'item', focus() {} };
    addTag('vef-x', 'things');
    expect(parent.config.things).toEqual(['item']);
  });

  it('removeTag removes from the sub-node config, not the selected parent', () => {
    const { parent, sub } = discoverWithJackett();
    sub.config.indexers    = ['nyaa', 'rarbg'];
    parent.config.indexers = ['SHOULD-SURVIVE'];
    const list = { querySelectorAll: () => [tag] };
    const tag  = { closest: () => list, remove() {} };
    const btn  = { closest: () => tag };
    removeTag(btn, 'indexers', 'jackett_2');
    expect(sub.config.indexers).toEqual(['rarbg']);
    expect(parent.config.indexers).toEqual(['SHOULD-SURVIVE']);
  });

  it('renderField threads the field owner node id into tag and popup handlers', () => {
    const { sub } = discoverWithJackett();
    sub.config.indexers = ['nyaa']; // need one rendered tag for the removeTag handler
    const listHtml = renderField({ key: 'indexers', type: 'list' }, sub.config, sub);
    expect(listHtml).toContain("addTag('vef-jackett_2-indexers','indexers',&quot;jackett_2&quot;)");
    expect(listHtml).toContain("removeTag(this,'indexers',&quot;jackett_2&quot;)");

    sub.config.indexers = undefined;
    const mlHtml = renderField({ key: 'notes', type: 'string', multiline: true }, sub.config, sub);
    expect(mlHtml).toContain("openFieldPopup('notes','',&quot;jackett_2&quot;)");
  });
});

// ── 7. incomplete route ports ────────────────────────────────────────────────

describe('incomplete route ports', () => {
  function routeGraph(rules) {
    const r = node('route_1', 'route', ['rss_0']);
    r.config = { rules };
    setup([node('rss_0', 'rss'), r]);
    return r;
  }

  it('serialises only complete ports and records a warning for the rest', () => {
    routeGraph([
      { name: 'series', accept: "series_episode_id != ''" },
      { name: 'half',   accept: '' },
      { name: '',       accept: "x != ''" },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('series=');
    expect(out).not.toContain('half=');
    const warns = _getDroppedPortWarnings();
    expect(warns).toHaveLength(2);
    expect(warns[0]).toContain("route port 'half'");
    expect(warns[0]).toContain('not saved');
    expect(warns[1]).toContain('route port #3');
  });

  it('surfaces the omission in the sync note when the text is written', () => {
    routeGraph([{ name: 'half', accept: '' }]);
    docElementsMock['config-editor'] = { value: '' };
    docElementsMock['ve-sync-note']  = { textContent: '' };
    onModelChange();
    expect(docElementsMock['ve-sync-note'].textContent).toContain("route port 'half'");
  });

  it('records no warnings when every port is complete', () => {
    routeGraph([{ name: 'a', accept: "x != ''" }]);
    dagToStarlark();
    expect(_getDroppedPortWarnings()).toHaveLength(0);
  });

  it('highlights the incomplete rule row in the editor UI', () => {
    const r = routeGraph([{ name: 'half', accept: '' }]);
    r.fields = { certain: [], reachable: [] };
    ve.selectedNodeId = 'route_1'; // builder body resolves the selected node
    const html = renderRouteRuleRow({ name: 'half', accept: '' }, 0, r);
    expect(html).toContain('ve-rule-incomplete');
    expect(html).toContain('incomplete port');
    const ok = renderRouteRuleRow({ name: 'full', accept: "x != ''" }, 1, r);
    expect(ok).not.toContain('ve-rule-incomplete');
  });
});

// ── 8. multi-selection delete ────────────────────────────────────────────────

describe('deleteSelection', () => {
  it('deletes every multi-selected node under a single undo snapshot', () => {
    setup([node('rss_0', 'rss'), node('seen_1', 'seen', ['rss_0']),
           node('print_2', 'print', ['seen_1'])]);
    ve.selectedNodeIds.add('rss_0');
    ve.selectedNodeIds.add('seen_1');
    deleteSelection();
    expect(ve.graphs[0].nodes.map(n => n.id)).toEqual(['print_2']);
    expect(ve.graphs[0].nodes[0].upstreams).toEqual([]); // dangling upstream cleaned
    expect(_getUndoStack()).toHaveLength(1);

    undo();
    expect(ve.graphs[0].nodes).toHaveLength(3);
  });

  it('falls back to the single selected node when no multi-selection exists', () => {
    setup([node('rss_0', 'rss'), node('seen_1', 'seen', ['rss_0'])]);
    ve.selectedNodeId = 'seen_1';
    deleteSelection();
    expect(ve.graphs[0].nodes.map(n => n.id)).toEqual(['rss_0']);
    expect(ve.selectedNodeId).toBeNull();
  });

  it('is a no-op with no selection', () => {
    setup([node('rss_0', 'rss')]);
    deleteSelection();
    expect(ve.graphs[0].nodes).toHaveLength(1);
    expect(_getUndoStack()).toHaveLength(0);
  });
});

// ── 9. addPipeline name collisions ───────────────────────────────────────────

describe('addPipeline', () => {
  it('picks the first free pipeline-N suffix', () => {
    setup([]);
    ve.graphs = [
      { name: 'pipeline-1', schedule: '', nodes: [] },
      { name: 'pipeline-3', schedule: '', nodes: [] },
    ];
    addPipeline();
    expect(ve.graphs[2].name).toBe('pipeline-2');
    addPipeline();
    expect(ve.graphs[3].name).toBe('pipeline-4');
  });

  it('does not collide after add-delete-add', () => {
    setup([]);
    ve.graphs = [{ name: 'pipeline-2', schedule: '', nodes: [] }];
    addPipeline(); // length+1 would have produced pipeline-2 again
    const names = ve.graphs.map(g => g.name);
    expect(new Set(names).size).toBe(names.length);
    expect(names).toContain('pipeline-1');
  });
});

// ── 10. param panel clamp ────────────────────────────────────────────────────

describe('clampPanelPos', () => {
  it('keeps an on-screen position unchanged', () => {
    expect(clampPanelPos(100, 200, 340, 1280, 800)).toEqual({ top: 100, left: 200 });
  });

  it('clamps a position saved on a larger screen back into view', () => {
    const pos = clampPanelPos(2000, 3000, 340, 1280, 800);
    expect(pos.left).toBeLessThanOrEqual(1280 - 40);
    expect(pos.top).toBeLessThanOrEqual(800 - 40);
  });

  it('clamps negative positions so at least 40px stay visible', () => {
    const pos = clampPanelPos(-500, -900, 340, 1280, 800);
    expect(pos.top).toBe(0);              // header stays reachable
    expect(pos.left + 340).toBeGreaterThanOrEqual(40);
  });
});

// ── 11. drag undo threshold ──────────────────────────────────────────────────

describe('startNodeDrag undo snapshot', () => {
  function fireMove(x, y) {
    for (const fn of [...(docListeners['pointermove'] || [])]) fn({ clientX: x, clientY: y });
  }
  function fireUp() {
    for (const fn of [...(docListeners['pointerup'] || [])]) fn({});
    docListeners['pointerup'] = [];
    docListeners['pointermove'] = [];
  }

  it('does not push a snapshot for a click without movement', () => {
    setup([node('rss_0', 'rss', [], { x: 100, y: 100 })]);
    startNodeDrag({ clientX: 300, clientY: 300 }, findNode('rss_0'));
    fireUp();
    expect(_getUndoStack()).toHaveLength(0);
  });

  it('does not push a snapshot for sub-threshold jitter', () => {
    setup([node('rss_0', 'rss', [], { x: 100, y: 100 })]);
    startNodeDrag({ clientX: 300, clientY: 300 }, findNode('rss_0'));
    fireMove(302, 301); // < 5px
    fireUp();
    expect(_getUndoStack()).toHaveLength(0);
    expect(findNode('rss_0').x).toBe(100); // node did not move
  });

  it('pushes exactly one pre-drag snapshot once the threshold is crossed', () => {
    setup([node('rss_0', 'rss', [], { x: 100, y: 100 })]);
    startNodeDrag({ clientX: 300, clientY: 300 }, findNode('rss_0'));
    fireMove(330, 300);
    fireMove(360, 300);
    fireUp();
    expect(_getUndoStack()).toHaveLength(1);
    expect(findNode('rss_0').x).toBe(160); // moved by 60

    undo(); // restores the PRE-drag position
    expect(findNode('rss_0').x).toBe(100);
  });
});

// ── 12. deleteCondRule2 _forcedRaw re-indexing ───────────────────────────────

describe('deleteCondRule2', () => {
  function condNode(exprs) {
    const c = node('condition_1', 'condition', ['rss_0']);
    c.config = { rules: exprs.map(e => ({ accept: e })) };
    setup([node('rss_0', 'rss'), c]);
    ve.selectedNodeId = 'condition_1';
    return c;
  }

  it('shifts force-raw flags of later rules down by one index', () => {
    condNode(["a != ''", "b != ''", "c != ''"]);
    _forcedRaw.add(_builderRawKey('cond', 'condition_1', 1));
    _forcedRaw.add(_builderRawKey('cond', 'condition_1', 2));

    deleteCondRule2(0);

    expect(_forcedRaw.has(_builderRawKey('cond', 'condition_1', 0))).toBe(true);
    expect(_forcedRaw.has(_builderRawKey('cond', 'condition_1', 1))).toBe(true);
    expect(_forcedRaw.has(_builderRawKey('cond', 'condition_1', 2))).toBe(false);
  });

  it('drops the removed rule flag without touching earlier ones', () => {
    condNode(["a != ''", "b != ''", "c != ''"]);
    _forcedRaw.add(_builderRawKey('cond', 'condition_1', 0));
    _forcedRaw.add(_builderRawKey('cond', 'condition_1', 1));

    deleteCondRule2(1);

    expect(_forcedRaw.has(_builderRawKey('cond', 'condition_1', 0))).toBe(true);
    expect(_forcedRaw.has(_builderRawKey('cond', 'condition_1', 1))).toBe(false);
  });
});
