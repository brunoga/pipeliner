/**
 * Tests for schedBadgeHTML in dashboard.js — the schedule chip that marks
 * push-fed (webhook-driven) pipelines and shows their queue depth.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'dashboard.js'), 'utf8');

let schedBadgeHTML;

beforeAll(() => {
  const shims = `
    var dbLoaded = false;
    var dbNavItems = [];
    function renderDBSidebar() {}
  `;
  const mod = new Function('exports', 'document', 'fetch', 'setTimeout', 'clearTimeout', 'requestAnimationFrame',
    shims + src + `
    exports.schedBadgeHTML = schedBadgeHTML;
  `);
  const exports = {};
  mod(exports, { getElementById: () => null, createElement: () => ({}), addEventListener() {}, removeEventListener() {} },
    async () => ({ ok: true, json: async () => ({}) }), () => null, () => {}, cb => cb());
  schedBadgeHTML = exports.schedBadgeHTML;
});

describe('schedBadgeHTML', () => {
  it('marks push-fed pipelines instead of "manual"', () => {
    const html = schedBadgeHTML({ name: 'ondemand', queues: ['movies'] }, null);
    expect(html).toContain('push-fed');
    expect(html).toContain('/api/ingest/movies');
    expect(html).not.toContain('manual');
  });

  it('shows the queued count when items are waiting', () => {
    const html = schedBadgeHTML({ queues: ['movies'], queued: 3 }, null);
    expect(html).toContain('3 queued');
    expect(schedBadgeHTML({ queues: ['movies'], queued: 0 }, null)).not.toContain('queued');
  });

  it('stays compact — the next-run time lives in the tooltip, not the chip', () => {
    // A long chip squeezed the task name to one character per line in the
    // flex card header; the datetime is already on the "Next run" row.
    const d = new Date('2026-09-20T22:30:00Z');
    const html = schedBadgeHTML({ queues: ['3d-movies'], queued: 0 }, d);
    const chipText = html.replace(/^[^>]*>/, '').replace(/<[^>]*>/g, '');
    expect(chipText.trim()).toBe('⚡ push-fed');
    expect(html).toContain('next');           // datetime moved into title=
  });

  it('keeps the plain label for normal pipelines', () => {
    expect(schedBadgeHTML({ schedule: '1h' }, null)).toContain('1h');
    expect(schedBadgeHTML({}, null)).toContain('manual');
  });
});
