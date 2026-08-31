/**
 * Tests for the quality tester renderer in database.js:
 *   - qualityTesterResultHTML: pure SpecResult → HTML renderer
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let qualityTesterResultHTML;

beforeAll(() => {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const mod = new Function('exports', prelude + src + `
    exports.qualityTesterResultHTML = qualityTesterResultHTML;
  `);
  const exports = {};
  mod(exports);
  qualityTesterResultHTML = exports.qualityTesterResultHTML;
});

describe('qualityTesterResultHTML', () => {
  it('shows only the detected quality when no spec is given', () => {
    const html = qualityTesterResultHTML({ quality: '1080p WEB-DL', dimensions: [] }, '');
    expect(html).toContain('1080p WEB-DL');
    expect(html).not.toContain('MATCH');
  });

  it('renders MATCH with the spec', () => {
    const html = qualityTesterResultHTML({
      quality: '1080p WEB-DL', spec: '720p-1080p', matched: true,
      dimensions: [{ name: 'resolution', constrained: true, passed: true, constraint: '720p-1080p', value: '1080p' }],
    }, '720p-1080p');
    expect(html).toContain('MATCH');
    expect(html).toContain('match-yes');
  });

  it('renders NO MATCH and marks the failing dimension', () => {
    const html = qualityTesterResultHTML({
      quality: '720p WEB-DL', spec: '1080p+', matched: false,
      dimensions: [
        { name: 'resolution', constrained: true, passed: false, constraint: '1080p+', value: '720p' },
        { name: 'source', constrained: false, passed: true, constraint: '', value: 'web-dl' },
      ],
    }, '1080p+');
    expect(html).toContain('NO MATCH');
    expect(html).toContain('match-no');
    expect(html).toContain('match-row-no');
    expect(html).toContain('does not satisfy constraint');
    // Unconstrained dimensions are hidden from the table.
    expect(html).not.toContain('>source<');
  });

  it('notes a bypassed optional dimension', () => {
    const html = qualityTesterResultHTML({
      quality: '1080p', spec: '1080p webrip?', matched: true,
      dimensions: [
        { name: 'resolution', constrained: true, passed: true, constraint: '1080p', value: '1080p' },
        { name: 'source', constrained: true, passed: true, bypassed: true, constraint: 'webrip?', value: 'unknown' },
      ],
    }, '1080p webrip?');
    expect(html).toContain('bypassed');
  });

  it('handles a spec that constrains nothing', () => {
    const html = qualityTesterResultHTML({
      quality: '1080p', spec: '', matched: true, dimensions: [],
    }, '   '); // whitespace-only spec still counts as "has spec" from caller's view
    // Caller passes the trimmed spec; here we exercise the empty-constraint path.
    expect(html).toContain('no constraints');
  });
});
