/**
 * Centralized-definition round-trip: the visual editor must preserve the
 * top-level env()/variable block (extractPreamble + dagToStarlark) and keep
 * node values that reference those variables as references, not re-inlined
 * literals (overlayConfigExprs). This is the "undefined: JACKETT_API_KEY on
 * save" bug: a visual save used to drop the definitions and leave the nodes
 * referencing an undefined name.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'visual-editor.js'), 'utf8');

const helperStubs = `
function esc(s){return String(s??'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}
function syncHighlight(){}
const CSS={escape:s=>String(s)};
`;

let ve, extractPreamble, overlayConfigExprs, dagToStarlark, isRawExpr, rawExpr;

beforeAll(() => {
  const noopDoc = new Proxy({}, {
    get: (_, prop) => {
      if (prop === 'getElementById' || prop === 'querySelector') return () => null;
      if (prop === 'querySelectorAll') return () => [];
      if (prop === 'addEventListener' || prop === 'removeEventListener') return () => {};
      return () => null;
    },
  });
  const mod = new Function('exports', 'document', 'fetch', 'confirm',
    helperStubs + src + `
exports.ve = ve;
exports.extractPreamble = extractPreamble;
exports.overlayConfigExprs = overlayConfigExprs;
exports.dagToStarlark = dagToStarlark;
exports.isRawExpr = isRawExpr;
exports.rawExpr = rawExpr;
`);
  const exports = {};
  mod(exports, noopDoc, () => Promise.reject(new Error('no fetch')), () => true);
  ({ ve, extractPreamble, overlayConfigExprs, dagToStarlark, isRawExpr, rawExpr } = exports);
});

describe('extractPreamble', () => {
  it('captures the leading definition block before the first def', () => {
    const cfg = [
      '# header',
      'API_KEY = env("K")',
      'SMTP = {"h": "x"}',
      '',
      '# doc',
      'def helper(u):',
      '    return process("seen", upstream=u)',
      '',
      'n = input("rss", url="x")',
      'pipeline("p")',
    ].join('\n');
    const pre = extractPreamble(cfg);
    expect(pre).toContain('API_KEY = env("K")');
    expect(pre).toContain('SMTP = {"h": "x"}');
    expect(pre).not.toContain('def helper');
    expect(pre).not.toContain('pipeline(');
  });

  it('captures the block before the first node when there are no functions', () => {
    const cfg = 'X = "v"\n# pipeliner:pos 1 1\nn = input("rss", url=X)\npipeline("p")';
    const pre = extractPreamble(cfg);
    expect(pre).toContain('X = "v"');
    expect(pre).not.toContain('input(');
    expect(pre).not.toContain('# pipeliner:pos'); // node's own pos comment stays with the node
  });

  it('returns empty when there is no preamble', () => {
    expect(extractPreamble('n = input("rss", url="x")\npipeline("p")')).toBe('');
  });
});

describe('overlayConfigExprs', () => {
  it('replaces referenced values with the raw-expression marker, keeps literals', () => {
    const out = overlayConfigExprs(
      { url: 'https://resolved', api_key: 'secret-resolved', limit: 5 },
      { api_key: { __star_raw__: 'API_KEY' } }
    );
    expect(out.url).toBe('https://resolved');
    expect(out.limit).toBe(5);
    expect(isRawExpr(out.api_key)).toBe(true);
    expect(out.api_key.__star_raw__).toBe('API_KEY');
  });

  it('leaves list/search alone (they round-trip via sub-nodes)', () => {
    const out = overlayConfigExprs({}, { list: [{ __star_raw__: 'L' }], search: [{ __star_raw__: 'S' }] });
    expect(out.list).toBeUndefined();
    expect(out.search).toBeUndefined();
  });

  it('is a no-op without exprs', () => {
    expect(overlayConfigExprs({ a: 1 }, undefined)).toEqual({ a: 1 });
  });
});

describe('dagToStarlark round-trip', () => {
  it('emits the preamble and keeps referenced values unquoted', () => {
    ve.userPreamble = 'API_KEY = env("API_KEY")\nSMTP = {"smtp_host": "h"}';
    ve.userFunctions = {};
    ve.graphs = [{
      name: 'p', schedule: '', nodes: [
        { id: 'src', plugin: 'rss', upstreams: [], config: { url: 'https://x', api_key: rawExpr('API_KEY') } },
        { id: 'snk', plugin: 'notify', upstreams: ['src'], config: { config: rawExpr('SMTP') } },
      ],
    }];
    const out = dagToStarlark();
    // Preamble present at the top.
    expect(out).toContain('API_KEY = env("API_KEY")');
    expect(out).toContain('SMTP = {"smtp_host": "h"}');
    // References emitted unquoted (not "API_KEY").
    expect(out).toMatch(/api_key=API_KEY(\b|,|\))/);
    expect(out).toContain('config=SMTP');
    expect(out).not.toContain('api_key="API_KEY"');
    // The preamble comes before the pipeline definition.
    expect(out.indexOf('API_KEY = env')).toBeLessThan(out.indexOf('pipeline('));
  });
});
