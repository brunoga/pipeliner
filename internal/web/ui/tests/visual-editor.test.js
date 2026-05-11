/**
 * Tests for the DAG visual pipeline editor (visual-editor.js).
 * Covers the pure serialiser functions: starLit, valToStar, dagToStarlark.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

let starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, ve;

beforeAll(() => {
  const mod = new Function(
    'exports', 'document', 'fetch',
    src + `
exports.starLit        = starLit;
exports.valToStar      = valToStar;
exports.configToKwargs = configToKwargs;
exports.upstreamsStr   = upstreamsStr;
exports.dagToStarlark  = dagToStarlark;
exports.ve             = ve;
`
  );
  const exports = {};
  const noopDoc = new Proxy({}, { get: () => () => null });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ starLit, valToStar, configToKwargs, upstreamsStr, dagToStarlark, ve } = exports);
});

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

// ── dagToStarlark ─────────────────────────────────────────────────────────────

describe('dagToStarlark', () => {
  function setup(nodes, name = 'my-pipeline', schedule = '') {
    ve.model.name     = name;
    ve.model.schedule = schedule;
    ve.model.nodes    = nodes;
    ve.plugins = [
      { name: 'rss',          role: 'source'    },
      { name: 'seen',         role: 'processor' },
      { name: 'metainfo_quality', role: 'processor' },
      { name: 'transmission', role: 'sink'      },
      { name: 'print',        role: 'sink'      },
    ];
  }

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
    expect(out).toContain('seen_1 = process("seen", from_=rss_0)');
  });

  it('generates output() for sink nodes (no variable)', () => {
    setup([
      { id: 'rss_0',           plugin: 'rss',          config: {}, upstreams: [] },
      { id: 'transmission_1',  plugin: 'transmission', config: {}, upstreams: ['rss_0'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('output("transmission", from_=rss_0)');
    expect(out).not.toContain('transmission_1 =');
  });

  it('uses merge() for multiple upstreams', () => {
    setup([
      { id: 'rss_0',  plugin: 'rss',  config: {}, upstreams: [] },
      { id: 'rss_1',  plugin: 'rss',  config: {}, upstreams: [] },
      { id: 'seen_2', plugin: 'seen', config: {}, upstreams: ['rss_0', 'rss_1'] },
    ]);
    const out = dagToStarlark();
    expect(out).toContain('from_=merge(rss_0, rss_1)');
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

  it('preserves node order from model', () => {
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
