/**
 * Tests for the DAG visual pipeline editor (visual-editor.js).
 * Covers pure serialiser functions and the comment/layout/via features.
 */

import { describe, it, expect, beforeAll, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

let docQueries;
let starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, viaNodeToStar,
    nodesToFunctionSource, performExtraction, addNodeFromPalette, extractFunctionSource,
    parseFunctionComment, nodeTooltipText, edgePath, configPreview, syncRoutePorts,
    condAcceptAbsenceRemovedFields, condNarrowedFields, ve, setActiveGraph,
    _builderGetExpr, _builderSetExpr, renderRouteRulesWidget, renderRouteRuleRow,
    toggleRouteRawMode, _forcedRaw, exprToFlatModel,
    pushUndo, undo, initLayout, marqueeSelect;

// Minimal HTML-escape helper matching dashboard.js's esc() — needed so render
// functions (which call esc as a global) work in the test sandbox.
const escHelper = `function esc(s) {
  return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}\n`;

beforeAll(() => {
  const mod = new Function(
    'exports', 'document', 'fetch',
    escHelper + src + `
exports.starLit              = starLit;
exports.valToStar            = valToStar;
exports.configToKwargs       = configToKwargs;
exports.upstreamsStr         = upstreamsStr;
exports.dagToStarlark        = dagToStarlark;
exports.viaNodeToStar        = viaNodeToStar;
exports.nodesToFunctionSource  = nodesToFunctionSource;
exports.performExtraction     = performExtraction;
exports.addNodeFromPalette    = addNodeFromPalette;
exports.extractFunctionSource = extractFunctionSource;
exports.parseFunctionComment  = parseFunctionComment;
exports.nodeTooltipText       = nodeTooltipText;
exports.edgePath              = edgePath;
exports.configPreview         = configPreview;
exports.syncRoutePorts                = syncRoutePorts;
exports.condAcceptAbsenceRemovedFields = condAcceptAbsenceRemovedFields;
exports.condNarrowedFields             = condNarrowedFields;
exports.ve                            = ve;
exports.setActiveGraph                = setActiveGraph;
exports._builderGetExpr               = _builderGetExpr;
exports._builderSetExpr               = _builderSetExpr;
exports.renderRouteRulesWidget        = renderRouteRulesWidget;
exports.renderRouteRuleRow            = renderRouteRuleRow;
exports.toggleRouteRawMode            = toggleRouteRawMode;
exports._forcedRaw                    = _forcedRaw;
exports.exprToFlatModel               = exprToFlatModel;
exports.pushUndo                      = pushUndo;
exports.undo                          = undo;
exports.initLayout                    = initLayout;
exports.marqueeSelect                 = marqueeSelect;
`
  );
  const exports = {};
  // Stub document. Most methods return null (existing tests depend on the
  // resulting falsy-guard branch being skipped), but querySelectorAll must
  // return an empty iterable so `.forEach(...)` doesn't throw — production
  // code is allowed to assume that contract. querySelector still returns null
  // (preserves the existing no-op behaviour for `?.classList.x()` callers) but
  // records its selector so tests can observe which elements were queried.
  docQueries = [];
  const noopDoc = new Proxy({}, {
    get: (_, prop) => {
      if (prop === 'querySelectorAll') return () => [];
      if (prop === 'querySelector')    return sel => { docQueries.push(sel); return null; };
      return () => null;
    },
  });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, viaNodeToStar,
     nodesToFunctionSource, performExtraction, addNodeFromPalette, extractFunctionSource,
     parseFunctionComment, nodeTooltipText, edgePath, configPreview, syncRoutePorts,
     condAcceptAbsenceRemovedFields, condNarrowedFields, ve, setActiveGraph,
     _builderGetExpr, _builderSetExpr, renderRouteRulesWidget, renderRouteRuleRow,
     toggleRouteRawMode, _forcedRaw, exprToFlatModel,
     pushUndo, undo, initLayout, marqueeSelect } = exports);
});

// ── test helpers ──────────────────────────────────────────────────────────────

const PLUGINS = [
  { name: 'rss',          role: 'source'    },
  { name: 'seen',         role: 'processor' },
  { name: 'discover',     role: 'processor', accepts_search: true },
  { name: 'jackett',      role: 'source',    is_search_plugin: true },
  { name: 'rss_search',   role: 'source',    is_search_plugin: true },
  { name: 'metainfo_quality', role: 'processor' },
  { name: 'transmission', role: 'sink'      },
  { name: 'print',        role: 'sink'      },
];

// Set up a single-graph model (backward-compat with old ve.model tests).
function setup(nodes, name = 'my-pipeline', schedule = '', comment = '') {
  ve.graphs       = [{ name, schedule, nodes, comment }];
  ve.activeGraph  = 0;
  ve.plugins      = PLUGINS;
}

// ── starLit ───────────────────────────────────────────────────────────────────

describe('starLit', () => {
  it('wraps plain string in double quotes', () => {
    expect(starLit('hello')).toBe('"hello"');
  });

  it('escapes double quotes inside string', () => {
    expect(starLit('say "hi"')).toBe('"say \\"hi\\""');
  });

  it('uses triple-quote for multiline strings', () => {
    const result = starLit('line1\nline2');
    expect(result).toMatch(/^"""/);
    expect(result).toMatch(/"""$/);
  });

  it('escapes backslashes', () => {
    expect(starLit('a\\b')).toBe('"a\\\\b"');
  });

  it('converts non-string values to string', () => {
    expect(starLit(42)).toBe('42');
  });
});

// ── valToStar ─────────────────────────────────────────────────────────────────

describe('valToStar', () => {
  it('converts null/undefined to None', () => {
    expect(valToStar(null)).toBe('None');
    expect(valToStar(undefined)).toBe('None');
  });

  it('converts booleans', () => {
    expect(valToStar(true)).toBe('True');
    expect(valToStar(false)).toBe('False');
  });

  it('converts numbers as-is', () => {
    expect(valToStar(42)).toBe('42');
    expect(valToStar(3.14)).toBe('3.14');
  });

  it('converts strings via starLit', () => {
    expect(valToStar('hello')).toBe('"hello"');
  });

  it('converts string array to list literal', () => {
    expect(valToStar(['a', 'b'])).toBe('["a", "b"]');
  });

  it('converts empty array to []', () => {
    expect(valToStar([])).toBe('[]');
  });

  it('converts nested dict', () => {
    const result = valToStar({ key: 'val' });
    expect(result).toContain('"key"');
    expect(result).toContain('"val"');
  });

  it('converts mixed array', () => {
    const result = valToStar([1, 'two', true]);
    expect(result).toContain('1');
    expect(result).toContain('"two"');
    expect(result).toContain('True');
  });
});

// ── configToKwargs ────────────────────────────────────────────────────────────

describe('configToKwargs', () => {
  it('returns empty string for empty config', () => {
    expect(configToKwargs({})).toBe('');
  });

  it('renders single kwarg', () => {
    expect(configToKwargs({ url: 'https://example.com' })).toBe('url="https://example.com"');
  });

  it('renders bool kwarg as True/False', () => {
    expect(configToKwargs({ local: true })).toContain('local=True');
  });

  it('skips null/empty-string values', () => {
    const result = configToKwargs({ url: '', host: 'localhost' });
    expect(result).not.toContain('url=');
    expect(result).toContain('host=');
  });

  it('renders multiple kwargs comma-separated', () => {
    const result = configToKwargs({ host: 'localhost', port: 9091 });
    expect(result).toContain('host=');
    expect(result).toContain('port=');
    expect(result).toContain(',');
  });
});

// ── upstreamsStr ──────────────────────────────────────────────────────────────

describe('upstreamsStr', () => {
  it('returns empty string for no upstreams', () => {
    expect(upstreamsStr([])).toBe('');
    expect(upstreamsStr(null)).toBe('');
  });

  it('returns bare id for single upstream', () => {
    expect(upstreamsStr(['rss_0'])).toBe('rss_0');
  });

  it('wraps multiple upstreams in merge()', () => {
    expect(upstreamsStr(['rss_0', 'html_1'])).toBe('merge(rss_0, html_1)');
  });
});

// ── viaNodeToStar ─────────────────────────────────────────────────────────────

describe('viaNodeToStar', () => {
  it('produces a Starlark dict with a name key', () => {
    const node = { plugin: 'jackett', config: { url: 'http://localhost', api_key: 'abc' } };
    const result = viaNodeToStar(node);
    expect(result).toMatch(/^\{/);
    expect(result).toMatch(/\}$/);
    expect(result).toContain('"name": "jackett"');
    expect(result).toContain('"url": "http://localhost"');
    expect(result).toContain('"api_key": "abc"');
  });

  it('omits empty-string config values', () => {
    const node = { plugin: 'rss_search', config: { url_template: 'https://...', empty: '' } };
    const result = viaNodeToStar(node);
    expect(result).not.toContain('"empty"');
    expect(result).toContain('"url_template"');
  });

  it('omits null config values', () => {
    const node = { plugin: 'jackett', config: { url: 'http://localhost', timeout: null } };
    const result = viaNodeToStar(node);
    expect(result).not.toContain('"timeout"');
  });

  it('handles node with empty config', () => {
    const node = { plugin: 'jackett', config: {} };
    const result = viaNodeToStar(node);
    expect(result).toBe('{"name": "jackett"}');
  });
});

// ── dagToStarlark ─────────────────────────────────────────────────────────────

