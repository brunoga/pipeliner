/**
 * Tests for the visual pipeline editor logic (visual-editor.js).
 * Focuses on the pure serializer and model functions, which have no DOM dependency.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

// Extract pure functions by wrapping the module source.
let starLit, valToStar, pluginToStar, visualToStarlark, ve;
beforeAll(() => {
  // The visual-editor.js file defines globals including `ve` and the serializer
  // functions. We wrap it and export what we need.
  const mod = new Function(
    'exports', 'document', 'fetch',
    src + `
exports.starLit       = starLit;
exports.valToStar     = valToStar;
exports.pluginToStar  = pluginToStar;
exports.ve            = ve;
// visualToStarlark reads ve.model, so expose it too
exports.visualToStarlark = visualToStarlark;
`
  );
  const exports = {};
  // Provide minimal stubs for DOM globals the module references at definition time.
  const noopDoc = new Proxy({}, { get: () => () => null });
  mod(exports, noopDoc, () => Promise.resolve());
  ({ starLit, valToStar, pluginToStar, ve, visualToStarlark } = exports);
});

// ── starLit ──────────────────────────────────────────────────────────────────

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

// ── pluginToStar ──────────────────────────────────────────────────────────────

describe('pluginToStar', () => {
  it('renders plugin with no config', () => {
    expect(pluginToStar({ name: 'seen', config: {} })).toBe('plugin("seen")');
  });

  it('renders single kwarg', () => {
    const result = pluginToStar({ name: 'rss', config: { url: 'https://example.com' } });
    expect(result).toBe('plugin("rss", url="https://example.com")');
  });

  it('renders bool kwarg', () => {
    const result = pluginToStar({ name: 'seen', config: { local: true } });
    expect(result).toContain('local=True');
  });

  it('uses dict form when "from" key present', () => {
    const result = pluginToStar({ name: 'series', config: { from: [], tracking: 'follow' } });
    expect(result).toContain('"from"');
    expect(result).toContain('"tracking"');
  });

  it('skips empty-string values', () => {
    const result = pluginToStar({ name: 'rss', config: { url: '', other: 'x' } });
    // url is empty so should not appear
    expect(result).not.toContain('url=');
    expect(result).toContain('other=');
  });
});

// ── visualToStarlark ──────────────────────────────────────────────────────────

describe('visualToStarlark', () => {
  it('produces empty string for no tasks', () => {
    ve.model.variables = [];
    ve.model.tasks = [];
    const out = visualToStarlark();
    expect(out.trim()).toBe('');
  });

  it('generates task() call for a simple task', () => {
    ve.model.variables = [];
    ve.model.tasks = [{
      name: 'tv',
      schedule: '1h',
      plugins: [
        { name: 'rss', config: { url: 'https://example.com' } },
        { name: 'seen', config: {} },
      ],
    }];
    const out = visualToStarlark();
    expect(out).toContain('task("tv"');
    expect(out).toContain('schedule="1h"');
    expect(out).toContain('plugin("rss"');
    expect(out).toContain('plugin("seen")');
  });

  it('emits variables block at the top', () => {
    ve.model.variables = [{ name: 'api_key', value: 'abc123' }];
    ve.model.tasks = [];
    const out = visualToStarlark();
    expect(out).toContain('api_key = "abc123"');
  });

  it('omits schedule when empty', () => {
    ve.model.variables = [];
    ve.model.tasks = [{ name: 't', schedule: '', plugins: [{ name: 'seen', config: {} }] }];
    const out = visualToStarlark();
    expect(out).not.toContain('schedule=');
  });

  it('generates multiple tasks', () => {
    ve.model.variables = [];
    ve.model.tasks = [
      { name: 'a', schedule: '', plugins: [{ name: 'seen', config: {} }] },
      { name: 'b', schedule: '6h', plugins: [{ name: 'rss', config: { url: 'x' } }] },
    ];
    const out = visualToStarlark();
    expect(out).toContain('task("a"');
    expect(out).toContain('task("b"');
    expect(out).toContain('schedule="6h"');
  });
});
