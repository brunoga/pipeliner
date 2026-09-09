/**
 * Node labels: a friendly per-node title round-trips as a `# pipeliner:label`
 * machine comment emitted by dagToStarlark.
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

let ve, dagToStarlark;
beforeAll(() => {
  const noopDoc = new Proxy({}, {
    get: (_, p) => {
      if (p === 'getElementById' || p === 'querySelector') return () => null;
      if (p === 'querySelectorAll') return () => [];
      if (p === 'addEventListener' || p === 'removeEventListener') return () => {};
      return () => null;
    },
  });
  const mod = new Function('exports', 'document', 'fetch', 'confirm',
    helperStubs + src + `
exports.ve = ve;
exports.dagToStarlark = dagToStarlark;
`);
  const exports = {};
  mod(exports, noopDoc, () => Promise.reject(new Error('no fetch')), () => true);
  ({ ve, dagToStarlark } = exports);
});

describe('node label round-trip', () => {
  it('emits a # pipeliner:label comment above the labelled node', () => {
    ve.userPreamble = '';
    ve.userFunctions = {};
    ve.graphs = [{
      name: 'p', schedule: '', nodes: [
        { id: 'src', plugin: 'jackett', upstreams: [], config: {} },
        { id: 'flt', plugin: 'condition', upstreams: ['src'], config: {}, label: 'Match sci-fi and fantasy' },
      ],
    }];
    const out = dagToStarlark();
    expect(out).toContain('# pipeliner:label Match sci-fi and fantasy');
    // The label comment precedes the node's own assignment.
    expect(out.indexOf('# pipeliner:label Match sci-fi and fantasy'))
      .toBeLessThan(out.indexOf('flt = process("condition"'));
    // The unlabelled node emits no label comment.
    expect((out.match(/# pipeliner:label/g) || []).length).toBe(1);
  });

  it('collapses newlines in a label to a single line', () => {
    ve.userPreamble = '';
    ve.userFunctions = {};
    ve.graphs = [{
      name: 'p', schedule: '', nodes: [
        { id: 'src', plugin: 'jackett', upstreams: [], config: {}, label: 'line one\nline two' },
      ],
    }];
    const out = dagToStarlark();
    expect(out).toContain('# pipeliner:label line one line two');
    expect(out).not.toContain('line one\nline two');
  });

  it('omits the label comment when no label is set', () => {
    ve.userPreamble = '';
    ve.userFunctions = {};
    ve.graphs = [{ name: 'p', schedule: '', nodes: [{ id: 'src', plugin: 'jackett', upstreams: [], config: {} }] }];
    expect(dagToStarlark()).not.toContain('# pipeliner:label');
  });
});