describe('dagToStarlark', () => {
  it('generates pipeline() call', () => {
    setup([], 'test-pipe', '1h');
    const out = dagToStarlark();
    expect(out).toContain('pipeline("test-pipe"');
    expect(out).toContain('schedule="1h"');
  });

  it('omits schedule when empty', () => {
    setup([], 'p', '');
    expect(dagToStarlark()).not.toContain('schedule=');
  });

  it('generates input() for source nodes', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: { url: 'https://example.com' }, upstreams: [] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('rss_0 = input("rss"');
    expect(out).toContain('url="https://example.com"');
  });

  it('generates process() for processor nodes', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [] },
      { id: 'seen_1', plugin: 'seen', config: {}, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('seen_1 = process("seen", upstream=rss_0)');
  });

  it('assigns variable to output() for sink nodes (position persistence)', () => {
    setup([
      { id: 'rss_0',           plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'transmission_1',  plugin: 'transmission', config: {}, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('transmission_1 = output("transmission", upstream=rss_0)');
  });

  it('assigns variable to all output() nodes in a chained sink', () => {
    // src → sink1 → sink2: both get assignments so positions can be persisted.
    setup([
      { id: 'rss_0',  plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'sink_1', plugin: 'transmission', config: {}, upstreams: ['rss_0'] },
      { id: 'sink_2', plugin: 'print',        config: {}, upstreams: ['sink_1'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('sink_1 = output("transmission"');
    expect(out).toContain('sink_2 = output("print", upstream=sink_1)');
  });

  it('assigns variable to fan-out sinks (two terminal sinks)', () => {
    // src → sink1, src → sink2: both get assignments for position persistence.
    setup([
      { id: 'rss_0',  plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'sink_1', plugin: 'transmission', config: {}, upstreams: ['rss_0'] },
      { id: 'sink_2', plugin: 'print',        config: {}, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('sink_1 = output(');
    expect(out).toContain('sink_2 = output(');
  });

  it('uses merge() for multiple upstreams', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [] },
      { id: 'rss_1',  plugin: 'rss',  config: {}, upstreams: [] },
      { id: 'seen_2', plugin: 'seen', config: {}, upstreams: ['rss_0', 'rss_1'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('upstream=merge(rss_0, rss_1)');
  });

  it('handles fan-out: same upstream in two sinks', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',   config: {}, upstreams: [] },
      { id: 'print_1', plugin: 'print', config: {}, upstreams: ['rss_0'] },
      { id: 'print_2', plugin: 'print', config: {}, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out.match(/output\("print"/g)).toHaveLength(2);
  });

  it('emits upstreams before their dependents (already-ordered model)', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',              config: {}, upstreams: [] },
      { id: 'meta_1', plugin: 'metainfo_quality', config: {}, upstreams: ['rss_0'] },
      { id: 'sink_2', plugin: 'transmission',     config: {}, upstreams: ['meta_1'] },
    ]);
    const out = dagToStarlark();
    const posRss  = out.indexOf('rss_0 = input');
    const posMeta = out.indexOf('meta_1 = process');
    const posSink = out.indexOf('output("transmission"');
    expect(posRss).toBeLessThan(posMeta);
    expect(posMeta).toBeLessThan(posSink);
  });

  it('topologically reorders nodes so upstreams always come first', () => {
    // Simulates the bug: model has series_2 before tvdb_favorites_3,
    // but series_2.upstreams includes tvdb_favorites_3.
    setup([
      { id: 'series_2',        plugin: 'seen',         config: {}, upstreams: ['tvdb_favorites_3'] },
      { id: 'tvdb_favorites_3', plugin: 'rss',          config: {}, upstreams: [] },
    ]);
    const out = dagToStarlark();
    // tvdb_favorites_3 must appear before series_2 regardless of model order.
    const posFav    = out.indexOf('tvdb_favorites_3 = input');
    const posSeries = out.indexOf('series_2 = process');
    expect(posFav).toBeGreaterThan(-1);
    expect(posFav).toBeLessThan(posSeries);
  });

  it('includes config kwargs in output node', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'sink_1', plugin: 'transmission', config: { host: 'myhost', port: 9091 }, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('host="myhost"');
    expect(out).toContain('port=9091');
  });
});

// ── dagToStarlark — comments ──────────────────────────────────────────────────

describe('dagToStarlark with comments', () => {
  it('emits a single-line comment before a source node', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], comment: 'Main RSS feed' },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# Main RSS feed\nrss_0 = input("rss")');
  });

  it('emits multiline comment as separate # lines', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], comment: 'Line one\nLine two' },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# Line one\n# Line two\nrss_0 = input("rss")');
  });

  it('emits a comment before a processor node', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [], comment: '' },
      { id: 'seen_1', plugin: 'seen', config: {}, upstreams: ['rss_0'], comment: 'Dedup filter' },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# Dedup filter\nseen_1 = process("seen"');
  });

  it('does not emit a comment line when comment is empty', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], comment: '' },
    ]);
    // No user-visible comment lines should appear (pipeliner:* lines are machine-managed).
    const lines = dagToStarlark().split('\n').filter(l => l.startsWith('#'));
    const userComments = lines.filter(l => !l.includes('pipeliner:'));
    expect(userComments).toHaveLength(0);
  });

  it('emits a pipeline comment before pipeline()', () => {
    setup([], 'my-pipeline', '', 'Hourly TV show search');
    const out = dagToStarlark();
    expect(out).toContain('# Hourly TV show search\npipeline("my-pipeline")');
  });

  it('emits multiline pipeline comment', () => {
    setup([], 'p', '', 'First line\nSecond line');
    const out = dagToStarlark();
    expect(out).toContain('# First line\n# Second line\npipeline("p")');
  });

  it('omits pipeline comment when empty', () => {
    setup([], 'p', '', '');
    const out = dagToStarlark();
    const commentLines = out.split('\n')
      .filter(l => l.startsWith('#') && !l.includes('pipeliner:'));
    expect(commentLines).toHaveLength(0);
  });

  it('emits comment before layout marker before pipeline()', () => {
    setup([], 'p', '', 'My comment');
    const out = dagToStarlark();
    const commentPos = out.indexOf('# My comment');
    const pipelinePos = out.indexOf('pipeline("p")');
    expect(commentPos).toBeGreaterThan(-1);
    expect(commentPos).toBeLessThan(pipelinePos);
  });

  it('inserts blank line before a comment when preceded by other definitions', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [], comment: '' },
      { id: 'seen_1', plugin: 'seen', config: {}, upstreams: ['rss_0'], comment: 'Dedup step' },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('rss_0 = input("rss")\n\n# Dedup step\nseen_1 = process("seen"');
  });

  it('does not insert blank line before a comment that is the first output', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], comment: 'First node' },
    ]);
    const out = dagToStarlark();
    expect(out.startsWith('# First node\n')).toBe(true);
  });

  it('inserts blank line before pipeline comment when nodes precede it', () => {
    setup(
      [{ id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], comment: '' }],
      'p', '', 'My pipeline'
    );
    const out = dagToStarlark();
    expect(out).toContain('rss_0 = input("rss")\n\n# My pipeline\npipeline("p")');
  });

  it('emits user comment before pipeliner:pos when both are present', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], comment: 'Main feed', x: 50, y: 40 },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# Main feed\n# pipeliner:pos 50 40\nrss_0 = input("rss")');
  });

  it('trims leading/trailing whitespace from comment', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], comment: '  padded  ' },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# padded\nrss_0 = input("rss")');
  });
});

// ── dagToStarlark — layout ────────────────────────────────────────────────────

describe('dagToStarlark with layout', () => {
  it('emits pipeliner:pos comment before a positioned node', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], x: 50, y: 76 },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# pipeliner:pos 50 76\nrss_0 = input("rss")');
  });

  it('rounds float positions to integers', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], x: 50.7, y: 76.3 },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# pipeliner:pos 51 76');
  });

  it('pipeliner:pos appears before the node definition', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], x: 50, y: 76 },
    ]);
    const out = dagToStarlark();
    const posIdx  = out.indexOf('# pipeliner:pos');
    const nodeIdx = out.indexOf('rss_0 = input');
    expect(posIdx).toBeGreaterThan(-1);
    expect(posIdx).toBeLessThan(nodeIdx);
  });

  it('omits pipeliner:pos when node has no position', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [] },
    ]);
    const out = dagToStarlark();
    expect(out).not.toContain('pipeliner:pos');
    expect(out).not.toContain('pipeliner:layout');
  });

  it('emits pipeliner:pos for each positioned node', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [], x: 50,  y: 76  },
      { id: 'seen_1', plugin: 'seen', config: {}, upstreams: ['rss_0'], x: 310, y: 76  },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# pipeliner:pos 50 76\nrss_0 = input');
    expect(out).toContain('# pipeliner:pos 310 76\nseen_1 = process');
  });

  it('blank line appears before pipeliner:pos comment', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [], x: 50,  y: 40  },
      { id: 'seen_1', plugin: 'seen', config: {}, upstreams: ['rss_0'], x: 310, y: 40  },
    ]);
    const out = dagToStarlark();
    // The second node's pos comment must be preceded by a blank line.
    expect(out).toContain('rss_0 = input("rss")\n\n# pipeliner:pos');
  });

  it('blank line appears before pipeline() even without user comment or layout', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('rss_0 = input("rss")\n\npipeline(');
  });
});

// ── dagToStarlark — search nodes ──────────────────────────────────────────────

