/**
 * Tests for the DAG visual pipeline editor (visual-editor.js).
 * Covers pure serialiser functions and the comment/layout/via features.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

let starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, viaNodeToStar,
    nodesToFunctionSource, performExtraction, ve;

beforeAll(() => {
  const mod = new Function(
    'exports', 'document', 'fetch',
    src + `
exports.starLit              = starLit;
exports.valToStar            = valToStar;
exports.configToKwargs       = configToKwargs;
exports.upstreamsStr         = upstreamsStr;
exports.dagToStarlark        = dagToStarlark;
exports.viaNodeToStar        = viaNodeToStar;
exports.nodesToFunctionSource = nodesToFunctionSource;
exports.performExtraction    = performExtraction;
exports.ve                   = ve;
`
  );
  const exports = {};
  const noopDoc = new Proxy({}, { get: () => () => null });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, viaNodeToStar,
     nodesToFunctionSource, performExtraction, ve } = exports);
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
