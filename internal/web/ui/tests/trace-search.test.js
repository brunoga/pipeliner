/**
 * Tests for traceSearchResultHTML in database.js — the pure renderer for the
 * cross-run trace search results.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let traceSearchResultHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.traceSearchResultHTML = traceSearchResultHTML;
  `);
  const exports = {};
  mod(exports);
  traceSearchResultHTML = exports.traceSearchResultHTML;
});

const NOW = Date.parse('2026-08-01T13:00:00Z');

describe('traceSearchResultHTML', () => {
  it('reports when nothing matched', () => {
    expect(traceSearchResultHTML([], NOW)).toContain('No matching entries');
  });

  it('renders occurrences with state class and reason', () => {
    const html = traceSearchResultHTML([
      { task: 'tv', run_id: 'r2', at: '2026-08-01T12:30:00Z', dry_run: false,
        entry: { title: 'Star Trek Strange New Worlds S04E05 1080p', final: 'accepted' } },
      { task: 'tv', run_id: 'r1', at: '2026-08-01T12:00:00Z', dry_run: false,
        entry: { title: 'Star Trek Strange New Worlds S04E05 720p', final: 'rejected', reason: 'dedup: better copy' } },
    ], NOW);
    expect(html).toContain('2 occurrence');
    expect(html).toContain('trace-accepted');
    expect(html).toContain('trace-rejected');
    expect(html).toContain('dedup: better copy');
    expect(html).toContain('30m ago');
  });

  it('shows a DRY badge for dry runs', () => {
    const html = traceSearchResultHTML([
      { task: 'tv', run_id: 'd1', at: '2026-08-01T12:59:00Z', dry_run: true,
        entry: { title: 'Something', final: 'accepted' } },
    ], NOW);
    expect(html).toContain('trace-dry');
    expect(html).toContain('DRY');
  });

  it('escapes titles and reasons', () => {
    const html = traceSearchResultHTML([
      { task: 't', run_id: 'r', at: '2026-08-01T12:59:00Z',
        entry: { title: '<b>x</b>', final: 'failed', reason: '<i>bad</i>' } },
    ], NOW);
    expect(html).not.toContain('<b>x</b>');
    expect(html).toContain('&lt;b&gt;x&lt;/b&gt;');
    expect(html).toContain('&lt;i&gt;bad&lt;/i&gt;');
  });

  it('falls back to undecided when final is missing', () => {
    const html = traceSearchResultHTML([
      { task: 't', run_id: 'r', at: '2026-08-01T12:59:00Z', entry: { title: 'x' } },
    ], NOW);
    expect(html).toContain('undecided');
  });
});