describe('dagToStarlark with search-connected nodes', () => {
  function setupSearch() {
    ve.graphs = [{
      name: 'test', schedule: '', comment: '',
      nodes: [
        { id: 'titles_0', plugin: 'rss',     config: {}, upstreams: [], searchNodeIds: [] },
        { id: 'disc_1',   plugin: 'discover', config: { interval: '24h' }, upstreams: ['titles_0'],
          searchNodeIds: ['jk_2', 'rs_3'] },
        { id: 'jk_2',  plugin: 'jackett',    config: { url: 'http://localhost', api_key: 'k' },
          upstreams: [], searchNodeIds: [], isSearchNode: true, searchParentId: 'disc_1' },
        { id: 'rs_3',  plugin: 'rss_search', config: { url_template: 'https://...' },
          upstreams: [], searchNodeIds: [], isSearchNode: true, searchParentId: 'disc_1' },
      ],
    }];
    ve.activeGraph = 0;
    ve.plugins = PLUGINS;
  }

  it('does not emit input() for search-connected nodes', () => {
    setupSearch();
    const out = dagToStarlark();
    expect(out).not.toContain('jk_2 = input');
    expect(out).not.toContain('rs_3 = input');
  });

  it('inlines search nodes as search=[{...}] in the processor', () => {
    setupSearch();
    const out = dagToStarlark();
    expect(out).toContain('search=[');
    expect(out).toContain('"name": "jackett"');
    expect(out).toContain('"name": "rss_search"');
  });

  it('includes search config keys in the dict', () => {
    setupSearch();
    const out = dagToStarlark();
    expect(out).toContain('"url": "http://localhost"');
    expect(out).toContain('"api_key": "k"');
  });

  it('still emits input() for regular (non-search) source nodes', () => {
    setupSearch();
    const out = dagToStarlark();
    expect(out).toContain('titles_0 = input("rss")');
  });

  it('still emits process() for discover with upstream= and search=', () => {
    setupSearch();
    const out = dagToStarlark();
    expect(out).toContain('disc_1 = process("discover"');
    expect(out).toContain('upstream=titles_0');
    expect(out).toContain('interval="24h"');
  });
});

// ── dagToStarlark — multiple pipelines ───────────────────────────────────────

describe('dagToStarlark with multiple pipelines', () => {
  it('separates pipelines with a blank line', () => {
    ve.graphs = [
      { name: 'pipe-a', schedule: '',   nodes: [], comment: '' },
      { name: 'pipe-b', schedule: '1h', nodes: [], comment: '' },
    ];
    ve.activeGraph = 0;
    ve.plugins = PLUGINS;
    const out = dagToStarlark();
    expect(out).toContain('pipeline("pipe-a")');
    expect(out).toContain('pipeline("pipe-b"');
    // Each pipeline section is separated by a blank line.
    expect(out).toContain('\n\n');
  });

  it('each pipeline has its own comment; positioned nodes get pipeliner:pos', () => {
    ve.graphs = [
      { name: 'a', schedule: '', comment: 'Pipeline A', nodes: [
          { id: 'r_0', plugin: 'rss', config: {}, upstreams: [], x: 50, y: 76 },
        ]},
      { name: 'b', schedule: '', comment: 'Pipeline B', nodes: [] },
    ];
    ve.activeGraph = 0;
    ve.plugins = PLUGINS;
    const out = dagToStarlark();
    expect(out).toContain('# Pipeline A');
    expect(out).toContain('# Pipeline B');
    expect(out).toContain('# pipeliner:pos 50 76\nr_0 = input');
    // Pipeline B has no nodes so no pipeliner:pos comments.
    const sectionB = out.split('\n\n').find(s => s.includes('pipeline("b")'));
    expect(sectionB).not.toContain('pipeliner:pos');
  });

  it('returns empty string for empty graphs list', () => {
    ve.graphs = [];
    expect(dagToStarlark()).toBe('');
  });
});

// ── nodesToFunctionSource: param type annotation ──────────────────────────────

describe('nodesToFunctionSource param type annotation', () => {
  const graph = { name: 'g', schedule: '', comment: '', nodes: [
    { id: 'rss_0', plugin: 'rss', config: {url: 'https://example.com'}, upstreams: [], searchNodeIds: [], listNodeIds: [] },
    { id: 'seen_1', plugin: 'seen', config: {}, upstreams: ['rss_0'], searchNodeIds: [], listNodeIds: [] },
  ]};
  const selectedIds = new Set(['seen_1']);
  const validation  = { entryUpstreams: ['rss_0'], returnNodeId: 'seen_1' };

  it('emits type=list annotation for list params', () => {
    const params = [
      { nodeId: 'seen_1', configKey: 'cats', paramName: 'cats', type: 'list', defaultValue: ['5030'], include: true, hint: 'Categories' },
    ];
    const src = nodesToFunctionSource('my_fn', params, selectedIds, validation, graph);
    expect(src).toContain('# pipeliner:param cats  type=list  Categories');
  });

  it('emits type=int annotation for int params', () => {
    const params = [
      { nodeId: 'seen_1', configKey: 'seeds', paramName: 'seeds', type: 'int', defaultValue: 1, include: true, hint: '' },
    ];
    const src = nodesToFunctionSource('my_fn', params, selectedIds, validation, graph);
    expect(src).toContain('# pipeliner:param seeds  type=int');
  });

  it('does NOT emit a type annotation for string params', () => {
    const params = [
      { nodeId: 'seen_1', configKey: 'label', paramName: 'label', type: 'string', defaultValue: 'tv', include: true, hint: 'Label' },
    ];
    const src = nodesToFunctionSource('my_fn', params, selectedIds, validation, graph);
    expect(src).not.toContain('type=string');
    expect(src).toContain('# pipeliner:param label  Label');
  });
});

// ── nodesToFunctionSource: list= and search= handling ────────────────────────

describe('nodesToFunctionSource with list/search sub-nodes', () => {
  const makeGraph = nodes => ({ name: 'g', schedule: '', nodes, comment: '' });

  it('includes list= in function body when a node has listNodeIds', () => {
    const listNode = {
      id: 'tl_0', plugin: 'trakt_list', config: { type: 'movies' },
      upstreams: [], searchNodeIds: [], listNodeIds: [], isListNode: true, listParentId: 'movies_1',
    };
    const moviesNode = {
      id: 'movies_1', plugin: 'movies', config: {},
      upstreams: ['rss_0'], searchNodeIds: [], listNodeIds: ['tl_0'],
    };
    const graph = makeGraph([
      { id: 'rss_0', plugin: 'rss', config: { url: 'https://example.com' }, upstreams: [], searchNodeIds: [], listNodeIds: [] },
      moviesNode,
      listNode,
    ]);
    const selectedIds = new Set(['movies_1']);
    const validation  = { entryUpstreams: ['rss_0'], returnNodeId: 'movies_1' };
    const src = nodesToFunctionSource('my_fn', [], selectedIds, validation, graph);
    expect(src).toContain("list=[{");
    expect(src).toContain("trakt_list");
    expect(src).toContain("movies");  // the type= config value
  });

  it('includes search= in function body when a node has searchNodeIds', () => {
    const searchNode = {
      id: 'rs_0', plugin: 'rss_search', config: { url_template: 'https://s.example.com/?q={Query}' },
      upstreams: [], searchNodeIds: [], listNodeIds: [], isSearchNode: true, searchParentId: 'disc_1',
    };
    const discoverNode = {
      id: 'disc_1', plugin: 'discover', config: {},
      upstreams: ['rss_src'], searchNodeIds: ['rs_0'], listNodeIds: [],
    };
    const graph = makeGraph([
      { id: 'rss_src', plugin: 'rss', config: { url: 'https://feed.example.com' }, upstreams: [], searchNodeIds: [], listNodeIds: [] },
      discoverNode,
      searchNode,
    ]);
    const selectedIds = new Set(['disc_1']);
    const validation  = { entryUpstreams: ['rss_src'], returnNodeId: 'disc_1' };
    const src = nodesToFunctionSource('my_fn', [], selectedIds, validation, graph);
    expect(src).toContain("search=[{");
    expect(src).toContain("rss_search");
    expect(src).toContain("url_template");
  });
});

describe('performExtraction removes list/search sub-nodes from canvas', () => {
  function buildModel(nodes) {
    ve.graphs      = [{ name: 'g', schedule: '', nodes, comment: '' }];
    ve.activeGraph = 0;
    ve.plugins     = PLUGINS;
    ve.userFunctions = {};
    ve.selectedNodeIds = new Set();
  }

  it('removes a list sub-node from g.nodes after extraction', () => {
    const listNode = {
      id: 'tl_0', plugin: 'trakt_list', config: { type: 'movies' },
      upstreams: [], searchNodeIds: [], listNodeIds: [], isListNode: true, listParentId: 'movies_1',
    };
    buildModel([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], searchNodeIds: [], listNodeIds: [], x: 60, y: 60 },
      { id: 'movies_1', plugin: 'movies', config: {}, upstreams: ['rss_0'], searchNodeIds: [], listNodeIds: ['tl_0'], x: 260, y: 60 },
      listNode,
    ]);
    ve.selectedNodeIds = new Set(['movies_1']);
    const validation = { ok: true, graphIdx: 0, entryUpstreams: ['rss_0'], returnNodeId: 'movies_1' };
    performExtraction('my_fn', [], validation, 0);
    const ids = ve.graphs[0].nodes.map(n => n.id);
    expect(ids).not.toContain('tl_0');    // list node must be gone
    expect(ids).not.toContain('movies_1'); // selected node must be gone
    expect(ids).toContain('rss_0');        // unselected node must remain
  });

  it('removes a search sub-node from g.nodes after extraction', () => {
    const searchNode = {
      id: 'rs_0', plugin: 'rss_search', config: {}, upstreams: [], searchNodeIds: [], listNodeIds: [],
      isSearchNode: true, searchParentId: 'disc_1',
    };
    buildModel([
      { id: 'rss_src', plugin: 'rss', config: {}, upstreams: [], searchNodeIds: [], listNodeIds: [], x: 60, y: 60 },
      { id: 'disc_1', plugin: 'discover', config: {}, upstreams: ['rss_src'], searchNodeIds: ['rs_0'], listNodeIds: [], x: 260, y: 60 },
      searchNode,
    ]);
    ve.selectedNodeIds = new Set(['disc_1']);
    const validation = { ok: true, graphIdx: 0, entryUpstreams: ['rss_src'], returnNodeId: 'disc_1' };
    performExtraction('my_fn', [], validation, 0);
    const ids = ve.graphs[0].nodes.map(n => n.id);
    expect(ids).not.toContain('rs_0');    // search node must be gone
    expect(ids).not.toContain('disc_1');  // selected node must be gone
    expect(ids).toContain('rss_src');     // unselected node must remain
  });
});

