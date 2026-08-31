/**
 * Tests for downloadHistoryHTML in database.js — the pure renderer for the
 * download-history tool.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let downloadHistoryHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.downloadHistoryHTML = downloadHistoryHTML;
  `);
  const exports = {};
  mod(exports);
  downloadHistoryHTML = exports.downloadHistoryHTML;
});

describe('downloadHistoryHTML', () => {
  it('reports no history for an empty list', () => {
    expect(downloadHistoryHTML([])).toContain('No download history');
  });

  it('renders a twice-downloaded episode as an upgrade, tracked', () => {
    const html = downloadHistoryHTML([{
      media_type: 'series', name: 'snw', display_name: 'Star Trek SNW', episode_id: 'S04E05',
      count: 2, currently_tracked: true,
      downloads: [
        { quality: { string: '1080p WEB-DL' }, downloaded_at: '2026-08-01T13:00:00Z', task: 'tv' },
        { quality: { string: '720p WEB-DL' }, downloaded_at: '2026-08-01T12:00:00Z', task: 'tv' },
      ],
    }]);
    expect(html).toContain('Star Trek SNW S04E05');
    expect(html).toContain('dl-upgrade');
    expect(html).toContain('2× (quality upgrades)');
    expect(html).toContain('dl-tracked');
    expect(html).toContain('1080p WEB-DL');
    expect(html).toContain('720p WEB-DL');
  });

  it('renders a single movie download, untracked, with year and 3D', () => {
    const html = downloadHistoryHTML([{
      media_type: 'movie', name: 'furiosa', display_name: 'Furiosa', year: 2024, is_3d: true,
      count: 1, currently_tracked: false,
      downloads: [{ quality: { string: '1080p BluRay' }, downloaded_at: '2026-06-01T00:00:00Z', task: 'movies' }],
    }]);
    expect(html).toContain('Furiosa (2024) [3D]');
    expect(html).toContain('downloaded once');
    expect(html).toContain('dl-untracked');
  });

  it('handles a download with unknown quality', () => {
    const html = downloadHistoryHTML([{
      media_type: 'series', name: 'x', episode_id: 'S01E01', count: 1, currently_tracked: true,
      downloads: [{ quality: {}, downloaded_at: '2026-01-01T00:00:00Z', task: 't' }],
    }]);
    expect(html).toContain('unknown');
  });

  it('escapes titles', () => {
    const html = downloadHistoryHTML([{
      media_type: 'series', name: '<b>x</b>', episode_id: 'S01E01', count: 1,
      downloads: [],
    }]);
    expect(html).not.toContain('<b>x</b>');
    expect(html).toContain('&lt;b&gt;x&lt;/b&gt;');
  });
});
