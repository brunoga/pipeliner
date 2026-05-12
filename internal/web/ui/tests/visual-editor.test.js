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

let starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, viaNodeToStar, ve;

beforeAll(() => {
  const mod = new Function(
    'exports', 'document', 'fetch',
    src + `
exports.starLit        = starLit;
exports.valToStar      = valToStar;
exports.configToKwargs = configToKwargs;
exports.upstreamsStr   = upstreamsStr;
exports.dagToStarlark  = dagToStarlark;
exports.viaNodeToStar  = viaNodeToStar;
exports.ve             = ve;
`
  );
  const exports = {};
  const noopDoc = new Proxy({}, { get: () => () => null });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, viaNodeToStar, ve } = exports);
});

// ── test helpers ──────────────────────────────────────────────────────────────

const PLUGINS = [
  { name: 'rss',          role: 'source'    },
  { name: 'seen',         role: 'processor' },
  { name: 'discover',     role: 'processor', accepts_via: true },
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

  it('generates output() for sink nodes (no variable when terminal)', () => {
    setup([
      { id: 'rss_0',           plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'transmission_1',  plugin: 'transmission', config: {}, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('output("transmission", upstream=rss_0)');
    expect(out).not.toContain('transmission_1 =');
  });

  it('assigns variable to output() when it has a chained downstream sink', () => {
    // src → sink1 → sink2: sink1 must be assigned so sink2 can reference it.
    setup([
      { id: 'rss_0',  plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'sink_1', plugin: 'transmission', config: {}, upstreams: ['rss_0'] },
      { id: 'sink_2', plugin: 'print',        config: {}, upstreams: ['sink_1'] },
    ]);
    const out = dagToStarlark();
    // The first (chained) sink must get a variable assignment.
    expect(out).toContain('sink_1 = output("transmission"');
    // The terminal sink should NOT have a variable.
    expect(out).not.toContain('sink_2 =');
    // The terminal sink references the intermediate sink.
    expect(out).toContain('output("print", upstream=sink_1)');
  });

  it('does not assign variable to a fan-out sink (two terminal sinks)', () => {
    // src → sink1, src → sink2: neither is chained so neither needs a variable.
    setup([
      { id: 'rss_0',  plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'sink_1', plugin: 'transmission', config: {}, upstreams: ['rss_0'] },
      { id: 'sink_2', plugin: 'print',        config: {}, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out).not.toContain('sink_1 =');
    expect(out).not.toContain('sink_2 =');
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
    // Only allow # pipeliner:layout (if positions present) — no user comment lines
    const lines = dagToStarlark().split('\n').filter(l => l.startsWith('#'));
    const userComments = lines.filter(l => !l.includes('pipeliner:layout'));
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
      .filter(l => l.startsWith('#') && !l.includes('pipeliner:layout'));
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
  it('emits pipeliner:layout comment when nodes have positions', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], x: 50, y: 76 },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('# pipeliner:layout');
    expect(out).toContain('"rss_0":[50,76]');
  });

  it('rounds float positions to integers', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], x: 50.7, y: 76.3 },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('"rss_0":[51,76]');
  });

  it('layout comment appears just before pipeline()', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [], x: 50, y: 76 },
    ]);
    const out = dagToStarlark();
    const layoutPos   = out.indexOf('# pipeliner:layout');
    const pipelinePos = out.indexOf('pipeline(');
    expect(layoutPos).toBeGreaterThan(-1);
    expect(layoutPos).toBeLessThan(pipelinePos);
    // No blank line between layout comment and pipeline()
    const between = out.slice(layoutPos, pipelinePos);
    expect(between).not.toMatch(/\n\n/);
  });

  it('omits layout comment when no nodes have positions', () => {
    setup([
      { id: 'rss_0', plugin: 'rss', config: {}, upstreams: [] },
    ]);
    const out = dagToStarlark();
    expect(out).not.toContain('pipeliner:layout');
  });

  it('includes multiple nodes in layout', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [], x: 50,  y: 76  },
      { id: 'seen_1', plugin: 'seen', config: {}, upstreams: ['rss_0'], x: 310, y: 76  },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('"rss_0":[50,76]');
    expect(out).toContain('"seen_1":[310,76]');
  });
});

// ── dagToStarlark — via nodes ─────────────────────────────────────────────────

describe('dagToStarlark with via-connected nodes', () => {
  function setupVia() {
    ve.graphs = [{
      name: 'test', schedule: '', comment: '',
      nodes: [
        { id: 'titles_0', plugin: 'rss',     config: {}, upstreams: [], viaNodeIds: [] },
        { id: 'disc_1',   plugin: 'discover', config: { interval: '24h' }, upstreams: ['titles_0'],
          viaNodeIds: ['jk_2', 'rs_3'] },
        { id: 'jk_2',  plugin: 'jackett',    config: { url: 'http://localhost', api_key: 'k' },
          upstreams: [], viaNodeIds: [], isViaNode: true, viaParentId: 'disc_1' },
        { id: 'rs_3',  plugin: 'rss_search', config: { url_template: 'https://...' },
          upstreams: [], viaNodeIds: [], isViaNode: true, viaParentId: 'disc_1' },
      ],
    }];
    ve.activeGraph = 0;
    ve.plugins = PLUGINS;
  }

  it('does not emit input() for via-connected nodes', () => {
    setupVia();
    const out = dagToStarlark();
    expect(out).not.toContain('jk_2 = input');
    expect(out).not.toContain('rs_3 = input');
  });

  it('inlines via nodes as via=[{...}] in the processor', () => {
    setupVia();
    const out = dagToStarlark();
    expect(out).toContain('via=[');
    expect(out).toContain('"name": "jackett"');
    expect(out).toContain('"name": "rss_search"');
  });

  it('includes via config keys in the dict', () => {
    setupVia();
    const out = dagToStarlark();
    expect(out).toContain('"url": "http://localhost"');
    expect(out).toContain('"api_key": "k"');
  });

  it('still emits input() for regular (non-via) source nodes', () => {
    setupVia();
    const out = dagToStarlark();
    expect(out).toContain('titles_0 = input("rss")');
  });

  it('still emits process() for discover with upstream= and via=', () => {
    setupVia();
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

  it('each pipeline has its own comment and layout', () => {
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
    expect(out).toContain('"r_0":[50,76]');
    // Pipeline B has no nodes so no layout comment.
    const sections = out.split('\n\n');
    const sectionB = sections.find(s => s.includes('pipeline("b")'));
    expect(sectionB).not.toContain('pipeliner:layout');
  });

  it('returns empty string for empty graphs list', () => {
    ve.graphs = [];
    expect(dagToStarlark()).toBe('');
  });
});