// ── marqueeSelect ─────────────────────────────────────────────────────────────

describe('marqueeSelect', () => {
  // Two pipelines stacked vertically. Region Y bounds are taken straight from
  // _regionY/_regionH the way findGraphAtPosition uses them.
  function setupTwoPipelines() {
    ve.graphs = [
      { name: 'pipe-a', schedule: '', comment: '', _regionY:   0, _regionH: 200, nodes: [
        { id: 'a_src',  plugin: 'rss',  config: {}, upstreams: [],         x:  40, y:  40 },
        { id: 'a_proc', plugin: 'seen', config: {}, upstreams: ['a_src'],  x: 280, y:  40 },
      ]},
      { name: 'pipe-b', schedule: '', comment: '', _regionY: 240, _regionH: 200, nodes: [
        { id: 'b_src',  plugin: 'rss',  config: {}, upstreams: [],         x:  40, y: 280 },
        { id: 'b_proc', plugin: 'seen', config: {}, upstreams: ['b_src'],  x: 280, y: 280 },
      ]},
    ];
    ve.activeGraph     = 0;     // pipe-a is the active one
    ve.selectedNodeId  = null;
    ve.selectedNodeIds = new Set();
  }

  it('selects nodes inside an inactive pipeline and activates it', () => {
    setupTwoPipelines();
    // Marquee that covers both nodes of pipe-b (which is inactive).
    const added = marqueeSelect(20, 260, 520, 380);
    expect(added.sort()).toEqual(['b_proc', 'b_src']);
    expect(ve.activeGraph).toBe(1);
    expect([...ve.selectedNodeIds].sort()).toEqual(['b_proc', 'b_src']);
  });

  it('selects nodes in the active pipeline without changing activeGraph', () => {
    setupTwoPipelines();
    const added = marqueeSelect(20, 20, 520, 140);
    expect(added.sort()).toEqual(['a_proc', 'a_src']);
    expect(ve.activeGraph).toBe(0);
  });

  it('returns an empty selection when the marquee falls outside every region', () => {
    setupTwoPipelines();
    // Far below both pipelines.
    const added = marqueeSelect(20, 2000, 520, 2100);
    expect(added).toEqual([]);
    expect(ve.selectedNodeIds.size).toBe(0);
    expect(ve.activeGraph).toBe(0); // unchanged
  });

  it('highlights owned list/search sub-nodes alongside their parent', () => {
    // series owns a tvdb_favorites list child; discover owns an rss_search.
    // Sub-nodes have no x/y of their own — they only appear visually attached
    // to their parent, so the marquee never crosses them directly.
    ve.graphs = [{
      name: 'p', schedule: '', comment: '', _regionY: 0, _regionH: 400, nodes: [
        { id: 'rss_0',   plugin: 'rss',      config: {}, upstreams: [],          x:  40, y:  40 },
        { id: 'tl_0',    plugin: 'tvdb_favorites', config: {}, upstreams: [], isListNode: true,  listParentId: 'series_1' },
        { id: 'series_1',plugin: 'series',   config: {}, upstreams: ['rss_0'],  x: 280, y:  40, listNodeIds:   ['tl_0'] },
        { id: 'rs_0',    plugin: 'rss_search',     config: {}, upstreams: [], isSearchNode: true, searchParentId: 'disc_1' },
        { id: 'disc_1',  plugin: 'discover', config: {}, upstreams: ['rss_0'],  x: 280, y: 160, searchNodeIds: ['rs_0'] },
      ],
    }];
    ve.activeGraph = 0; ve.selectedNodeId = null; ve.selectedNodeIds = new Set();

    docQueries.length = 0;
    const added = marqueeSelect(20, 20, 520, 220);

    expect(added.sort()).toEqual(['disc_1', 'rss_0', 'series_1']);
    // Sub-nodes themselves stay out of the selection set — they ride along
    // with their parent both visually and in extraction logic.
    expect([...ve.selectedNodeIds].sort()).toEqual(['disc_1', 'rss_0', 'series_1']);
    // The fix: marqueeSelect must querySelector the sub-node ids so they
    // pick up the multi-selected highlight class.
    expect(docQueries).toContain('.ve-node[data-id="tl_0"]');
    expect(docQueries).toContain('.ve-node[data-id="rs_0"]');
  });
});

// ── extractFunctionSource ─────────────────────────────────────────────────────

describe('extractFunctionSource', () => {
  const src = `
# pipeliner:param categories  type=list  Categories to include
# pipeliner:param indexers  type=list  Indexers to be used
def fetch_fn(categories, indexers):
    j = input("jackett_input", categories=categories, indexers=indexers)
    t = process("torrent_alive", upstream=j)
    return t

# pipeliner:pos 138 328
fetch_fn_41 = fetch_fn(categories=["2000"], indexers=["a", "b"])

pipeline("p")
`.trim();

  it('includes all pipeliner:param comment lines before def', () => {
    const result = extractFunctionSource(src, 'fetch_fn');
    expect(result).toContain('# pipeliner:param categories');
    expect(result).toContain('# pipeliner:param indexers');
  });

  it('includes the def line and full indented body', () => {
    const result = extractFunctionSource(src, 'fetch_fn');
    expect(result).toContain('def fetch_fn(categories, indexers):');
    expect(result).toContain('jackett_input');
    expect(result).toContain('return t');
  });

  it('does NOT include the pipeliner:pos comment or call site after the def body', () => {
    const result = extractFunctionSource(src, 'fetch_fn');
    expect(result).not.toContain('pipeliner:pos');
    expect(result).not.toContain('fetch_fn_41');
  });

  it('returns empty string when function is not found', () => {
    expect(extractFunctionSource(src, 'nonexistent_fn')).toBe('');
  });
});

// ── parseFunctionComment ──────────────────────────────────────────────────────

describe('parseFunctionComment', () => {
  it('returns empty string when sourceText is empty', () => {
    expect(parseFunctionComment('')).toBe('');
  });

  it('extracts a single user comment line', () => {
    const src = `# My function description\ndef my_fn(upstream):\n    pass\n`;
    expect(parseFunctionComment(src)).toBe('My function description');
  });

  it('extracts multiple user comment lines', () => {
    const src = `# Line one\n# Line two\ndef my_fn():\n    pass\n`;
    expect(parseFunctionComment(src)).toBe('Line one\nLine two');
  });

  it('ignores pipeliner: machine comment lines', () => {
    const src = `# pipeliner:param host type=string  Host\ndef my_fn(host):\n    pass\n`;
    expect(parseFunctionComment(src)).toBe('');
  });

  it('extracts only user comments when mixed with pipeliner: lines', () => {
    const src = `# Fetches from jackett\n# pipeliner:param cats  type=list  Categories\ndef fetch_fn(cats):\n    pass\n`;
    expect(parseFunctionComment(src)).toBe('Fetches from jackett');
  });

  it('stops at the def line', () => {
    const src = `# Before def\ndef my_fn():\n    # inside body\n    pass\n`;
    expect(parseFunctionComment(src)).toBe('Before def');
  });

  it('handles comment lines without a space after #', () => {
    const src = `#No space\ndef my_fn():\n    pass\n`;
    expect(parseFunctionComment(src)).toBe('No space');
  });
});

// ── nodeTooltipText ───────────────────────────────────────────────────────────

