/**
 * Promoting a notify backend config field (e.g. an email password) to a
 * function parameter. The field lives in the node's nested config={} dict, so
 * promotion sets a raw-expression reference (config.config[key] = <paramName>)
 * that round-trips as `config={"key": paramName}` and adds the param to the
 * function signature. Unlink reverses it.
 */

import { describe, it, expect, beforeAll, beforeEach } from 'vitest';
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

let ve, fnEditorPromoteToParam, fnEditorUnlinkParamRef, fnParamUsed, isRawExpr,
    renderNotifierSection, findNode, nodesToFunctionSource, fnParseLiteral;

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
exports.fnEditorPromoteToParam = fnEditorPromoteToParam;
exports.fnEditorUnlinkParamRef = fnEditorUnlinkParamRef;
exports.fnParamUsed = fnParamUsed;
exports.isRawExpr = isRawExpr;
exports.renderNotifierSection = renderNotifierSection;
exports.findNode = findNode;
exports.nodesToFunctionSource = nodesToFunctionSource;
exports.fnParseLiteral = fnParseLiteral;
`);
  const exports = {};
  mod(exports, noopDoc, () => Promise.reject(new Error('no fetch')), () => true);
  ({ ve, fnEditorPromoteToParam, fnEditorUnlinkParamRef, fnParamUsed, isRawExpr,
     renderNotifierSection, findNode, nodesToFunctionSource, fnParseLiteral } = exports);
});

beforeEach(() => {
  ve.graphs = [{ name: 'g', nodes: [{
    id: 'send', plugin: 'notify', upstreams: ['_upstream'],
    config: { via: 'email', config: { username: 'bot', password: 'secret' } },
    searchNodeIds: [], listNodeIds: [],
  }] }];
  ve.activeGraph = 0;
  ve.notifiers = { email: [
    { key: 'username', type: 'string' },
    { key: 'password', type: 'string' },
  ] };
  ve.fnEditor = { active: true, funcName: 'mailer', paramsSnapshot: [], paramsOpen: false };
});

describe('promote a notify field to a function parameter', () => {
  it('sets a raw-expr reference in the nested config and adds a param', () => {
    fnEditorPromoteToParam('notify$send', 'password', 'string');
    const cfg = findNode('send').config.config;
    expect(isRawExpr(cfg.password)).toBe(true);
    expect(cfg.password.__star_raw__).toBe('password');
    expect(cfg.username).toBe('bot'); // untouched
    expect(ve.fnEditor.paramsSnapshot.map(p => p.key)).toContain('password');
  });

  it('renders the promoted field as a param-ref badge, not read-only or editable', () => {
    fnEditorPromoteToParam('notify$send', 'password', 'string');
    const html = renderNotifierSection(findNode('send'));
    expect(html).toContain('ve-field-param-ref');           // badge
    expect(html).toContain('ve-param-ref-unlink');          // unlink button
    expect(html).not.toContain('data-field="password"');    // not an editable input
    expect(html).toContain('data-field="username"');        // sibling literal still editable
  });

  it('round-trips through the save serializer as config={"password": param}', () => {
    fnEditorPromoteToParam('notify$send', 'password', 'string');
    const params = ve.fnEditor.paramsSnapshot.map(p => ({ ...p, include: true, nodeId: null, configKey: null, paramName: p.key }));
    const source = nodesToFunctionSource('mailer', params, new Set(['send']),
      { entryUpstreams: ['__entry__'], returnNodeId: 'send' }, ve.graphs[0], '');
    expect(source).toContain('def mailer(upstream, password):');
    expect(source).toContain('"password": password');
    expect(source).not.toContain('"password": "password"'); // not quoted
  });

  it('unlink removes the reference and the now-unused param', () => {
    fnEditorPromoteToParam('notify$send', 'password', 'string');
    expect(fnParamUsed('password')).toBe(true);
    fnEditorUnlinkParamRef('notify$send', 'password');
    const cfg = findNode('send').config.config;
    expect('password' in cfg).toBe(false);
    expect(ve.fnEditor.paramsSnapshot.map(p => p.key)).not.toContain('password');
    expect(fnParamUsed('password')).toBe(false);
  });

  it('detects a param reused across a nested field and another node', () => {
    fnEditorPromoteToParam('notify$send', 'password', 'string');
    // Simulate the same param referenced by a second node's top-level field.
    ve.graphs[0].nodes.push({ id: 'other', plugin: 'seen', config: {}, _paramRefs: { key: 'password' }, upstreams: [] });
    // Unlinking the notify field must NOT drop the param — still used elsewhere.
    fnEditorUnlinkParamRef('notify$send', 'password');
    expect(ve.fnEditor.paramsSnapshot.map(p => p.key)).toContain('password');
  });
});

describe('re-opening a function that already binds a nested param', () => {
  it('renders a parsed raw-expr param reference as a badge', () => {
    // As parseFunctionBodyNodes would produce for config={"password": pwd}.
    const n = findNode('send');
    n.config.config = fnParseLiteral('{"username": "bot", "password": pwd}');
    ve.fnEditor.paramsSnapshot = [{ key: 'pwd', type: 'string', required: true }];
    const html = renderNotifierSection(n);
    expect(html).toContain('ve-field-param-ref');
    expect(html).toContain('pwd');
    expect(html).not.toContain('data-field="password"');
  });
});
