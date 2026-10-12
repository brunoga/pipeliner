/**
 * A sub-plugin dict must not round-trip a duplicate "name" key.
 *
 * `search=[{...}]` and `list=[{...}]` carry their plugin in a "name" key, and
 * the editor models that as node.plugin rather than as config -- the server
 * strips it (plugin.ResolveNameAndConfig) and so does the function-body parser.
 * The reference overlay was the one path that did not: configRefs renders a
 * dict literal key by key, so any such dict holding a variable
 * (`{"name": "jackett", "api_key": JACKETT_API_KEY}` -- i.e. every one in a
 * real config) handed "name" back into the node's config. viaNodeToStar then
 * emitted it twice and the save died on `duplicate key: "name"`.
 *
 * That failure is the whole save, not one node, so a user who merely dragged
 * nodes to tidy a pipeline could not store the new positions at all.
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

let overlaySubConfig, viaNodeToStar, rawExpr;

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
exports.overlaySubConfig = overlaySubConfig;
exports.viaNodeToStar = viaNodeToStar;
exports.rawExpr = rawExpr;
`);
  const exports = {};
  mod(exports, noopDoc, () => Promise.reject(new Error('no fetch')), () => true);
  ({ overlaySubConfig, viaNodeToStar, rawExpr } = exports);
});

describe('sub-plugin "name" never lands in config', () => {
  // What /api/parse sends for
  //   search=[{"name": "jackett", "api_key": JACKETT_API_KEY, "limit": 10}]
  // config has name stripped by the server; config_exprs does not, because it
  // renders the dict literal key by key.
  const serverConfig = { api_key: 'resolved-secret', limit: 10 };
  const refOverlay = {
    name: 'jackett',
    api_key: { __star_raw__: 'JACKETT_API_KEY' },
    limit: 10,
  };

  it('strips name from the reference overlay', () => {
    const cfg = overlaySubConfig(serverConfig, refOverlay);
    expect('name' in cfg).toBe(false);
  });

  it('still applies the rest of the overlay', () => {
    const cfg = overlaySubConfig(serverConfig, refOverlay);
    // the variable reference must win over the resolved value, or a save
    // would inline the secret
    expect(cfg.api_key).toEqual({ __star_raw__: 'JACKETT_API_KEY' });
    expect(cfg.limit).toBe(10);
  });

  it('serialises exactly one name key', () => {
    const cfg = overlaySubConfig(serverConfig, refOverlay);
    const out = viaNodeToStar({ plugin: 'jackett', config: cfg });
    const names = out.match(/"name":/g) || [];
    expect(names.length).toBe(1);
    expect(out).toContain('"name": "jackett"');
    expect(out).toContain('"api_key": JACKETT_API_KEY');
  });

  it('serialises one name key even if config somehow still holds one', () => {
    // Belt to overlaySubConfig's braces: a duplicate key fails the whole save,
    // so the serializer must not be able to produce one whatever it is handed.
    const out = viaNodeToStar({ plugin: 'jackett', config: { name: 'stale', limit: 5 } });
    const names = out.match(/"name":/g) || [];
    expect(names.length).toBe(1);
    expect(out).toContain('"name": "jackett"');
    expect(out).not.toContain('stale');
  });

  it('leaves a dict with no name untouched', () => {
    const cfg = overlaySubConfig({ api_key: 'x' }, { api_key: { __star_raw__: 'KEY' } });
    expect(cfg).toEqual({ api_key: { __star_raw__: 'KEY' } });
  });

  it('ignores a raw-expression overlay rather than merging it as a dict', () => {
    const raw = rawExpr('SOME_LIST');
    expect(overlaySubConfig({ a: 1 }, raw)).toEqual({ a: 1 });
  });
});
