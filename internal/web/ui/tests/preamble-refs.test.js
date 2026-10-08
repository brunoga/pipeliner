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

/**
 * Interstitial definitions. extractPreamble used to keep only the lines before
 * the FIRST def/node/pipeline, so a top-level variable written further down —
 * between two pipelines, say — was dropped while its uses survived, and the
 * saved config failed to load with "undefined: NAME".
 *
 * Hit live on a config whose MVC_INBOX sat after eight pipelines and fed two
 * of them: "tidy all pipelines" then save produced
 * `config: <input>:833:65: undefined: MVC_INBOX`.
 */
describe('extractPreamble: definitions below the first construct', () => {
  const cfgWithLateDef = [
    'API_KEY = env("K")',
    '',
    'a = input("rss", url="x")',
    'pipeline("first")',
    '',
    '# where the harvest drops its files',
    'INBOX = "/data/inbox"',
    '',
    'b = input("rss", url="y")',
    'c = output("transmission", upstream=b, path=INBOX + "/x")',
    'pipeline("second")',
  ].join('\n');

  it('keeps a definition written between two pipelines', () => {
    const pre = extractPreamble(cfgWithLateDef);
    expect(pre).toContain('INBOX = "/data/inbox"');
    expect(pre).toContain('API_KEY = env("K")');
  });

  it('keeps the definition above every use, so the result parses', () => {
    const pre = extractPreamble(cfgWithLateDef);
    expect(pre.indexOf('INBOX = ')).toBeGreaterThanOrEqual(0);
    // Nothing that uses it may be hoisted along with it.
    expect(pre).not.toContain('output(');
    expect(pre).not.toContain('pipeline(');
    expect(pre).not.toContain('input(');
  });

  it('carries the definition comment with it', () => {
    expect(extractPreamble(cfgWithLateDef)).toContain('# where the harvest drops its files');
  });

  it('never hoists a node, bare or assigned', () => {
    const cfg = [
      'X = 1',
      'n = input("rss", url="x")',
      'process("seen", upstream=n)',
      'output("print", upstream=n)',
      'lanes = route(n, a="true")',
      'pipeline("p")',
    ].join('\n');
    const pre = extractPreamble(cfg);
    expect(pre.trim()).toBe('X = 1');
  });

  it('never hoists a call to a function the file defines', () => {
    const cfg = [
      'Q = "1080p"',
      'def feed_fn(query):',
      '    return input("jackett", api_key=query)',
      '',
      'feed_mvc = feed_fn(query="MVC")',
      'out = output("print", upstream=feed_mvc)',
      'pipeline("p")',
    ].join('\n');
    const pre = extractPreamble(cfg);
    expect(pre.trim()).toBe('Q = "1080p"');
    expect(pre).not.toContain('feed_fn(query="MVC")');
  });

  it('keeps a multi-line value whose brackets span lines', () => {
    const cfg = [
      'a = input("rss", url="x")',
      'pipeline("p1")',
      '',
      'REPORT = (',
      '    "head" +',
      '    "body"',
      ')',
      '',
      'b = input("rss", url="y")',
      'pipeline("p2")',
    ].join('\n');
    const pre = extractPreamble(cfg);
    expect(pre).toContain('REPORT = (');
    expect(pre).toContain('"body"');
    expect(pre).toContain(')');
    expect(pre).not.toContain('pipeline(');
  });

  it('keeps a triple-quoted value and is not fooled by its contents', () => {
    const cfg = [
      'a = input("rss", url="x")',
      'pipeline("p1")',
      '',
      'CARD = """',
      'pipeline("not really")',
      'n = input("nope")',
      '"""',
      '',
      'b = input("rss", url="y")',
      'pipeline("p2")',
    ].join('\n');
    const pre = extractPreamble(cfg);
    expect(pre).toContain('CARD = """');
    expect(pre).toContain('pipeline("not really")');   // inert, inside the string
    expect(pre.match(/CARD = /g)).toHaveLength(1);
    expect(pre).not.toContain('url="y"');
  });

  it('preserves the order definitions were written in', () => {
    const cfg = [
      'FIRST = 1',
      'a = input("rss", url="x")',
      'pipeline("p1")',
      'SECOND = 2',
      'b = input("rss", url="y")',
      'pipeline("p2")',
      'THIRD = 3',
      'c = input("rss", url="z")',
      'pipeline("p3")',
    ].join('\n');
    const pre = extractPreamble(cfg);
    expect(pre.indexOf('FIRST')).toBeLessThan(pre.indexOf('SECOND'));
    expect(pre.indexOf('SECOND')).toBeLessThan(pre.indexOf('THIRD'));
  });

  it('still keeps nothing when the file models no construct at all', () => {
    expect(extractPreamble('A = 1\nB = 2\n')).toBe('');
  });
});