describe('nodeTooltipText', () => {
  beforeEach(() => {
    ve.plugins = PLUGINS;
    ve.userFunctions = {};
  });

  it('returns the node comment for a regular plugin node', () => {
    const n = { plugin: 'rss', comment: 'My feed source', isFunctionCall: false };
    expect(nodeTooltipText(n, { description: 'RSS plugin' })).toBe('My feed source');
  });

  it('returns the plugin description when node comment is empty', () => {
    const n = { plugin: 'rss', comment: '', isFunctionCall: false };
    expect(nodeTooltipText(n, { description: 'RSS plugin' })).toBe('RSS plugin');
  });

  it('returns empty string when node comment and plugin description are both absent', () => {
    const n = { plugin: 'rss', comment: '', isFunctionCall: false };
    expect(nodeTooltipText(n, {})).toBe('');
  });

  it('returns the node comment for a function call node, overriding fn definition comment', () => {
    ve.userFunctions['my_fn'] = { comment: 'Fn def comment', description: 'desc' };
    const n = { plugin: 'my_fn', comment: 'Instance comment', isFunctionCall: true };
    expect(nodeTooltipText(n, {})).toBe('Instance comment');
  });

  it('falls back to function definition comment when node comment is empty', () => {
    ve.userFunctions['my_fn'] = { comment: 'Fn def comment', description: 'desc' };
    const n = { plugin: 'my_fn', comment: '', isFunctionCall: true };
    expect(nodeTooltipText(n, {})).toBe('Fn def comment');
  });

  it('falls back to function description when both node comment and fn comment are empty', () => {
    ve.userFunctions['my_fn'] = { comment: '', description: 'Fn description' };
    const n = { plugin: 'my_fn', comment: '', isFunctionCall: true };
    expect(nodeTooltipText(n, {})).toBe('Fn description');
  });

  it('returns empty string when all comment and description fields are empty', () => {
    ve.userFunctions['my_fn'] = { comment: '', description: '' };
    const n = { plugin: 'my_fn', comment: '', isFunctionCall: true };
    expect(nodeTooltipText(n, {})).toBe('');
  });

  it('preserves newlines in node comment', () => {
    const n = { plugin: 'rss', comment: 'Line one\nLine two', isFunctionCall: false };
    const tip = nodeTooltipText(n, { description: 'RSS plugin' });
    expect(tip).toBe('Line one\nLine two');
    expect(tip).toContain('\n');
  });

  it('preserves newlines in function definition comment', () => {
    ve.userFunctions['my_fn'] = { comment: 'First line\nSecond line', description: '' };
    const n = { plugin: 'my_fn', comment: '', isFunctionCall: true };
    const tip = nodeTooltipText(n, {});
    expect(tip).toBe('First line\nSecond line');
    expect(tip).toContain('\n');
  });

  it('trims leading/trailing whitespace from node comment', () => {
    const n = { plugin: 'rss', comment: '  padded  ', isFunctionCall: false };
    expect(nodeTooltipText(n, {})).toBe('padded');
  });

  it('trims leading/trailing whitespace from fn definition comment', () => {
    ve.userFunctions['my_fn'] = { comment: '  padded  ', description: '' };
    const n = { plugin: 'my_fn', comment: '', isFunctionCall: true };
    expect(nodeTooltipText(n, {})).toBe('padded');
  });
});

// ── nodesToFunctionSource: function comment ───────────────────────────────────

describe('nodesToFunctionSource with function comment', () => {
  const graph = { name: 'g', schedule: '', comment: '', nodes: [
    { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], searchNodeIds: [], listNodeIds: [] },
  ]};
  const selectedIds = new Set(['rss_0']);
  const validation  = { entryUpstreams: [], returnNodeId: 'rss_0' };

  it('emits comment lines before the def line', () => {
    const src = nodesToFunctionSource('my_fn', [], selectedIds, validation, graph, 'Fetches RSS feed');
    expect(src).toContain('# Fetches RSS feed\ndef my_fn():');
  });

  it('emits multiline comment as separate # lines', () => {
    const src = nodesToFunctionSource('my_fn', [], selectedIds, validation, graph, 'Line one\nLine two');
    expect(src).toContain('# Line one\n# Line two\ndef my_fn():');
  });

  it('emits comment before pipeliner:param lines', () => {
    const params = [{ nodeId: 'rss_0', configKey: 'url', paramName: 'url', type: 'string', defaultValue: '', include: true, hint: '' }];
    const src = nodesToFunctionSource('my_fn', params, selectedIds, validation, graph, 'My comment');
    const commentPos = src.indexOf('# My comment');
    const paramPos   = src.indexOf('# pipeliner:param');
    expect(commentPos).toBeGreaterThan(-1);
    expect(commentPos).toBeLessThan(paramPos);
  });

  it('omits comment block when comment is empty', () => {
    const src = nodesToFunctionSource('my_fn', [], selectedIds, validation, graph, '');
    const lines = src.split('\n').filter(l => l.startsWith('#') && !l.includes('pipeliner:'));
    expect(lines).toHaveLength(0);
  });

  it('omits comment block when comment is whitespace only', () => {
    const src = nodesToFunctionSource('my_fn', [], selectedIds, validation, graph, '   ');
    const lines = src.split('\n').filter(l => l.startsWith('#') && !l.includes('pipeliner:'));
    expect(lines).toHaveLength(0);
  });
});

// ── addNodeFromPalette: user function nodes ───────────────────────────────────

describe('addNodeFromPalette with user function', () => {
  beforeEach(() => {
    ve.graphs        = [{ name: 'g', schedule: '', comment: '', nodes: [] }];
    ve.activeGraph   = 0;
    ve.plugins       = PLUGINS;
    ve.userFunctions = {};
    ve.nextId        = 0;
    ve.selectedNodeId = null;
  });

  it('sets isFunctionCall and funcCallKey when adding a user function', () => {
    ve.userFunctions['my_fn'] = {
      name: 'my_fn', role: 'source', description: '', params: [],
      _sourceText: 'def my_fn():\n    pass\n',
    };
    addNodeFromPalette('my_fn');
    const node = ve.graphs[0].nodes[0];
    expect(node.isFunctionCall).toBe(true);
    expect(node.funcCallKey).toBe(node.id);
  });

  it('pre-populates config from param defaults when adding a user function', () => {
    ve.userFunctions['search_fn'] = {
      name: 'search_fn', role: 'source', description: '', params: [
        { key: 'categories', type: 'list',   required: false, default: ['5030'], hint: '' },
        { key: 'min_seeds',  type: 'int',    required: false, default: 1,        hint: '' },
        { key: 'required_p', type: 'string', required: true,  default: null,     hint: '' },
      ],
      _sourceText: '',
    };
    addNodeFromPalette('search_fn');
    const cfg = ve.graphs[0].nodes[0].config;
    expect(cfg.categories).toEqual(['5030']);
    expect(cfg.min_seeds).toBe(1);
    // Required params (no default) get a type-appropriate empty value so the
    // call is syntactically valid; the user must fill in the real value.
    expect(cfg.required_p).toBe('');
  });

  it('does NOT set isFunctionCall for a regular plugin', () => {
    addNodeFromPalette('rss');
    const node = ve.graphs[0].nodes[0];
    expect(node.isFunctionCall).toBeFalsy();
  });

  it('dagToStarlark emits var=funcname(...) even when isFunctionCall flag is missing (legacy broken node)', () => {
    ve.userFunctions['jackett_fn'] = {
      name: 'jackett_fn', role: 'source', description: '',
      params: [],
      _sourceText: 'def jackett_fn():\n    j = input("jackett_input")\n    return j\n',
    };
    // Node without isFunctionCall (as saved by a broken older version).
    ve.graphs[0].nodes.push({
      id: 'jackett_fn_5', plugin: 'jackett_fn', config: {}, upstreams: [],
      searchNodeIds: [], listNodeIds: [], comment: '', x: 60, y: 60,
      // isFunctionCall deliberately absent
    });
    const src = dagToStarlark();
    expect(src).not.toContain('input("jackett_fn"');
    expect(src).toContain('= jackett_fn(');
    expect(src).toContain('def jackett_fn(');
  });

  it('dagToStarlark emits var=funcname(...) not input("funcname",...) for a function call node', () => {
    ve.userFunctions['jackett_fn'] = {
      name: 'jackett_fn', role: 'source', description: '',
      params: [{ key: 'cats', type: 'list', required: false, default: ['5030'], hint: '' }],
      _sourceText: '# pipeliner:param cats  type=list\ndef jackett_fn(cats):\n    j = input("jackett_input", categories=cats)\n    return j\n',
    };
    addNodeFromPalette('jackett_fn');
    const src = dagToStarlark();
    // Must be a function call, not input().
    expect(src).not.toContain('input("jackett_fn"');
    expect(src).toContain('= jackett_fn(');
    // The function definition must also be emitted.
    expect(src).toContain('def jackett_fn(');
  });
});

// ── route() node tests ────────────────────────────────────────────────────────

// Shared setup: src → route → [series_sel, movies_sel] → [seen_s, seen_m] → print
function setupRoute() {
  ve.graphs = [{
    name: 'branched', schedule: '1h', comment: '',
    nodes: [
      { id: 'src_0', plugin: 'rss', config: { url: 'https://example.com/rss' },
        upstreams: [], searchNodeIds: [], listNodeIds: [], portNodeIds: [] },
      { id: 'route_1', plugin: 'route',
        config: { rules: [
          { name: 'series', accept: "series_episode_id != ''" },
          { name: 'movies', accept: "series_episode_id == ''" },
        ]},
        upstreams: ['src_0'], searchNodeIds: [], listNodeIds: [],
        portNodeIds: ['sel_series_2', 'sel_movies_3'] },
      { id: 'sel_series_2', plugin: 'route_selector', config: {},
        upstreams: ['route_1'], searchNodeIds: [], listNodeIds: [], portNodeIds: [],
        isRoutePort: true, routeParentId: 'route_1', routePortName: 'series' },
      { id: 'sel_movies_3', plugin: 'route_selector', config: {},
        upstreams: ['route_1'], searchNodeIds: [], listNodeIds: [], portNodeIds: [],
        isRoutePort: true, routeParentId: 'route_1', routePortName: 'movies' },
      { id: 'seen_s_4', plugin: 'seen', config: {},
        upstreams: ['sel_series_2'], searchNodeIds: [], listNodeIds: [], portNodeIds: [] },
      { id: 'seen_m_5', plugin: 'seen', config: {},
        upstreams: ['sel_movies_3'], searchNodeIds: [], listNodeIds: [], portNodeIds: [] },
      { id: 'print_6', plugin: 'print', config: {},
        upstreams: ['seen_s_4', 'seen_m_5'], searchNodeIds: [], listNodeIds: [], portNodeIds: [] },
    ],
  }];
  ve.activeGraph = 0;
  ve.plugins = [
    ...PLUGINS,
    { name: 'route',          role: 'processor' },
    { name: 'route_selector', role: 'processor' },
  ];
}

