/**
 * Notify backend config in the visual editor (feat/notifier-schemas-ui):
 *   - fieldConfigTarget routes "notify$<id>" ids to the node's nested
 *     config={} dict (lazily created), and plain ids to node.config.
 *   - collectParams writes into a passed target object.
 *   - renderNotifierSection renders the selected backend's fields (and points
 *     at the text editor for dict fields).
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

let ve, fieldConfigTarget, collectParams, renderNotifierSection, findNode;

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
exports.fieldConfigTarget = fieldConfigTarget;
exports.collectParams = collectParams;
exports.renderNotifierSection = renderNotifierSection;
exports.findNode = findNode;
`);
  const exports = {};
  mod(exports, noopDoc, () => Promise.reject(new Error('no fetch')), () => true);
  ({ ve, fieldConfigTarget, collectParams, renderNotifierSection, findNode } = exports);
});

function setupNotifyNode(config) {
  ve.graphs = [{ name: 'g', nodes: [{ id: 'notify_1', plugin: 'notify', config, upstreams: [] }] }];
  ve.activeGraph = 0;
  ve.notifiers = {
    email: [
      { key: 'smtp_host', type: 'string', required: true },
      { key: 'smtp_port', type: 'int', default: 25 },
      { key: 'to', type: 'list', required: true },
      { key: 'username', type: 'string' },
      { key: 'password', type: 'string' },
      { key: 'html', type: 'bool' },
    ],
    webhook: [{ key: 'url', type: 'string' }, { key: 'headers', type: 'dict' }],
  };
}

describe('fieldConfigTarget', () => {
  it('routes a plain node id to node.config', () => {
    setupNotifyNode({ via: 'email' });
    expect(fieldConfigTarget('notify_1')).toBe(findNode('notify_1').config);
  });

  it('routes notify$<id> to the nested config, creating it lazily', () => {
    setupNotifyNode({ via: 'email' });
    const n = findNode('notify_1');
    expect(n.config.config).toBeUndefined();
    const tgt = fieldConfigTarget('notify$notify_1');
    expect(tgt).toBe(n.config.config);
    expect(typeof n.config.config).toBe('object');
    tgt.username = 'me';
    expect(findNode('notify_1').config.config.username).toBe('me');
  });

  it('replaces a non-object nested config', () => {
    setupNotifyNode({ via: 'email', config: 'garbage' });
    const tgt = fieldConfigTarget('notify$notify_1');
    expect(tgt).toEqual({});
  });
});

describe('collectParams into a nested target', () => {
  it('writes typed values into the passed cfg, not node.config', () => {
    setupNotifyNode({ via: 'email' });
    const node = findNode('notify_1');
    const cfg = {};
    const fakeBody = {
      querySelector: (sel) => {
        const key = sel.match(/data-field="([^"]+)"/)[1];
        return ({
          smtp_host: { value: 'smtp.example.com' },
          smtp_port: { value: '587' },
          username:  { value: 'user' },
          password:  { value: 'pass' },
          html:      { checked: true },
        })[key] || null;
      },
    };
    collectParams(node, ve.notifiers.email, fakeBody, cfg);
    expect(cfg).toEqual({ smtp_host: 'smtp.example.com', smtp_port: 587, username: 'user', password: 'pass', html: true });
    // list fields are handled by the tag widget, not collectParams
    expect(cfg.to).toBeUndefined();
    // node.config itself was not written
    expect(node.config.smtp_host).toBeUndefined();
  });
});

describe('renderNotifierSection', () => {
  it('prompts to choose a backend when via is empty', () => {
    setupNotifyNode({});
    expect(renderNotifierSection(findNode('notify_1'))).toContain('choose a notifier');
  });

  it('renders the selected backend fields incl. username/password', () => {
    setupNotifyNode({ via: 'email' });
    const html = renderNotifierSection(findNode('notify_1'));
    expect(html).toContain('data-field="username"');
    expect(html).toContain('data-field="password"');
    expect(html).toContain('data-field="smtp_host"');
    expect(html).toContain('data-notify-node="notify_1"');
  });

  it('points dict fields at the text editor instead of a broken input', () => {
    setupNotifyNode({ via: 'webhook' });
    const html = renderNotifierSection(findNode('notify_1'));
    expect(html).toContain('data-field="url"');
    expect(html).not.toContain('data-field="headers"');
    expect(html).toContain('text editor');
  });

  it('renders nothing for an unknown backend', () => {
    setupNotifyNode({ via: 'carrier-pigeon' });
    expect(renderNotifierSection(findNode('notify_1'))).toBe('');
  });
});
