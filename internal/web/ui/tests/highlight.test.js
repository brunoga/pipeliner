/**
 * Tests for the Starlark syntax highlighter (highlight.js).
 * The highlighter is a pure function so we can import its logic directly.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

// Load and execute highlight.js in this module scope.
const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'highlight.js'), 'utf8');

// Extract the two functions we need by evaluating the source.
// The file uses plain 'use strict' globals so we wrap it.
let highlightStarlark, hlStarlarkLine;
beforeAll(() => {
  const mod = new Function(
    'exports',
    src + '\nexports.highlightStarlark = highlightStarlark;\nexports.hlStarlarkLine = hlStarlarkLine;'
  );
  const exports = {};
  mod(exports);
  highlightStarlark = exports.highlightStarlark;
  hlStarlarkLine    = exports.hlStarlarkLine;
});

describe('highlightStarlark', () => {
  it('produces a non-empty string for non-empty input', () => {
    const out = highlightStarlark('task("t", [])');
    expect(typeof out).toBe('string');
    expect(out.length).toBeGreaterThan(0);
  });

  it('ends with a newline', () => {
    expect(highlightStarlark('x = 1')).toMatch(/\n$/);
  });

  it('wraps comment lines in y-comment span', () => {
    const out = highlightStarlark('# hello world');
    expect(out).toContain('class="y-comment"');
    expect(out).toContain('hello world');
  });

  it('highlights keywords in y-key span', () => {
    const out = highlightStarlark('def my_func():');
    expect(out).toContain('class="y-key"');
    expect(out).toContain('def');
  });

  it('highlights built-ins (plugin, task, env) in y-builtin span', () => {
    const out = highlightStarlark('plugin("rss", url="x")');
    expect(out).toContain('class="y-builtin"');
  });

  it('highlights True/False/None in y-bool span', () => {
    for (const kw of ['True', 'False', 'None']) {
      const out = highlightStarlark(`x = ${kw}`);
      expect(out).toContain('class="y-bool"');
    }
  });

  it('highlights string literals in y-str span', () => {
    const out = highlightStarlark('url = "https://example.com"');
    expect(out).toContain('class="y-str"');
  });

  it('highlights numbers in y-num span', () => {
    const out = highlightStarlark('port = 8080');
    expect(out).toContain('class="y-num"');
    expect(out).toContain('8080');
  });

  it('handles multiple lines', () => {
    const out = highlightStarlark('# comment\ndef f():\n    pass');
    const lines = out.split('\n');
    expect(lines.length).toBeGreaterThanOrEqual(3);
  });

  it('escapes HTML special characters', () => {
    const out = highlightStarlark('x = "<script>"');
    expect(out).not.toContain('<script>');
    expect(out).toContain('&lt;');
    expect(out).toContain('&gt;');
  });

  it('handles empty string without throwing', () => {
    expect(() => highlightStarlark('')).not.toThrow();
  });

  it('handles triple-quoted string spanning two lines', () => {
    const out = highlightStarlark('x = """\nhello\n"""');
    expect(out).toContain('class="y-str"');
  });
});
