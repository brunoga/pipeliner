/**
 * Tests for failuresPanelHTML in dashboard.js — the pure renderer for the
 * dashboard's recent-failures strip.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'dashboard.js'), 'utf8');

let failuresPanelHTML;

beforeAll(() => {
  const shims = `
    var dbLoaded = false;
    var dbNavItems = [];
    function renderDBSidebar() {}
  `;
  const mod = new Function('exports', 'document', 'fetch', 'setTimeout', 'clearTimeout', 'requestAnimationFrame',
    shims + src + `
    exports.failuresPanelHTML = failuresPanelHTML;
    exports.settledBadge = settledBadge;
  `);
  const exports = {};
  mod(exports, { getElementById: () => null, createElement: () => ({}), addEventListener() {}, removeEventListener() {} },
    async () => ({ ok: true, json: async () => ({}) }), () => null, () => {}, cb => cb());
  failuresPanelHTML = exports.failuresPanelHTML;
});

describe('failuresPanelHTML', () => {
  it('renders nothing when there are no failures', () => {
    expect(failuresPanelHTML([])).toBe('');
    expect(failuresPanelHTML(null)).toBe('');
    expect(failuresPanelHTML([], true)).toBe('');
  });

  it('collapsed renders only the header with a count, no rows', () => {
    const html = failuresPanelHTML([
      { title: 'Dead.Torrent.2026.1080p', reason: 'x', task: 'movies', failed_at: new Date().toISOString() },
      { title: 'Other.Torrent', reason: 'y', task: 'movies', failed_at: new Date().toISOString() },
    ], true);
    expect(html).toContain('Recent failures (2)');
    expect(html).toContain('▸');
    expect(html).toContain('section-head');
    expect(html).toContain('collapsed');
    expect(html).not.toContain('Dead.Torrent');
  });

  it('expanded shows the collapse chevron and count', () => {
    const html = failuresPanelHTML([
      { title: 'Dead.Torrent.2026.1080p', reason: 'x', task: 'movies', failed_at: new Date().toISOString() },
    ], false);
    expect(html).toContain('▾');
    expect(html).toContain('Recent failures (1)');
    expect(html).toContain('Dead.Torrent.2026.1080p');
  });

  it('renders one row per failure with reason and task', () => {
    const html = failuresPanelHTML([
      { title: 'Dead.Torrent.2026.1080p', reason: 'deluge: add failed', task: 'movies', failed_at: new Date().toISOString() },
      { title: 'Stalled.3D.Movie', reason: 'grab failed', task: 'movies-3d-discover', failed_at: new Date().toISOString() },
    ]);
    expect(html).toContain('Recent failures');
    expect((html.match(/task-history-row/g) || []).length).toBe(2);
    expect(html).toContain('deluge: add failed');
    expect(html).toContain('movies-3d-discover');
    expect(html).toContain('Failure log'); // pointer to the full history tool
  });

  it('escapes titles and reasons', () => {
    const html = failuresPanelHTML([{ title: '<x>', reason: '<y>', failed_at: new Date().toISOString() }]);
    expect(html).toContain('&lt;x&gt;');
    expect(html).toContain('&lt;y&gt;');
  });
});

describe('settledBadge', () => {
  it('is absent for ordinary failures', () => {
    expect(failuresPanelHTML([
      { title: 'Plain.Release', reason: 'x', task: 'movies', failed_at: new Date().toISOString() },
    ], false)).not.toContain('settled-badge');
  });

  it('marks a settled failure, and flags a revived one distinctly', () => {
    const settled = failuresPanelHTML([
      { title: 'Waited.Release', reason: 'x', task: 'movies', settled: true, failed_at: new Date().toISOString() },
    ], false);
    expect(settled).toContain('settled-badge');
    expect(settled).not.toContain('revived');

    const revived = failuresPanelHTML([
      { title: 'Rebuilt.Release', reason: 'x', task: 'movies', settled: true, settled_revived: true, failed_at: new Date().toISOString() },
    ], false);
    expect(revived).toContain('revived');
  });
});