describe('upstreamsStr with route port nodes', () => {
  it('translates route_selector upstream to routeNodeId.portName', () => {
    setupRoute();
    // seen_s_4 has upstream sel_series_2, which is isRoutePort with parent route_1, port series
    expect(upstreamsStr(['sel_series_2'])).toBe('route_1.series');
  });

  it('translates multiple route port upstreams in merge()', () => {
    setupRoute();
    expect(upstreamsStr(['sel_series_2', 'sel_movies_3']))
      .toBe('merge(route_1.series, route_1.movies)');
  });

  it('leaves non-route upstream IDs unchanged', () => {
    setupRoute();
    expect(upstreamsStr(['src_0'])).toBe('src_0');
  });
});

describe('dagToStarlark with route() node', () => {
  it('emits route() call with upstream and port conditions', () => {
    setupRoute();
    const out = dagToStarlark();
    expect(out).toContain('route_1 = route(src_0,');
    expect(out).toContain('series=');
    expect(out).toContain('movies=');
  });

  it('does not emit process() or input() for route_selector nodes', () => {
    setupRoute();
    const out = dagToStarlark();
    expect(out).not.toContain('sel_series_2');
    expect(out).not.toContain('sel_movies_3');
    expect(out).not.toContain('route_selector');
  });

  it('emits downstream nodes with port references as upstream', () => {
    setupRoute();
    const out = dagToStarlark();
    expect(out).toContain('upstream=route_1.series');
    expect(out).toContain('upstream=route_1.movies');
  });

  it('emits merge() at the convergence point', () => {
    setupRoute();
    const out = dagToStarlark();
    expect(out).toContain('merge(seen_s_4, seen_m_5)');
  });

  it('still emits input() for the source node', () => {
    setupRoute();
    const out = dagToStarlark();
    expect(out).toContain('src_0 = input("rss"');
  });

  it('emits the pipeline() call with schedule', () => {
    setupRoute();
    const out = dagToStarlark();
    expect(out).toContain('pipeline("branched", schedule="1h")');
  });
});

// ── edgePath ─────────────────────────────────────────────────────────────────

describe('edgePath', () => {
  it('horizontal: control points extend in x direction', () => {
    const d = edgePath(0, 50, 200, 50, 'right', 'left');
    expect(d).toMatch(/^M0,50 C/);
    expect(d).toContain(',50'); // both control points at y=50 for flat line
    expect(d).toContain('200,50');
  });

  it('vertical down: control points extend in y direction', () => {
    const d = edgePath(100, 0, 100, 200, 'down', 'top');
    expect(d).toMatch(/^M100,0 C/);
    expect(d).toContain('100,'); // cp1 x stays at 100
    expect(d).toContain('100,200');
  });

  it('mixed down+left: cp1 goes down, cp2 goes left', () => {
    // route port circle (bottom) → processor (left side)
    const d = edgePath(100, 0, 300, 100, 'down', 'left');
    // cp1 should be (100, something>0) — exits downward
    // cp2 should be (something<300, 100) — arrives from left
    const parts = d.replace('M100,0 C', '').split(' ');
    const [cp1, cp2] = parts;
    const [cp1x, cp1y] = cp1.split(',').map(Number);
    const [cp2x, cp2y] = cp2.split(',').map(Number);
    expect(cp1x).toBe(100);   // doesn't move horizontally from start
    expect(cp1y).toBeGreaterThan(0); // exits downward
    expect(cp2x).toBeLessThan(300);  // arrives from the left
    expect(cp2y).toBe(100);  // at target y level
  });

  it('shorthand h = right+left', () => {
    expect(edgePath(0, 0, 100, 0, 'h')).toBe(edgePath(0, 0, 100, 0, 'right', 'left'));
  });

  it('shorthand v = down+top', () => {
    expect(edgePath(0, 0, 0, 100, 'v')).toBe(edgePath(0, 0, 0, 100, 'down', 'top'));
  });

  it('shorthand v-up = up+bottom', () => {
    expect(edgePath(0, 100, 0, 0, 'v-up')).toBe(edgePath(0, 100, 0, 0, 'up', 'bottom'));
  });
});

// ── configPreview ─────────────────────────────────────────────────────────────

describe('configPreview', () => {
  it('shows key: value for plain strings', () => {
    expect(configPreview({url: 'https://example.com'})).toContain('url: https://example.com');
  });

  it('shows "N rules" for arrays of objects (rule_list)', () => {
    const cfg = { rules: [{name: 'a', accept: '1'}, {name: 'b', accept: '2'}] };
    expect(configPreview(cfg)).toContain('2 rules');
    expect(configPreview(cfg)).not.toContain('[object');
  });

  it('shows "1 rule" singular', () => {
    expect(configPreview({rules: [{name: 'a', accept: '1'}]})).toContain('1 rule');
  });

  it('shows [] for empty arrays', () => {
    expect(configPreview({items: []})).toContain('[]');
  });

  it('truncates long strings', () => {
    const preview = configPreview({url: 'a'.repeat(50)});
    expect(preview.length).toBeLessThan(60);
    expect(preview).toContain('…');
  });
});

// ── syncRoutePorts ────────────────────────────────────────────────────────────

describe('syncRoutePorts', () => {
  function makeGraph(routeNode) {
    const g = { nodes: [routeNode] };
    ve.graphs = [g];
    ve.activeGraph = 0;
    return g;
  }

  it('creates port nodes for each named rule', () => {
    const route = { id: 'r0', plugin: 'route', config: {
      rules: [{name: 'series', accept: "1"}, {name: 'movies', accept: "2"}]
    }, portNodeIds: [] };
    const g = makeGraph(route);
    const changed = syncRoutePorts(route, g);
    expect(changed).toBe(true);
    expect(route.portNodeIds).toHaveLength(2);
    const portNames = g.nodes.filter(n => n.isRoutePort).map(n => n.routePortName);
    expect(portNames).toContain('series');
    expect(portNames).toContain('movies');
  });

  it('removes port nodes when rule is deleted', () => {
    const portId = 'r0__port__series';
    const route = { id: 'r0', plugin: 'route', config: {
      rules: [{name: 'movies', accept: "2"}]
    }, portNodeIds: [portId] };
    const port = { id: portId, isRoutePort: true, routeParentId: 'r0', routePortName: 'series' };
    const g = { nodes: [route, port] };
    const changed = syncRoutePorts(route, g);
    expect(changed).toBe(true);
    expect(route.portNodeIds).not.toContain(portId);
    expect(g.nodes.find(n => n.id === portId)).toBeUndefined();
  });

  it('returns false when nothing changes', () => {
    const portId = 'r0__port__series';
    const route = { id: 'r0', plugin: 'route', config: {
      rules: [{name: 'series', accept: "1"}]
    }, portNodeIds: [portId] };
    const port = { id: portId, isRoutePort: true, routeParentId: 'r0', routePortName: 'series' };
    const g = { nodes: [route, port] };
    const changed = syncRoutePorts(route, g);
    expect(changed).toBe(false);
  });

  it('creates placeholder port for empty-name rules', () => {
    const route = { id: 'r0', plugin: 'route', config: {
      rules: [{name: '', accept: ''}]
    }, portNodeIds: [] };
    const g = makeGraph(route);
    syncRoutePorts(route, g);
    expect(route.portNodeIds).toHaveLength(1); // placeholder port created
  });

  it('syncs portAcceptExpr on existing port', () => {
    const portId = 'r0__port__series';
    const route = { id: 'r0', plugin: 'route', config: {
      rules: [{name: 'series', accept: 'series_episode_id != ""'}]
    }, portNodeIds: [portId] };
    const port = { id: portId, isRoutePort: true, routeParentId: 'r0', routePortName: 'series', portAcceptExpr: '' };
    const g = { nodes: [route, port] };
    syncRoutePorts(route, g);
    expect(port.portAcceptExpr).toBe('series_episode_id != ""');
  });
});

// ── condAcceptAbsenceRemovedFields ────────────────────────────────────────────

describe('condAcceptAbsenceRemovedFields', () => {
  const reachable = ['source', 'description', 'torrent_files', 'magnet_url'];

  it('removes field when accept uses == ""', () => {
    const removed = condAcceptAbsenceRemovedFields('description == ""', {reachable});
    expect(removed).toContain('description');
  });

  it('AND of two absence ops removes both', () => {
    const removed = condAcceptAbsenceRemovedFields('description == "" and torrent_files == ""', {reachable});
    expect(removed).toContain('description');
    expect(removed).toContain('torrent_files');
  });

  it('OR of two different absence ops: intersection empty — removes nothing', () => {
    const removed = condAcceptAbsenceRemovedFields('description == "" or torrent_files == ""', {reachable});
    expect(removed).not.toContain('description');
    expect(removed).not.toContain('torrent_files');
  });

  it('OR of same field: intersection keeps it', () => {
    const removed = condAcceptAbsenceRemovedFields('description == "" or description == ""', {reachable});
    expect(removed).toContain('description');
  });

  it('presence op != "" returns nothing', () => {
    const removed = condAcceptAbsenceRemovedFields('description != ""', {reachable});
    expect(removed).toHaveLength(0);
  });

  it('field not in reachable not returned', () => {
    const removed = condAcceptAbsenceRemovedFields('movie_tagline == ""', {reachable});
    expect(removed).not.toContain('movie_tagline');
  });

  it('empty expression returns empty array', () => {
    expect(condAcceptAbsenceRemovedFields('', {reachable})).toHaveLength(0);
  });
});

