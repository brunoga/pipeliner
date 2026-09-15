/**
 * Tests for plexReconcileHTML in database.js — the pure renderer for the
 * Plex reconcile tool (tracker-vs-library diff).
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let plexReconcileHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.plexReconcileHTML = plexReconcileHTML;
  `);
  const exports = {};
  mod(exports);
  plexReconcileHTML = exports.plexReconcileHTML;
});

describe('plexReconcileHTML', () => {
  it('renders the server summary and an in-sync verdict', () => {
    const html = plexReconcileHTML({
      servers: [{ name: 'Hex', sections: 2 }],
      tracker: 100, library: 100, missing: [],
    });
    expect(html).toContain('Hex (2 movie libraries)');
    expect(html).toContain('In sync');
    expect(html).not.toContain('plexrec-cb');
  });

  it('renders missing rows with checkboxes keyed by tracker key', () => {
    const html = plexReconcileHTML({
      servers: [{ name: 'Hex', sections: 1 }],
      tracker: 2, library: 1,
      missing: [{
        key: 'the darkest hour|2011|3d', title: 'the darkest hour', year: 2011,
        is_3d: true, quality: '1080p BluRay H.264', downloaded_at: '2026-05-09T18:20:05Z',
      }],
    });
    expect(html).toContain('1 missing');
    expect(html).toContain('the darkest hour|2011|3d');
    expect(html).toContain('plexrec-cb');
    expect(html).toContain('3D');
    expect(html).toContain('Forget selected');
  });

  it('surfaces a reconcile abort error instead of a table', () => {
    const html = plexReconcileHTML({
      servers: [{ name: 'Hex', err: 'no reachable connection' }],
      error: 'one or more servers could not be listed',
    });
    expect(html).toContain('unreachable');
    expect(html).toContain('could not be listed');
    expect(html).not.toContain('Forget selected');
  });

  it('escapes titles', () => {
    const html = plexReconcileHTML({
      servers: [], tracker: 1, library: 0,
      missing: [{ key: 'x|2020', title: '<b>x</b>', year: 2020 }],
    });
    expect(html).toContain('&lt;b&gt;x&lt;/b&gt;');
  });
});
