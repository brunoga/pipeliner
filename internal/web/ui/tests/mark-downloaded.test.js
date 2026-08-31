/**
 * Tests for markResultHTML in database.js — the pure renderer for the
 * "Mark as downloaded" tool's server response.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let markResultHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.markResultHTML = markResultHTML;
  `);
  const exports = {};
  mod(exports);
  markResultHTML = exports.markResultHTML;
});

describe('markResultHTML', () => {
  it('renders a series key with quality', () => {
    const html = markResultHTML({
      key: 'star trek strange new worlds|S04E05', existed: false,
      record: { quality: { string: '1080p WEB-DL' } },
    });
    expect(html).toContain('Marked');
    expect(html).toContain('star trek strange new worlds|S04E05');
    expect(html).toContain('1080p WEB-DL');
    expect(html).not.toContain('already tracked');
  });

  it('notes when a record already existed', () => {
    const html = markResultHTML({ key: 'silo|S02E01', existed: true, record: {} });
    expect(html).toContain('already tracked');
  });

  it('renders a movie with year and 3D', () => {
    const html = markResultHTML({
      existed: false,
      record: { title: 'furiosa a mad max saga', year: 2024, is_3d: true, quality: { string: '1080p BluRay' } },
    });
    expect(html).toContain('furiosa a mad max saga');
    expect(html).toContain('(2024)');
    expect(html).toContain('[3D]');
    expect(html).toContain('1080p BluRay');
  });

  it('handles a movie with no year and no quality', () => {
    const html = markResultHTML({ existed: false, record: { title: 'some movie' } });
    expect(html).toContain('some movie');
    expect(html).not.toContain('undefined');
  });
});
