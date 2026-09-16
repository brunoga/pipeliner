/**
 * Tests for plexAccountStatusHTML in trakt.js — the Settings-tab Plex
 * account indicator.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'trakt.js'), 'utf8');

let plexAccountStatusHTML;

beforeAll(() => {
  const mod = new Function('exports', 'document', 'fetch', src + `
    exports.plexAccountStatusHTML = plexAccountStatusHTML;
  `);
  const exports = {};
  mod(exports, { getElementById: () => null, addEventListener() {} }, async () => ({ ok: false }));
  plexAccountStatusHTML = exports.plexAccountStatusHTML;
});

describe('plexAccountStatusHTML', () => {
  it('shows signed-in state', () => {
    const html = plexAccountStatusHTML(true);
    expect(html).toContain('Signed in');
    expect(html).toContain('trakt-status-ok');
  });
  it('shows not-signed-in state', () => {
    const html = plexAccountStatusHTML(false);
    expect(html).toContain('Not signed in');
  });
});
