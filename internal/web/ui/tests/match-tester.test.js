/**
 * Tests for the match tester helpers in database.js:
 *   - matchTesterResultHTML: pure ProbeResult → HTML renderer
 *   - listCacheBuckets: filters dbNavItems for resolved title-list caches
 *
 * Extracted via the Function constructor like the other database.js helper
 * tests — no DOM or fetch needed for the pure functions.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let matchTesterResultHTML, listCacheBuckets, setNavItems;

beforeAll(() => {
  // esc() lives in dashboard.js; provide a minimal stand-in so database.js's
  // references resolve when extracted in isolation.
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.matchTesterResultHTML = matchTesterResultHTML;
    exports.listCacheBuckets = listCacheBuckets;
    exports.setNavItems = (v) => { dbNavItems = v; };
  `);
  const exports = {};
  mod(exports);
  matchTesterResultHTML = exports.matchTesterResultHTML;
  listCacheBuckets = exports.listCacheBuckets;
  setNavItems = exports.setNavItems;
});

describe('matchTesterResultHTML', () => {
  it('renders a MATCH verdict with the matched candidate', () => {
    const html = matchTesterResultHTML({
      matched: true,
      matched_by: 'star trek strange new worlds',
      input_norm: 'star trek strange new worlds',
      candidates: [
        { norm: 'star trek strange new worlds', matched: true, distance: 0, year: 0 },
        { norm: 'silo', matched: false, distance: 26, year: 0 },
      ],
    });
    expect(html).toContain('MATCH');
    expect(html).toContain('match-yes');
    expect(html).toContain('star trek strange new worlds');
    expect(html).toContain('match-row-yes');
  });

  it('renders NO MATCH and a note for a year mismatch', () => {
    const html = matchTesterResultHTML({
      matched: false,
      input_norm: 'dune',
      candidates: [
        { norm: 'dune', matched: false, title_matched: true, distance: 0, year: 1984 },
      ],
    });
    expect(html).toContain('NO MATCH');
    expect(html).toContain('match-no');
    expect(html).toContain('year mismatch');
  });

  it('flags a punctuation-only near-miss', () => {
    const html = matchTesterResultHTML({
      matched: false,
      input_norm: 'star trek strange new worlds',
      candidates: [
        { norm: 'star trek: strange new worlds', matched: false, punctuation_only: true, distance: 1, year: 0 },
      ],
    });
    expect(html).toContain('punctuation-only diff');
  });

  it('caps the non-matching rows and reports the hidden count', () => {
    const candidates = [];
    for (let i = 0; i < 20; i++) {
      candidates.push({ norm: 'cand' + i, matched: false, distance: i + 1, year: 0 });
    }
    const html = matchTesterResultHTML({ matched: false, input_norm: 'x', candidates });
    expect(html).toContain('5 more non-matching candidate');
  });

  it('handles an empty candidate list', () => {
    const html = matchTesterResultHTML({ matched: false, input_norm: 'x', candidates: [] });
    expect(html).toContain('No candidates');
  });

  it('escapes candidate titles', () => {
    const html = matchTesterResultHTML({
      matched: false, input_norm: 'x',
      candidates: [{ norm: '<script>', matched: false, distance: 1, year: 0 }],
    });
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;script&gt;');
  });
});

describe('listCacheBuckets', () => {
  it('returns only resolved title-list caches', () => {
    setNavItems([
      { bucket: 'series', section: 'trackers' },
      { bucket: 'cache_series_list', label: 'Series Title List Cache', section: 'caches' },
      { bucket: 'cache_movies_list', label: 'Movies Title List Cache', section: 'caches' },
      { bucket: 'cache_tvdb', label: 'TVDB Cache', section: 'caches' },
    ]);
    const got = listCacheBuckets().map(b => b.bucket);
    expect(got).toEqual(['cache_series_list', 'cache_movies_list']);
  });
});
