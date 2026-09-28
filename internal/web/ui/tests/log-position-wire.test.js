/**
 * The log view treats a line's position and a paging cursor as the same
 * value: it takes the pos a response carries and feeds it straight back as
 * the cursor for the next page, or compares it against the last live pos.
 *
 * The server used to marshal LinePos as an object ({file, end}) while every
 * cursor field was a "<file>:<end>" string. parseLinePosClient only accepts
 * the string form, so positionCovered() silently answered false for every
 * comparison — the guard that stops the SSE ring replaying lines /tail had
 * already rendered — and stringifying a pos into a cursor produced
 * "[object Object]", which the server rejects with 400.
 *
 * These tests pin the client to the string form.
 */

import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'dashboard.js'), 'utf8');

let parseLinePosClient, positionCovered;

beforeAll(() => {
  const mod = new Function('exports', 'document', 'window', src + `
    exports.parseLinePosClient = parseLinePosClient;
    exports.positionCovered    = positionCovered;
  `);
  const exports = {};
  mod(exports, { getElementById: () => null, addEventListener: () => {} }, {});
  ({ parseLinePosClient, positionCovered } = exports);
});

describe('parseLinePosClient', () => {
  it('parses the wire form the server emits', () => {
    expect(parseLinePosClient('2:8305176')).toEqual({ fileIdx: 2, byteEnd: 8305176 });
  });

  it('rejects an object — the shape that broke the guard', () => {
    expect(parseLinePosClient({ file: 0, end: 12 })).toBeNull();
  });

  it('rejects malformed input rather than guessing', () => {
    expect(parseLinePosClient('')).toBeNull();
    expect(parseLinePosClient('nope')).toBeNull();
    expect(parseLinePosClient(null)).toBeNull();
  });
});

describe('positionCovered', () => {
  it('treats an already-rendered position as covered', () => {
    // The exact case behind the duplicate: the SSE ring replays the newest
    // line straight after /tail rendered it.
    expect(positionCovered('0:8305176', '0:8305176')).toBe(true);
  });

  it('treats an older position as covered', () => {
    expect(positionCovered('0:8305176', '0:8000000')).toBe(true);
  });

  it('treats a newer position as not covered', () => {
    expect(positionCovered('0:8305176', '0:8400000')).toBe(false);
  });

  it('does not compare positions across files', () => {
    expect(positionCovered('0:8305176', '1:10')).toBe(false);
  });

  it('is false when either side is missing', () => {
    expect(positionCovered(null, '0:1')).toBe(false);
    expect(positionCovered('0:1', null)).toBe(false);
  });
});

describe('a cursor built from a position', () => {
  it('survives String() — the conversion buildLogUrl applies', () => {
    const posFromResponse = '0:8305176';
    expect(String(posFromResponse)).toBe('0:8305176');
    expect(parseLinePosClient(String(posFromResponse))).not.toBeNull();
  });

  it('would have been unusable as an object', () => {
    expect(String({ file: 0, end: 8305176 })).toBe('[object Object]');
    expect(parseLinePosClient(String({ file: 0, end: 8305176 }))).toBeNull();
  });
});