// ── _builderGetExpr / _builderSetExpr ─────────────────────────────────────────

describe('_builderGetExpr route mode', () => {
  beforeEach(() => {
    ve.graphs = [{name: 'p', schedule: '', nodes: [
      { id: 'route1', plugin: 'route', config: {
          rules: [
            { name: 'series', accept: 'series_episode_id != ""' },
            { name: 'movies', accept: 'movie_title != ""' },
          ]
        }, portNodeIds: [] }
    ]}];
    ve.activeGraph  = 0;
    ve.selectedNodeId = 'route1';
  });

  it('returns accept expression for the given ruleIdx', () => {
    expect(_builderGetExpr('route', 0)).toBe('series_episode_id != ""');
    expect(_builderGetExpr('route', 1)).toBe('movie_title != ""');
  });

  it('returns empty string for out-of-bounds index', () => {
    expect(_builderGetExpr('route', 99)).toBe('');
  });

  it('returns empty string when rules array is missing', () => {
    const node = ve.graphs[0].nodes[0];
    delete node.config.rules;
    expect(_builderGetExpr('route', 0)).toBe('');
  });
});

describe('_builderGetExpr cond mode', () => {
  beforeEach(() => {
    ve.graphs = [{name: 'p', schedule: '', nodes: [
      { id: 'cond1', plugin: 'condition', config: {
          rules: [{ accept: 'title != ""' }, { reject: 'source == ""' }]
        }}
    ]}];
    ve.activeGraph    = 0;
    ve.selectedNodeId = 'cond1';
  });

  it('returns expr for the given cond ruleIdx', () => {
    expect(_builderGetExpr('cond', 0)).toBe('title != ""');
    expect(_builderGetExpr('cond', 1)).toBe('source == ""');
  });
});

describe('_builderSetExpr route mode', () => {
  let routeNode;

  beforeEach(() => {
    routeNode = { id: 'route1', plugin: 'route', config: {
        rules: [{ name: 'series', accept: '' }]
      }, portNodeIds: [] };
    ve.graphs        = [{ name: 'p', schedule: '', nodes: [routeNode] }];
    ve.activeGraph   = 0;
    ve.selectedNodeId = 'route1';
    ve.plugins       = PLUGINS;
  });

  it('updates node.config.rules[idx].accept', () => {
    _builderSetExpr('route', 0, 'series_episode_id != ""');
    expect(routeNode.config.rules[0].accept).toBe('series_episode_id != ""');
  });

  it('does nothing for out-of-bounds index', () => {
    _builderSetExpr('route', 5, 'x != ""');
    expect(routeNode.config.rules).toHaveLength(1);
  });

  it('does nothing when node is not selected', () => {
    ve.selectedNodeId = 'nonexistent';
    _builderSetExpr('route', 0, 'x != ""');
    // Original value unchanged
    expect(routeNode.config.rules[0].accept).toBe('');
  });
});

// ── renderRouteRulesWidget ────────────────────────────────────────────────────

describe('renderRouteRulesWidget', () => {
  it('renders empty state when no rules', () => {
    const node = { id: 'r0', plugin: 'route', config: {}, portNodeIds: [] };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0;
    ve.selectedNodeId = 'r0';
    const html = renderRouteRulesWidget(node);
    expect(html).toContain('No ports yet');
    expect(html).toContain('+ Add port');
  });

  it('renders one row per rule', () => {
    const node = { id: 'r0', plugin: 'route', config: {
      rules: [
        { name: 'tv',     accept: 'series_episode_id != ""' },
        { name: 'movies', accept: 'movie_title != ""' },
      ]
    }, portNodeIds: [], fields: { certain: [], reachable: [] } };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0;
    ve.selectedNodeId = 'r0';
    ve.fieldRegistry = [];
    const html = renderRouteRulesWidget(node);
    expect(html).toContain('data-rule-idx="0"');
    expect(html).toContain('data-rule-idx="1"');
    expect(html).not.toContain('No ports yet');
  });

  it('uses Ports label with first-match hint', () => {
    const node = { id: 'r0', plugin: 'route', config: { rules: [] }, portNodeIds: [] };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0;
    ve.selectedNodeId = 'r0';
    const html = renderRouteRulesWidget(node);
    expect(html).toContain('Ports');
    expect(html).toContain('first match wins');
  });
});

// ── renderRouteRuleRow ────────────────────────────────────────────────────────

describe('renderRouteRuleRow', () => {
  function makeRouteNode(rules) {
    const node = { id: 'r0', plugin: 'route', config: { rules }, portNodeIds: [],
                   fields: { certain: [], reachable: [] } };
    ve.graphs        = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph   = 0;
    ve.selectedNodeId = 'r0';
    ve.fieldRegistry  = [];
    return node;
  }

  it('renders port name input', () => {
    const node = makeRouteNode([{ name: 'series', accept: 'series_episode_id != ""' }]);
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('ve-cond-port-name');
    expect(html).toContain('value="series"');
  });

  it('renders raw textarea when expression is empty', () => {
    const node = makeRouteNode([{ name: 'p1', accept: '' }]);
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('ve-cond-raw-ta');
    expect(html).toContain('routeRawInput');
    expect(html).toContain('routeRawChanged');
  });

  it('renders builder body when expression is parseable', () => {
    const node = makeRouteNode([{ name: 'tv', accept: 'series_episode_id != ""' }]);
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('ve-cond-builder');
    expect(html).toContain('routeFieldChanged');
    expect(html).toContain('routeOpChanged');
    expect(html).not.toContain('ve-cond-raw-ta');
  });

  it('shows toggleRouteRawMode on raw button', () => {
    const node = makeRouteNode([{ name: 'tv', accept: 'series_episode_id != ""' }]);
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('toggleRouteRawMode(0)');
  });

  it('uses removeRule for delete button', () => {
    const node = makeRouteNode([{ name: 'tv', accept: '' }]);
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('removeRule(');
  });

  it('shows narrowing preview for presence-op accept', () => {
    const node = makeRouteNode([{ name: 'tv', accept: 'series_episode_id != ""' }]);
    node.fields = { certain: [], reachable: ['series_episode_id'] };
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('Promotes to certain');
    expect(html).toContain('series_episode_id');
  });

  it('shows removal notice for absence-op accept', () => {
    const node = makeRouteNode([{ name: 'noDesc', accept: 'description == ""' }]);
    node.fields = { certain: [], reachable: ['description'] };
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('Removes from available');
    expect(html).toContain('description');
  });

  it('uses updateRuleNameOnly oninput', () => {
    const node = makeRouteNode([{ name: 'tv', accept: '' }]);
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('updateRuleNameOnly(');
  });

  it('uses syncRoutePortsForNode onblur', () => {
    const node = makeRouteNode([{ name: 'tv', accept: '' }]);
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('syncRoutePortsForNode(');
  });
});

// ── Bug regression: nid escaping in route widget onclick attributes ────────────
// JSON.stringify(node.id) produces a double-quoted string; without esc() the
// onclick attribute is malformed and all buttons are silently dead.

describe('renderRouteRulesWidget — onclick nid escaping', () => {
  it('Add-port button uses HTML-escaped node ID (no raw double-quotes inside onclick)', () => {
    const node = { id: 'route_1', plugin: 'route', config: { rules: [] }, portNodeIds: [],
                   fields: { certain: [], reachable: [] } };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0; ve.selectedNodeId = 'route_1';
    const html = renderRouteRulesWidget(node);
    // The id "route_1" gets JSON-stringified to "route_1" and must be &quot;-escaped
    expect(html).toContain('&quot;route_1&quot;');
    // There must be no unescaped " inside an onclick="..." attribute
    expect(html).not.toMatch(/onclick="[^"]*"[^'=][^"]*"/);
  });

  it('Remove and name-input handlers in renderRouteRuleRow use escaped node ID', () => {
    const node = { id: 'route_2', plugin: 'route',
                   config: { rules: [{ name: 'tv', accept: '' }] }, portNodeIds: [],
                   fields: { certain: [], reachable: [] } };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0; ve.selectedNodeId = 'route_2'; ve.fieldRegistry = [];
    const html = renderRouteRuleRow(node.config.rules[0], 0, node);
    expect(html).toContain('&quot;route_2&quot;');
  });
});

// ── Bug regression: toggleRouteRawMode with empty expression ──────────────────
// When a new port is added (accept:''), the rule starts in raw mode because
// expr.trim()==='' . Clicking "≡ builder" should switch TO builder by setting
// a default expression — not add a forced-raw flag.

