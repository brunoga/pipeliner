/**
 * Tests for failureLogHTML in database.js — the pure renderer for the failure
 * audit log tool.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let failureLogHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.failureLogHTML = failureLogHTML;
  `);
  const exports = {};
  mod(exports);
  failureLogHTML = exports.failureLogHTML;
});

describe('failureLogHTML', () => {
  it('shows a no-failures message for an empty blank search', () => {
    expect(failureLogHTML([], '')).toContain('No failures recorded');
  });

  it('shows a no-match message for an empty filtered search', () => {
    expect(failureLogHTML([], 'deluge')).toContain('No failures matching');
  });

  it('renders failures with node, title, and reason', () => {
    const html = failureLogHTML([
      { failed_at: '2026-07-01T12:00:00Z', task: 'tv', node: 'deluge_7',
        title: 'SNW S04E05 1080p', reason: 'deluge: connection refused' },
    ], '');
    expect(html).toContain('1 failure');
    expect(html).toContain('deluge_7');
    expect(html).toContain('SNW S04E05 1080p');
    expect(html).toContain('deluge: connection refused');
    expect(html).toContain('fail-reason');
  });

  it('escapes titles and reasons', () => {
    const html = failureLogHTML([
      { failed_at: '2026-07-01T12:00:00Z', task: 't', node: '', title: '<b>x</b>', reason: '<i>boom</i>' },
    ], '');
    expect(html).not.toContain('<b>x</b>');
    expect(html).toContain('&lt;b&gt;x&lt;/b&gt;');
    expect(html).toContain('&lt;i&gt;boom&lt;/i&gt;');
  });

  it('tolerates missing optional fields', () => {
    const html = failureLogHTML([{ failed_at: '2026-07-01T12:00:00Z', title: 'x' }], '');
    expect(html).toContain('x');
    expect(html).not.toContain('undefined');
  });
});
