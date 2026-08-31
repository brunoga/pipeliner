/**
 * Tests for traktStatusHTML in trakt.js — the pure renderer for the Trakt
 * token-status badge in Settings.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'trakt.js'), 'utf8');

let traktStatusHTML;

beforeAll(() => {
  const mod = new Function('exports', src + `
    exports.traktStatusHTML = traktStatusHTML;
  `);
  const exports = {};
  mod(exports);
  traktStatusHTML = exports.traktStatusHTML;
});

const NOW = Date.parse('2026-08-01T00:00:00Z');

describe('traktStatusHTML', () => {
  it('prompts to authorize when there are no tokens', () => {
    expect(traktStatusHTML([], NOW)).toContain('Not authorized yet');
  });

  it('shows a healthy token with a relative expiry', () => {
    const html = traktStatusHTML([{
      client_id: 'abcdef1234567890', expires_at: '2026-10-01T00:00:00Z',
      expired: false, refreshable: true, needs_reauth: false,
    }], NOW);
    expect(html).toContain('trakt-status-ok');
    expect(html).toContain('Authorized');
    expect(html).toContain('auto-refreshes');
    expect(html).toContain('abcdef12'); // truncated client id
    expect(html).not.toContain('abcdef1234567890');
  });

  it('flags a needs-reauth token with the error', () => {
    const html = traktStatusHTML([{
      client_id: 'cid', expires_at: '2026-07-01T00:00:00Z',
      expired: false, refreshable: true, needs_reauth: true,
      last_error: 'refresh token rejected',
    }], NOW);
    expect(html).toContain('trakt-status-error');
    expect(html).toContain('Authorization failed');
    expect(html).toContain('refresh token rejected');
  });

  it('distinguishes expired-refreshable from expired-unrefreshable', () => {
    const refreshable = traktStatusHTML([{ client_id: 'c', expires_at: '2026-07-01T00:00:00Z',
      expired: true, refreshable: true, needs_reauth: false }], NOW);
    expect(refreshable).toContain('refresh automatically');

    const stuck = traktStatusHTML([{ client_id: 'c', expires_at: '2026-07-01T00:00:00Z',
      expired: true, refreshable: false, needs_reauth: false }], NOW);
    expect(stuck).toContain('cannot refresh');
  });

  it('escapes the error message', () => {
    const html = traktStatusHTML([{ client_id: 'c', needs_reauth: true, last_error: '<b>x</b>' }], NOW);
    expect(html).not.toContain('<b>x</b>');
    expect(html).toContain('&lt;b&gt;x&lt;/b&gt;');
  });
});