describe('toggleRouteRawMode', () => {
  beforeEach(() => _forcedRaw.clear());

  it('switches empty-expression rule to builder by setting a default expression', () => {
    const node = { id: 'r0', plugin: 'route', upstreams: [],
                   config: { rules: [{ name: 'tv', accept: '' }] }, portNodeIds: [],
                   fields: { certain: [], reachable: ['title', 'source'] } };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0; ve.selectedNodeId = 'r0'; ve.plugins = PLUGINS;

    toggleRouteRawMode(0);

    expect(node.config.rules[0].accept).not.toBe('');
    const model = exprToFlatModel(node.config.rules[0].accept);
    expect(model).not.toBeNull();
    expect(model.clauses.length).toBeGreaterThan(0);
  });

  it('does NOT add a forced-raw key when switching empty → builder', () => {
    const node = { id: 'r0', plugin: 'route', upstreams: [],
                   config: { rules: [{ name: 'tv', accept: '' }] }, portNodeIds: [],
                   fields: { certain: [], reachable: ['title'] } };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0; ve.selectedNodeId = 'r0'; ve.plugins = PLUGINS;

    toggleRouteRawMode(0);

    expect(_forcedRaw.has('route:r0:0')).toBe(false);
  });

  it('switches parseable-expression builder → raw by setting forced-raw flag', () => {
    const node = { id: 'r0', plugin: 'route', upstreams: [],
                   config: { rules: [{ name: 'tv', accept: 'title != ""' }] }, portNodeIds: [],
                   fields: { certain: [], reachable: ['title'] } };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0; ve.selectedNodeId = 'r0'; ve.plugins = PLUGINS;

    toggleRouteRawMode(0); // builder → raw

    expect(_forcedRaw.has('route:r0:0')).toBe(true);
    expect(node.config.rules[0].accept).toBe('title != ""'); // expression unchanged
  });

  it('switches forced-raw → builder by removing the forced-raw flag', () => {
    _forcedRaw.add('route:r0:0');
    const node = { id: 'r0', plugin: 'route', upstreams: [],
                   config: { rules: [{ name: 'tv', accept: 'title != ""' }] }, portNodeIds: [],
                   fields: { certain: [], reachable: ['title'] } };
    ve.graphs = [{ name: 'p', schedule: '', nodes: [node] }];
    ve.activeGraph = 0; ve.selectedNodeId = 'r0'; ve.plugins = PLUGINS;

    toggleRouteRawMode(0); // forced-raw → builder

    expect(_forcedRaw.has('route:r0:0')).toBe(false);
    expect(node.config.rules[0].accept).toBe('title != ""'); // expression preserved
  });
});

// ── Bug regression: dagToStarlark topo-sort with route ports ──────────────────
// Route port nodes are excluded from the sort but their IDs appear in downstream
// node upstreams. Without remapping, the route node has no dependency edges and
// may be emitted after its dependents — causing "referenced before assignment".

describe('dagToStarlark — route port upstream topo ordering', () => {
  it('emits the route node before any node that uses one of its ports', () => {
    const rss   = { id: 'rss_0', plugin: 'rss', upstreams: [], config: { url: 'http://x' },
                    x: 50, y: 100 };
    const route = { id: 'route_1', plugin: 'route', upstreams: ['rss_0'],
                    config: { rules: [{ name: 'torrent', accept: 'torrent_link_type == "torrent"' }] },
                    portNodeIds: ['route_1__port__torrent'], x: 310, y: 100 };
    const port  = { id: 'route_1__port__torrent', plugin: 'route_selector',
                    upstreams: ['route_1'], config: {},
                    isRoutePort: true, routeParentId: 'route_1', routePortName: 'torrent',
                    portAcceptExpr: 'torrent_link_type == "torrent"', x: null, y: null };
    const seen  = { id: 'seen_2', plugin: 'seen', upstreams: ['route_1__port__torrent'],
                    config: {}, x: 570, y: 100 };
    setup([rss, route, port, seen]);

    const star  = dagToStarlark();
    const lines = star.split('\n');
    const routeLine = lines.findIndex(l => l.includes('route_1 ='));
    const seenLine  = lines.findIndex(l => l.includes('seen_2 ='));
    expect(routeLine).toBeGreaterThanOrEqual(0);
    expect(seenLine).toBeGreaterThanOrEqual(0);
    expect(routeLine).toBeLessThan(seenLine);
  });

  it('handles two downstream nodes (torrent + magnet ports) both after route', () => {
    const rss     = { id: 'rss_0', plugin: 'rss', upstreams: [], config: { url: 'http://x' },
                      x: 50, y: 100 };
    const route   = { id: 'route_1', plugin: 'route', upstreams: ['rss_0'],
                      config: { rules: [
                        { name: 'torrent', accept: 'torrent_link_type == "torrent"' },
                        { name: 'magnet',  accept: 'torrent_link_type == "magnet"'  },
                      ]},
                      portNodeIds: ['route_1__port__torrent', 'route_1__port__magnet'],
                      x: 310, y: 100 };
    const portT   = { id: 'route_1__port__torrent', plugin: 'route_selector',
                      upstreams: ['route_1'], config: {},
                      isRoutePort: true, routeParentId: 'route_1', routePortName: 'torrent',
                      portAcceptExpr: 'torrent_link_type == "torrent"', x: null, y: null };
    const portM   = { id: 'route_1__port__magnet', plugin: 'route_selector',
                      upstreams: ['route_1'], config: {},
                      isRoutePort: true, routeParentId: 'route_1', routePortName: 'magnet',
                      portAcceptExpr: 'torrent_link_type == "magnet"', x: null, y: null };
    const metaT   = { id: 'meta_t_3', plugin: 'seen', upstreams: ['route_1__port__torrent'],
                      config: {}, x: 570, y: 50  };
    const metaM   = { id: 'meta_m_4', plugin: 'seen', upstreams: ['route_1__port__magnet'],
                      config: {}, x: 570, y: 150 };
    setup([rss, route, portT, portM, metaT, metaM]);

    const star  = dagToStarlark();
    const lines = star.split('\n');
    const routeLine = lines.findIndex(l => l.includes('route_1 ='));
    const metaTLine = lines.findIndex(l => l.includes('meta_t_3 ='));
    const metaMLine = lines.findIndex(l => l.includes('meta_m_4 ='));
    expect(routeLine).toBeGreaterThanOrEqual(0);
    expect(routeLine).toBeLessThan(metaTLine);
    expect(routeLine).toBeLessThan(metaMLine);
  });
});

// setActiveGraph centralises the "switch active pipeline" mutation so all
// call sites stay in sync. Selecting a node in another pipeline used to set
// ve.activeGraph without refreshing the label/region DOM, then a follow-up
// canvas click in that same pipeline early-returned (gi === activeGraph) and
// the labels stayed stuck on the previous pipeline forever.
describe('setActiveGraph', () => {
  beforeEach(() => {
    ve.graphs = [
      { name: 'A', nodes: [], schedule: '' },
      { name: 'B', nodes: [], schedule: '' },
    ];
    ve.activeGraph = 0;
  });

  it('updates ve.activeGraph and returns true for a valid index', () => {
    expect(setActiveGraph(1)).toBe(true);
    expect(ve.activeGraph).toBe(1);
  });

  it('is idempotent for the same index (still returns true, no churn)', () => {
    setActiveGraph(0);
    expect(setActiveGraph(0)).toBe(true);
    expect(ve.activeGraph).toBe(0);
  });

  it('returns false and leaves activeGraph alone for out-of-range indices', () => {
    expect(setActiveGraph(-1)).toBe(false);
    expect(setActiveGraph(99)).toBe(false);
    expect(ve.activeGraph).toBe(0);
  });
});

// ── undo: y values are stable across pushUndo + undo ──────────────────────────
//
// Regression: before the fix, undo() called initLayout() on a snapshot whose
// nodes carried ABSOLUTE y values. initLayout's stored-position branch adds
// g._regionY to every node y, treating it as relative — so each undo would
// shift nodes down by ~32px (more for later pipelines). The fix subtracts
// _regionY before calling initLayout, so the round-trip is idempotent.
describe('undo() y-stability', () => {
  beforeEach(() => {
    ve.graphs = [];
    ve.userFunctions = {};
    ve.nextId = 1;
    ve.activeGraph = 0;
    ve.selectedNodeId = null;
    ve.plugins = PLUGINS;
  });

  it('preserves node y after pushUndo() + undo() when nothing else changes', () => {
    // Single pipeline with one positioned node (server-style: y is relative).
    ve.graphs = [{
      name: 'pipe', schedule: '', comment: '',
      nodes: [
        { id: 'n1', plugin: 'rss', config: {}, upstreams: [], x: 50, y: 44 },
      ],
    }];

    // initLayout converts relative y → absolute y (_regionY + relY).
    initLayout();
    const afterInit = ve.graphs[0].nodes[0].y;
    const regionY = ve.graphs[0]._regionY;
    expect(afterInit).toBe(regionY + 44);

    // Take a snapshot, then undo without making any change. After the round-
    // trip the node must end up at the same absolute y, not shifted by
    // _regionY.
    pushUndo();
    undo();

    expect(ve.graphs[0].nodes[0].y).toBe(afterInit);
  });

  it('preserves node y across two consecutive undos', () => {
    ve.graphs = [{
      name: 'pipe', schedule: '', comment: '',
      nodes: [
        { id: 'n1', plugin: 'rss', config: {}, upstreams: [], x: 50, y: 44 },
      ],
    }];
    initLayout();
    const baseline = ve.graphs[0].nodes[0].y;

    pushUndo();
    pushUndo();
    undo();
    undo();

    expect(ve.graphs[0].nodes[0].y).toBe(baseline);
  });

  it('preserves later-pipeline node y after undo (no compounding shift)', () => {
    ve.graphs = [
      { name: 'p1', schedule: '', comment: '', nodes: [
        { id: 'a', plugin: 'rss',    config: {}, upstreams: [],   x: 50, y: 44 },
        { id: 'b', plugin: 'print',  config: {}, upstreams: ['a'], x: 310, y: 44 },
      ]},
      { name: 'p2', schedule: '', comment: '', nodes: [
        { id: 'c', plugin: 'rss',    config: {}, upstreams: [],   x: 50, y: 44 },
        { id: 'd', plugin: 'print',  config: {}, upstreams: ['c'], x: 310, y: 44 },
      ]},
    ];

    initLayout();
    const before = ve.graphs.map(g => g.nodes.map(n => n.y));

    pushUndo();
    undo();

    const after = ve.graphs.map(g => g.nodes.map(n => n.y));
    expect(after).toEqual(before);
  });
});
