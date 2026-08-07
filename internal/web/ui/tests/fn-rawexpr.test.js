/**
 * Raw Starlark expressions in the function editor.
 *
 * The visual function-body parser used to flatten any non-literal value —
 * a function parameter, an env() call, any expression — into a string, at any
 * nesting depth, and the serializer re-quoted it. Opening + saving a function
 * whose notify config used `env("SECRET")` therefore corrupted it into a
 * literal string. fnParseLiteral now wraps non-literals in a raw-expression
 * marker that valToStar emits verbatim, so they round-trip faithfully; the UI
 * renders them read-only so a keystroke can't silently overwrite them.
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

let ve, fnParseLiteral, valToStar, rawExpr, isRawExpr, renderField, collectParams,
    renderNotifierSection, findNode, configToKwargs, nodesToFunctionSource;

beforeAll(() => {
  const noopDoc = new Proxy({}, {
    get: (_, prop) => {
      if (prop === 'querySelector' || prop === 'getElementById') return () => null;
      if (prop === 'querySelectorAll') return () => [];
      if (prop === 'addEventListener' || prop === 'removeEventListener') return () => {};
      return () => null;
    },
  });
  const mod = new Function('exports', 'document', 'fetch', 'confirm',
    helperStubs + src + `
exports.ve = ve;
exports.fnParseLiteral = fnParseLiteral;
exports.valToStar = valToStar;
exports.rawExpr = rawExpr;
exports.isRawExpr = isRawExpr;
exports.renderField = renderField;
exports.collectParams = collectParams;
exports.renderNotifierSection = renderNotifierSection;
exports.findNode = findNode;
exports.configToKwargs = configToKwargs;
exports.nodesToFunctionSource = nodesToFunctionSource;
`);
  const exports = {};
  mod(exports, noopDoc, () => Promise.reject(new Error('no fetch')), () => true);
  ({ ve, fnParseLiteral, valToStar, rawExpr, isRawExpr, renderField, collectParams,
     renderNotifierSection, findNode, configToKwargs, nodesToFunctionSource } = exports);
});

describe('raw expression round-trip (parse → serialize)', () => {
  const cases = [
    ['bare identifier (param)', 'smtp_pass'],
    ['env() call',             'env("SMTP_PASS")'],
    ['env() with default',     'env("SMTP_PASS", default="x")'],
  ];
  for (const [name, expr] of cases) {
    it(`preserves ${name} at the top level`, () => {
      const v = fnParseLiteral(expr);
      expect(isRawExpr(v)).toBe(true);
      expect(valToStar(v)).toBe(expr);
    });
    it(`preserves ${name} inside a nested config dict`, () => {
      const v = fnParseLiteral(`{"password": ${expr}, "smtp_host": "smtp.x"}`);
      expect(isRawExpr(v.password)).toBe(true);
      expect(v.smtp_host).toBe('smtp.x'); // literals stay literals
      expect(valToStar(v)).toBe(`{"password": ${expr}, "smtp_host": "smtp.x"}`);
    });
  }

  it('still parses genuine literals as values (not raw exprs)', () => {
    expect(fnParseLiteral('"hello"')).toBe('hello');
    expect(fnParseLiteral('42')).toBe(42);
    expect(fnParseLiteral('True')).toBe(true);
    expect(fnParseLiteral('["a", "b"]')).toEqual(['a', 'b']);
    expect(isRawExpr(fnParseLiteral('"hello"'))).toBe(false);
  });

  it('round-trips a whole notify node config via configToKwargs (the fn-call/body serializer)', () => {
    const cfg = {
      via: 'email',
      config: fnParseLiteral('{"smtp_host": "smtp.x", "to": ["me@x"], "password": env("SMTP_PASS")}'),
    };
    const kw = configToKwargs(cfg);
    expect(kw).toContain('config={"smtp_host": "smtp.x", "to": ["me@x"], "password": env("SMTP_PASS")}');
    expect(kw).not.toContain('"env('); // the env() call must NOT be quoted
  });
});

describe('read-only rendering of raw expressions', () => {
  it('renderField shows a raw expr read-only, not an editable input', () => {
    const f = { key: 'password', type: 'string' };
    const html = renderField(f, { password: rawExpr('env("SMTP_PASS")') }, { id: 'n1' });
    expect(html).toContain('ve-field-rawexpr');
    expect(html).toContain('env(&quot;SMTP_PASS&quot;)');
    expect(html).not.toContain('data-field="password"'); // no editable widget
    expect(html).toContain('edit in text');
  });

  it('collectParams does not overwrite a raw-expr field', () => {
    const node = { id: 'n1', config: {} };
    const cfg = { password: rawExpr('env("SMTP_PASS")') };
    // A body that would (wrongly) return a value for password if queried.
    const body = { querySelector: () => ({ value: 'typed-over' }) };
    collectParams(node, [{ key: 'password', type: 'string' }], body, cfg);
    expect(isRawExpr(cfg.password)).toBe(true);
    expect(valToStar(cfg.password)).toBe('env("SMTP_PASS")');
  });

  it('renderNotifierSection renders an env() password read-only', () => {
    ve.graphs = [{ name: 'g', nodes: [{
      id: 'notify_1', plugin: 'notify',
      config: { via: 'email', config: { username: 'bot', password: rawExpr('env("SMTP_PASS")') } },
      upstreams: [],
    }] }];
    ve.activeGraph = 0;
    ve.notifiers = { email: [
      { key: 'username', type: 'string' },
      { key: 'password', type: 'string' },
    ] };
    const html = renderNotifierSection(findNode('notify_1'));
    expect(html).toContain('data-field="username"');   // literal stays editable
    expect(html).toContain('ve-field-rawexpr');         // password is read-only
    expect(html).toContain('env(&quot;SMTP_PASS&quot;)');
  });
});

describe('function-editor save path (nodesToFunctionSource)', () => {
  it('re-emits a notify node whose password is env() without quoting it', () => {
    const notifyNode = {
      id: 'send', plugin: 'notify', upstreams: ['_upstream'],
      config: {
        via: 'email',
        config: fnParseLiteral('{"smtp_host": "smtp.x", "to": ["me@x"], "password": env("SMTP_PASS")}'),
      },
      searchNodeIds: [], listNodeIds: [],
    };
    const graph = { nodes: [notifyNode] };
    const source = nodesToFunctionSource(
      'mailer', [], new Set(['send']),
      { entryUpstreams: ['__entry__'], returnNodeId: 'send' }, graph, '');

    expect(source).toContain('def mailer(');
    expect(source).toContain('via="email"');
    expect(source).toContain('"password": env("SMTP_PASS")');
    expect(source).not.toContain('"env('); // never a quoted string
  });
});
