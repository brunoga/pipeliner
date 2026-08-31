/**
 * Tests for staleFavoritesHTML in database.js — the pure renderer for the
 * stale-favorites watchdog results.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let staleFavoritesHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.staleFavoritesHTML = staleFavoritesHTML;
  `);
  const exports = {};
  mod(exports);
  staleFavoritesHTML = exports.staleFavoritesHTML;
});

describe('staleFavoritesHTML', () => {
  it('prompts to run a pipeline when no favorites are resolved', () => {
    expect(staleFavoritesHTML({ stuck: [], favorite_count: 0 })).toContain('No resolved favorite lists');
  });

  it('shows all-clear when favorites exist but nothing is stuck', () => {
    const html = staleFavoritesHTML({ stuck: [], favorite_count: 12 });
    expect(html).toContain('All clear');
    expect(html).toContain('12 favorite');
  });

  it('flags a possible matching issue for a non-zero distance', () => {
    const html = staleFavoritesHTML({
      favorite_count: 5,
      stuck: [{
        favorite: 'star trek strange new worlds', runs: 3, nearest_distance: 1,
        last_task: 'tv', last_reason: 'series: show not in list',
        example_title: 'Star Trek Strange New Worlds S04E05 1080p',
      }],
    });
    expect(html).toContain('star trek strange new worlds');
    expect(html).toContain('seen in 3 run');
    expect(html).toContain('stale-bug');
    expect(html).toContain('possible matching');
    expect(html).toContain('series: show not in list');
  });

  it('describes a downstream reject for a zero distance', () => {
    const html = staleFavoritesHTML({
      favorite_count: 5,
      stuck: [{ favorite: 'silo', runs: 4, nearest_distance: 0, last_reason: 'quality too low' }],
    });
    expect(html).toContain('rejected downstream');
    expect(html).not.toContain('stale-bug');
  });

  it('escapes favorite names and reasons', () => {
    const html = staleFavoritesHTML({
      favorite_count: 1,
      stuck: [{ favorite: '<b>x</b>', runs: 3, nearest_distance: 0, last_reason: '<i>r</i>' }],
    });
    expect(html).not.toContain('<b>x</b>');
    expect(html).toContain('&lt;b&gt;x&lt;/b&gt;');
  });
});
