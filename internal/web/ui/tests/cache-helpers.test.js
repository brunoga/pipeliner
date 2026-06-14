/**
 * Tests for the cache renderer helpers added to database.js:
 *   - cacheValuePreview: one-line shape preview for arbitrary cache values
 *   - cacheExpiryLabel: relative TTL label ("in 3d 4h", "expired 2h ago")
 *
 * The helpers are pure functions, so we extract them via a Function
 * constructor and exercise them directly — no DOM or fetch needed.
 */

import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let cacheValuePreview, cacheExpiryLabel;

beforeAll(() => {
  const mod = new Function('exports', src + `
    exports.cacheValuePreview = cacheValuePreview;
    exports.cacheExpiryLabel  = cacheExpiryLabel;
  `);
  const exports = {};
  mod(exports);
  cacheValuePreview = exports.cacheValuePreview;
  cacheExpiryLabel  = exports.cacheExpiryLabel;
});

describe('cacheValuePreview', () => {
  it('renders empty-set marker for null and undefined', () => {
    expect(cacheValuePreview(null)).toBe('∅');
    expect(cacheValuePreview(undefined)).toBe('∅');
  });

  it('renders array length for arrays', () => {
    expect(cacheValuePreview([])).toBe('[0 items]');
    expect(cacheValuePreview([1])).toBe('[1 item]');
    expect(cacheValuePreview([1, 2, 3])).toBe('[3 items]');
  });

  it('renders top-level keys for objects, capped at four', () => {
    expect(cacheValuePreview({})).toBe('{}');
    expect(cacheValuePreview({a: 1, b: 2})).toBe('{a, b}');
    expect(cacheValuePreview({a: 1, b: 2, c: 3, d: 4, e: 5})).toBe('{a, b, c, d, …}');
  });

  it('returns scalars as-is, truncating long strings', () => {
    expect(cacheValuePreview('short')).toBe('short');
    expect(cacheValuePreview(42)).toBe('42');
    expect(cacheValuePreview(true)).toBe('true');
    const long = 'x'.repeat(200);
    const out = cacheValuePreview(long);
    expect(out.length).toBe(118); // 117 + ellipsis
    expect(out.endsWith('…')).toBe(true);
  });
});

describe('cacheExpiryLabel', () => {
  // Freeze the clock so the function under test sees the same "now" the
  // test used to build its ISO timestamps — otherwise even a few ms of
  // setup time can floor 4560min down to 4559min and flip "3d 4h" to
  // "3d 3h" (this used to flake in CI).
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-01-01T00:00:00Z')); });
  afterEach(() => { vi.useRealTimers(); });

  it('returns em-dash for missing or invalid timestamps', () => {
    expect(cacheExpiryLabel(null)).toBe('—');
    expect(cacheExpiryLabel('')).toBe('—');
    expect(cacheExpiryLabel('not-a-date')).toBe('—');
  });

  it('formats future times as "in …"', () => {
    const now = Date.now();
    const future = new Date(now + 3 * 24 * 3600 * 1000 + 4 * 3600 * 1000).toISOString();
    expect(cacheExpiryLabel(future)).toBe('in 3d 4h');
  });

  it('formats past times as "expired … ago"', () => {
    const now = Date.now();
    const past = new Date(now - 2 * 3600 * 1000 - 15 * 60 * 1000).toISOString();
    expect(cacheExpiryLabel(past)).toBe('expired 2h 15m ago');
  });

  it('uses minutes for sub-hour deltas', () => {
    const future = new Date(Date.now() + 12 * 60 * 1000).toISOString();
    expect(cacheExpiryLabel(future)).toBe('in 12m');
  });

  it('uses <1m sentinel for sub-minute deltas', () => {
    const future = new Date(Date.now() + 30 * 1000).toISOString();
    expect(cacheExpiryLabel(future)).toBe('in <1m');
  });
});
