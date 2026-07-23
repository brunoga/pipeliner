/**
 * Tests for the fix/visual-editor-ux batch:
 *   1. veRestoreViewAfterSync — pan/zoom/active-graph/selection preserved
 *      across a text→visual re-parse (validate/save no longer reset the view).
 *   2. connectTargetError — shared validation for drag-connect and the param
 *      panel Upstreams checkboxes (cycle, sink chaining, cross-pipeline,
 *      duplicate edge), with human-readable reasons.
 *   3. toggleUpstream — rejects invalid connections with a sync-note reason.
 *   4. evalRequiresGroups / fieldWarnings — OR-group semantics (a group is
 *      satisfied when ANY member is certain).
 *   5. layoutGraph — route children are placed to the RIGHT of the route node
 *      (no more backwards edges from a left column).
 *   6. tidyActivePipeline — re-runs auto-layout for the active pipeline only.
 *   7. fnDeletionDropsUpstream / removeNode — deleting the last '_upstream'
 *      consumer in the function editor requires an explicit confirm.
 *   8. fnEditorStateSnapshot / fnEditorIsDirty — Back-button dirty tracking.
 *   9. configPreview route summary (named ports) lives in visual-editor.test.js.
 */

import { describe, it, expect, beforeAll, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

let docElementsMock; // id → element-like mock; tests populate per-case
let confirmImpl;     // per-test confirm() behaviour
let confirmCalls;    // messages passed to confirm()

let ve, veRestoreViewAfterSync, connectTargetError, toggleUpstream,
    evalRequiresGroups, requiresGroupsFor, fieldWarnings, layoutGraph,
    tidyActivePipeline, fnDeletionDropsUpstream, removeNode,
    fnEditorStateSnapshot, fnEditorIsDirty, findNode,
    _getPan, _setPan, _getZoom, _setZoom;

const helperStubs = `
function esc(s) {
  return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function syncHighlight() {}
const CSS = { escape: s => String(s) };
`;

beforeAll(() => {
  const mod = new Function(
    'exports', 'document', 'fetch', 'confirm',
    helperStubs + src + `
exports.ve                      = ve;
exports.veRestoreViewAfterSync  = veRestoreViewAfterSync;
exports.connectTargetError      = connectTargetError;
exports.toggleUpstream          = toggleUpstream;
exports.evalRequiresGroups      = evalRequiresGroups;
exports.requiresGroupsFor       = requiresGroupsFor;
exports.fieldWarnings           = fieldWarnings;
exports.layoutGraph             = layoutGraph;
exports.tidyActivePipeline      = tidyActivePipeline;
exports.fnDeletionDropsUpstream = fnDeletionDropsUpstream;
exports.removeNode              = removeNode;
exports.fnEditorStateSnapshot   = fnEditorStateSnapshot;
exports.fnEditorIsDirty         = fnEditorIsDirty;
exports.findNode                = findNode;
exports._getPan  = () => ({x: ve_panX, y: ve_panY});
exports._setPan  = (x, y) => { ve_panX = x; ve_panY = y; };
exports._getZoom = () => ve_zoom;
exports._setZoom = z => { ve_zoom = z; };
`
  );
  const exports = {};
  docElementsMock = {};
  const noopDoc = new Proxy({}, {
    get: (_, prop) => {
      if (prop === 'querySelectorAll') return () => [];
      if (prop === 'querySelector')    return () => null;
      if (prop === 'getElementById')   return id => docElementsMock[id] ?? null;
      if (prop === 'addEventListener' || prop === 'removeEventListener') return () => {};
      if (prop === 'createElement') return () => ({style: {}, classList: {add(){}, remove(){}, toggle(){}},
        addEventListener() {}, appendChild() {}, remove() {}, querySelector: () => null, querySelectorAll: () => []});
      return () => null;
    },
  });
  const confirmDispatch = msg => { confirmCalls.push(msg); return confirmImpl(msg); };
  mod(exports, noopDoc, () => Promise.reject(new Error('no fetch in tests')), confirmDispatch);
  ({ ve, veRestoreViewAfterSync, connectTargetError, toggleUpstream,
     evalRequiresGroups, requiresGroupsFor, fieldWarnings, layoutGraph,
     tidyActivePipeline, fnDeletionDropsUpstream, removeNode,
     fnEditorStateSnapshot, fnEditorIsDirty, findNode,
     _getPan, _setPan, _getZoom, _setZoom } = exports);
});

const PLUGINS = [
  { name: 'rss',   role: 'source',    produces: ['title', 'source'], may_produce: [], requires: [] },
  { name: 'seen',  role: 'processor', produces: [], may_produce: [], requires: [] },
  { name: 'meta',  role: 'processor', produces: [], may_produce: ['series_episode_id'], requires: [] },
  { name: 'dedup', role: 'processor', produces: [], may_produce: [],
    requires: ['media_type', 'series_episode_id', 'title'],
    requires_groups: [['media_type'], ['series_episode_id', 'title']] },
  { name: 'print', role: 'sink', produces: [], may_produce: [], requires: [] },
  { name: 'transmission', role: 'sink', produces: [], may_produce: [], requires: [] },
  { name: 'route', role: 'processor', produces: [], may_produce: [], requires: [] },
];

function mkNode(id, plugin, upstreams = [], extra = {}) {
  return { id, plugin, config: {}, upstreams, searchNodeIds: [], listNodeIds: [], comment: '', ...extra };
}

beforeEach(() => {
  ve.plugins        = JSON.parse(JSON.stringify(PLUGINS));
  ve.userFunctions  = {};
  ve.graphs         = [];
  ve.activeGraph    = 0;
  ve.selectedNodeId = null;
  ve.selectedNodeIds.clear();
  ve.syncing        = false;
  ve.fnEditor       = { active: false };
  confirmImpl  = () => true;
  confirmCalls = [];
  docElementsMock = {};
});

// ── 1. veRestoreViewAfterSync ────────────────────────────────────────────────

describe('veRestoreViewAfterSync', () => {
  beforeEach(() => {
    ve.graphs = [
      { name: 'alpha', schedule: '', nodes: [mkNode('a_0', 'rss')] },
      { name: 'beta',  schedule: '', nodes: [mkNode('b_0', 'rss'), mkNode('b_1', 'seen', ['b_0'])] },
    ];
  });

  it('preserves pan/zoom and re-activates the graph by name', () => {
    _setPan(-120, -45); _setZoom(0.75);
    const prev = { activeName: 'beta', selectedId: null, panX: -120, panY: -45, zoom: 0.75 };
    // Simulate the reset the parse path used to do:
    _setPan(0, 0); _setZoom(1.0); ve.activeGraph = 0;
    expect(veRestoreViewAfterSync(prev)).toBe(true);
    expect(ve.activeGraph).toBe(1);
    expect(_getPan()).toEqual({x: -120, y: -45});
    expect(_getZoom()).toBe(0.75);
  });

  it('restores the selected node when it still exists', () => {
    const prev = { activeName: 'beta', selectedId: 'b_1', panX: 0, panY: 0, zoom: 1 };
    expect(veRestoreViewAfterSync(prev)).toBe(true);
    expect(ve.selectedNodeId).toBe('b_1');
  });

  it('drops the selection (but keeps the view) when the node disappeared', () => {
    _setPan(-10, -20);
    const prev = { activeName: 'beta', selectedId: 'gone_9', panX: -10, panY: -20, zoom: 1 };
    expect(veRestoreViewAfterSync(prev)).toBe(true);
    expect(ve.selectedNodeId).toBe(null);
    expect(_getPan()).toEqual({x: -10, y: -20});
  });

  it('falls back to a full reset when the active pipeline disappeared', () => {
    _setPan(-300, -80);
    ve.selectedNodeId = 'a_0';
    const prev = { activeName: 'zeta', selectedId: 'a_0', panX: -300, panY: -80, zoom: 0.5 };
    expect(veRestoreViewAfterSync(prev)).toBe(false);
    expect(ve.activeGraph).toBe(0);
    expect(ve.selectedNodeId).toBe(null);
    expect(_getPan()).toEqual({x: 0, y: 0});
  });

  it('falls back to a full reset on a first load (no previous state)', () => {
    expect(veRestoreViewAfterSync({activeName: null, selectedId: null, panX: 0, panY: 0, zoom: 1})).toBe(false);
  });
});

// ── 2. connectTargetError ────────────────────────────────────────────────────

describe('connectTargetError', () => {
  beforeEach(() => {
    ve.graphs = [
      { name: 'p1', schedule: '', nodes: [
        mkNode('src_0',  'rss'),
        mkNode('seen_1', 'seen', ['src_0']),
        mkNode('meta_2', 'meta', ['seen_1']),
        mkNode('out_3',  'print', ['meta_2']),
        mkNode('out_4',  'transmission', ['meta_2']),
      ]},
      { name: 'p2', schedule: '', nodes: [mkNode('other_0', 'seen')] },
    ];
  });

  it('accepts a valid processor → processor connection', () => {
    expect(connectTargetError('src_0', 'meta_2')).toBe(null);
  });

  it('rejects a connection into a source node', () => {
    expect(connectTargetError('seen_1', 'src_0')).toMatch(/source node cannot receive/);
  });

  it('rejects a self connection', () => {
    expect(connectTargetError('seen_1', 'seen_1')).toMatch(/itself/);
  });

  it('rejects a duplicate edge', () => {
    expect(connectTargetError('src_0', 'seen_1')).toMatch(/already connected/);
  });

  it('rejects a cycle with an explicit reason', () => {
    expect(connectTargetError('meta_2', 'seen_1')).toMatch(/cycle/);
  });

  it('rejects sink → processor (sink chaining rule) with the reason', () => {
    expect(connectTargetError('out_3', 'meta_2')).toBe('sink output can only feed another sink');
  });

  it('accepts sink → sink chaining', () => {
    expect(connectTargetError('out_3', 'out_4')).toBe(null);
  });

  it('rejects cross-pipeline connections', () => {
    expect(connectTargetError('src_0', 'other_0')).toMatch(/different pipelines/);
  });

  it('rejects connecting into a list sub-node', () => {
    ve.graphs[0].nodes.push(mkNode('lst_9', 'rss', [], {isListNode: true, listParentId: 'seen_1'}));
    expect(connectTargetError('src_0', 'lst_9')).toMatch(/list source/);
  });

  it('rejects the upstream pseudo-node as a target', () => {
    ve.graphs[0].nodes.push(mkNode('_upstream', 'upstream', [], {isUpstreamPseudo: true}));
    expect(connectTargetError('src_0', '_upstream')).toMatch(/entry point/);
  });
});

// ── 3. toggleUpstream — same rules as drag-connect, reason in sync note ──────

describe('toggleUpstream validation', () => {
  beforeEach(() => {
    ve.graphs = [
      { name: 'p1', schedule: '', nodes: [
        mkNode('src_0',  'rss'),
        mkNode('seen_1', 'seen', ['src_0']),
        mkNode('meta_2', 'meta', ['seen_1']),
        mkNode('out_3',  'print', ['meta_2']),
      ]},
    ];
    docElementsMock['ve-sync-note'] = { textContent: '' };
    // renderParamPanel exits early via ve-param-empty (absent) — fine.
  });

  it('rejects a cycle and reports the reason in the sync note', () => {
    toggleUpstream('seen_1', 'meta_2', true);
    expect(findNode('seen_1').upstreams).toEqual(['src_0']);
    expect(docElementsMock['ve-sync-note'].textContent).toMatch(/cannot connect: .*cycle/);
  });

  it('rejects sink → processor and reports the sink-chain rule', () => {
    toggleUpstream('meta_2', 'out_3', true);
    expect(findNode('meta_2').upstreams).toEqual(['seen_1']);
    expect(docElementsMock['ve-sync-note'].textContent).toMatch(/sink output can only feed another sink/);
  });

  it('accepts a valid new upstream', () => {
    toggleUpstream('meta_2', 'src_0', true);
    expect(findNode('meta_2').upstreams).toEqual(['seen_1', 'src_0']);
    expect(docElementsMock['ve-sync-note'].textContent).toBe('');
  });

  it('still removes an upstream when unchecked', () => {
    toggleUpstream('seen_1', 'src_0', false);
    expect(findNode('seen_1').upstreams).toEqual([]);
  });
});

// ── 4. requires OR-groups ────────────────────────────────────────────────────

describe('evalRequiresGroups', () => {
  it('satisfied when ANY group member is certain', () => {
    const warns = evalRequiresGroups(
      [['media_type'], ['series_episode_id', 'title']],
      new Set(['media_type', 'title']),
      new Set(['media_type', 'title']));
    expect(warns).toEqual([]);
  });

  it('warns when a group is only reachable (never certain)', () => {
    const warns = evalRequiresGroups(
      [['series_episode_id', 'title']],
      new Set(),
      new Set(['series_episode_id']));
    expect(warns).toHaveLength(1);
    expect(warns[0].level).toBe('warn');
    expect(warns[0].msg).toContain('series_episode_id OR title');
  });

  it('errors when no group member is even reachable', () => {
    const warns = evalRequiresGroups(
      [['series_episode_id', 'title']],
      new Set(),
      new Set());
    expect(warns).toHaveLength(1);
    expect(warns[0].level).toBe('error');
    expect(warns[0].msg).toContain('series_episode_id OR title');
  });

  it('keeps the legacy single-field message shape', () => {
    const warns = evalRequiresGroups([['video_year']], new Set(), new Set());
    expect(warns[0].msg).toBe('requires "video_year" — add video_year-producing node upstream');
  });

  it('evaluates groups independently (AND across groups)', () => {
    const warns = evalRequiresGroups(
      [['media_type'], ['series_episode_id', 'title']],
      new Set(['title']),
      new Set(['title']));
    expect(warns).toHaveLength(1);
    expect(warns[0].msg).toContain('media_type');
  });
});

describe('requiresGroupsFor', () => {
  it('uses requires_groups when present', () => {
    expect(requiresGroupsFor({requires: ['a', 'b'], requires_groups: [['a', 'b']]}))
      .toEqual([['a', 'b']]);
  });

  it('falls back to single-field groups from the flat list', () => {
    expect(requiresGroupsFor({requires: ['a', 'b']})).toEqual([['a'], ['b']]);
  });

  it('returns [] when the plugin requires nothing', () => {
    expect(requiresGroupsFor({requires: []})).toEqual([]);
    expect(requiresGroupsFor(null)).toEqual([]);
  });
});

describe('fieldWarnings with OR-groups', () => {
  it('no false error when one alternative of an OR-group is certain', () => {
    // rss produces title (certain). dedup requires media_type AND
    // (series_episode_id OR title): title satisfies the second group, so only
    // media_type should be reported.
    ve.graphs = [{ name: 'p', schedule: '', nodes: [
      mkNode('src_0', 'rss'),
      mkNode('dd_1',  'dedup', ['src_0']),
    ]}];
    const warns = fieldWarnings(findNode('dd_1'));
    expect(warns).toHaveLength(1);
    expect(warns[0].msg).toContain('media_type');
    expect(warns[0].msg).not.toContain('series_episode_id');
  });
});

// ── 5. layoutGraph places route children to the right ───────────────────────

describe('layoutGraph route/fan-out ordering', () => {
  it('children fed by route ports land to the RIGHT of the route node', () => {
    const g = { name: 'p', schedule: '', nodes: [
      mkNode('src_0',   'rss'),
      mkNode('route_1', 'route', ['src_0'], {portNodeIds: ['route_1__port__tv', 'route_1__port__mv']}),
      mkNode('route_1__port__tv', 'route_selector', ['route_1'],
        {isRoutePort: true, routeParentId: 'route_1', routePortName: 'tv'}),
      mkNode('route_1__port__mv', 'route_selector', ['route_1'],
        {isRoutePort: true, routeParentId: 'route_1', routePortName: 'mv'}),
      mkNode('tv_2', 'seen', ['route_1__port__tv']),
      mkNode('mv_3', 'seen', ['route_1__port__mv']),
      mkNode('out_4', 'print', ['tv_2', 'mv_3']),
    ]};
    layoutGraph(g, 40);
    const x = id => g.nodes.find(n => n.id === id).x;
    expect(x('route_1')).toBeGreaterThan(x('src_0'));
    expect(x('tv_2')).toBeGreaterThan(x('route_1'));
    expect(x('mv_3')).toBeGreaterThan(x('route_1'));
    expect(x('out_4')).toBeGreaterThan(x('tv_2'));
  });

  it('fan-out children land to the right of their shared parent', () => {
    const g = { name: 'p', schedule: '', nodes: [
      mkNode('src_0', 'rss'),
      mkNode('a_1', 'seen', ['src_0']),
      mkNode('b_2', 'meta', ['src_0']),
    ]};
    layoutGraph(g, 40);
    const x = id => g.nodes.find(n => n.id === id).x;
    expect(x('a_1')).toBeGreaterThan(x('src_0'));
    expect(x('b_2')).toBeGreaterThan(x('src_0'));
  });
});

// ── 6. tidyActivePipeline ────────────────────────────────────────────────────

describe('tidyActivePipeline', () => {
  it('re-lays out only the active pipeline, left-to-right', () => {
    ve.graphs = [
      { name: 'a', schedule: '', _regionY: 32, _labelY: 40, _regionH: 150, nodes: [
        // Deliberately backwards stored positions (child left of parent).
        mkNode('src_0',  'rss',  [],        {x: 500, y: 80}),
        mkNode('seen_1', 'seen', ['src_0'], {x: 40,  y: 200}),
      ]},
      { name: 'b', schedule: '', _regionY: 300, _labelY: 308, _regionH: 120, nodes: [
        mkNode('other_0', 'rss', [], {x: 77, y: 350}),
      ]},
    ];
    ve.activeGraph = 0;
    tidyActivePipeline();
    const g = ve.graphs[0];
    const x = id => g.nodes.find(n => n.id === id).x;
    expect(x('seen_1')).toBeGreaterThan(x('src_0'));
    // The inactive pipeline keeps its stored x (y is re-stacked as usual).
    expect(ve.graphs[1].nodes[0].x).toBe(77);
  });

  it('is a no-op when the active pipeline has no nodes', () => {
    ve.graphs = [{ name: 'a', schedule: '', nodes: [] }];
    ve.activeGraph = 0;
    expect(() => tidyActivePipeline()).not.toThrow();
  });
});

// ── 7. function-editor upstream deletion guard ───────────────────────────────

describe('fnDeletionDropsUpstream', () => {
  beforeEach(() => {
    ve.fnEditor = { active: true, funcName: 'fn' };
    ve.graphs = [{ name: 'fn', schedule: '', nodes: [
      mkNode('_upstream', 'upstream', [], {isUpstreamPseudo: true}),
      mkNode('flt_0', 'seen', ['_upstream']),
      mkNode('out_1', 'meta', ['flt_0']),
    ]}];
  });

  it('true when deleting the only _upstream consumer', () => {
    expect(fnDeletionDropsUpstream(['flt_0'])).toBe(true);
  });

  it('false when another consumer remains', () => {
    ve.graphs[0].nodes.push(mkNode('flt_2', 'seen', ['_upstream']));
    expect(fnDeletionDropsUpstream(['flt_0'])).toBe(false);
  });

  it('false for nodes that do not consume _upstream', () => {
    expect(fnDeletionDropsUpstream(['out_1'])).toBe(false);
  });

  it('false outside the function editor', () => {
    ve.fnEditor = { active: false };
    expect(fnDeletionDropsUpstream(['flt_0'])).toBe(false);
  });

  it('removeNode asks for confirmation and aborts on cancel', () => {
    confirmImpl = () => false;
    removeNode('flt_0');
    expect(confirmCalls).toHaveLength(1);
    expect(confirmCalls[0]).toMatch(/signature/);
    expect(findNode('flt_0')).not.toBe(null);
  });

  it('removeNode proceeds when confirmed', () => {
    confirmImpl = () => true;
    removeNode('flt_0');
    expect(findNode('flt_0')).toBe(null);
  });

  it('removeNode never deletes the pseudo-node itself', () => {
    removeNode('_upstream');
    expect(findNode('_upstream')).not.toBe(null);
  });
});

// ── 8. fn editor dirty tracking ──────────────────────────────────────────────

describe('fnEditorIsDirty', () => {
  beforeEach(() => {
    ve.fnEditor = {
      active: true, funcName: 'fn',
      paramsSnapshot: [{key: 'url', type: 'string', default: null, hint: ''}],
      commentSnapshot: '',
    };
    ve.graphs = [{ name: 'fn', schedule: '', nodes: [mkNode('flt_0', 'seen', ['_upstream'])] }];
    ve.fnEditor.dirtyBaseline = fnEditorStateSnapshot();
  });

  it('clean immediately after the baseline is captured', () => {
    expect(fnEditorIsDirty()).toBe(false);
  });

  it('dirty after a node config change', () => {
    ve.graphs[0].nodes[0].config.pattern = 'x';
    expect(fnEditorIsDirty()).toBe(true);
  });

  it('dirty after a param change', () => {
    ve.fnEditor.paramsSnapshot.push({key: 'extra', type: 'string'});
    expect(fnEditorIsDirty()).toBe(true);
  });

  it('dirty after a node position change (positions persist on save)', () => {
    ve.graphs[0].nodes[0].x = 123;
    expect(fnEditorIsDirty()).toBe(true);
  });

  it('treats a missing baseline as dirty (be safe)', () => {
    delete ve.fnEditor.dirtyBaseline;
    expect(fnEditorIsDirty()).toBe(true);
  });

  it('false when the editor is not active', () => {
    ve.fnEditor = { active: false };
    expect(fnEditorIsDirty()).toBe(false);
  });
});
