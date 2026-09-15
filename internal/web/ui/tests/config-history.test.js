/**
 * Tests for configHistoryHTML in config-editor.js — the pure renderer for the
 * config-version history modal.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'config-editor.js'), 'utf8');

let configHistoryHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', 'document', prelude + src + `
    exports.configHistoryHTML = configHistoryHTML;
  `);
  const exports = {};
  mod(exports, { getElementById: () => null, createElement: () => ({}), body: { appendChild() {} } });
  configHistoryHTML = exports.configHistoryHTML;
});

describe('configHistoryHTML', () => {
  it('explains auto-snapshotting when empty', () => {
    expect(configHistoryHTML([])).toContain('No snapshots yet');
  });

  it('renders one row per version with a load button', () => {
    const html = configHistoryHTML([
      { id: '2026-09-15T10:00:00Z', saved_at: '2026-09-15T10:00:00Z', size: 23150 },
      { id: '2026-09-14T09:00:00Z', saved_at: '2026-09-14T09:00:00Z', size: 22800 },
    ]);
    expect((html.match(/loadConfigVersion\(/g) || []).length).toBe(2);
    expect(html).toContain('22.6 KB');
  });
});
