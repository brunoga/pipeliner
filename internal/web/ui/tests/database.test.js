/**
 * Tests for the database tab fixes:
 *   - series rows delete by the API-provided stored key (not a client-side
 *     "displayName|episode" reconstruction, which never matches the
 *     normalized stored keys)
 *   - whole-show deletion goes through the server-side endpoint with the
 *     normalized series_name (client-side key collection only ever saw the
 *     current page)
 *   - per-row deletes ask for confirmation and preserve filter/page state
 *   - bucket switches reset both the filter state and the visible input text
 *   - a11y: show rows are keyboard-operable, destructive buttons are labeled
 *   - movie year is HTML-escaped
 *
 * database.js is loaded via a Function constructor (same pattern as
 * cache-helpers.test.js); a tail re-exports the functions under test plus a
 * stub() hook that reassigns internal collaborators (fetchDBPage, dbShowError,
 * …) so the async flows can run without a real DOM or server.
 */

import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

let mod; // exports of a fresh module instance per test

function loadModule() {
  const factory = new Function('exports', src + `
    exports.renderSeriesTable    = renderSeriesTable;
    exports.renderMoviesTable    = renderMoviesTable;
    exports.dbDeleteEntryRequest = dbDeleteEntryRequest;
    exports.dbDeleteShowRequest  = dbDeleteShowRequest;
    exports.dbDeleteEntry        = dbDeleteEntry;
    exports.dbDeleteShow         = dbDeleteShow;
    exports.dbShowRowKeydown     = dbShowRowKeydown;
    exports.toggleEps            = toggleEps;
    exports.selectDBBucket       = selectDBBucket;
    exports.state = () => ({dbFilterQuery, dbCurrentCursor, dbCursorStack: dbCursorStack.slice(), dbActiveBucket});
    exports.setState = (s) => {
      if ('dbFilterQuery'   in s) dbFilterQuery   = s.dbFilterQuery;
      if ('dbCurrentCursor' in s) dbCurrentCursor = s.dbCurrentCursor;
      if ('dbCursorStack'   in s) dbCursorStack   = s.dbCursorStack;
      if ('dbActiveBucket'  in s) dbActiveBucket  = s.dbActiveBucket;
    };
    exports.stub = (name, fn) => {
      if (name === 'dbRefreshBucket') dbRefreshBucket = fn;
      else if (name === 'dbShowError')     dbShowError     = fn;
      else if (name === 'renderDBSidebar') renderDBSidebar = fn;
      else if (name === 'fetchDBPage')     fetchDBPage     = fn;
      else if (name === 'loadDBSidebar')   loadDBSidebar   = fn;
      else throw new Error('unknown stub: ' + name);
    };
  `);
  const exports = {};
  factory(exports);
  return exports;
}

beforeAll(() => {
  // esc lives in dashboard.js in the real page; provide the same minimal impl.
  globalThis.esc = (s) => String(s ?? '')
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
});

beforeEach(() => {
  mod = loadModule();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// ── series table rendering ────────────────────────────────────────────────────

const showFixture = {
  name: 'Breaking Bad',          // display name (original casing)
  series_name: 'breaking bad',   // normalized key material
  episodes: [
    {key: 'breaking bad|S01E01', episode_id: 'S01E01', quality: '720p', downloaded_at: ''},
  ],
};

describe('renderSeriesTable', () => {
  it('deletes episodes by the stored key from the API, not a display-name reconstruction', () => {
    const html = mod.renderSeriesTable([showFixture], 'series');
    expect(html).toContain('breaking bad|S01E01');
    // The old buggy construction: display name + '|' + episode id.
    expect(html).not.toContain('Breaking Bad|S01E01');
  });

  it('passes the normalized series_name (and the display label) to dbDeleteShow', () => {
    const html = mod.renderSeriesTable([showFixture], 'series');
    expect(html).toContain('dbDeleteShow(&quot;breaking bad&quot;,&quot;Breaking Bad&quot;)');
  });

  it('makes show rows keyboard-operable', () => {
    const html = mod.renderSeriesTable([showFixture], 'series');
    expect(html).toContain('role="button"');
    expect(html).toContain('tabindex="0"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain('onkeydown="dbShowRowKeydown(');
  });

  it('labels the destructive buttons for screen readers', () => {
    const html = mod.renderSeriesTable([showFixture], 'series');
    expect(html).toContain('aria-label="Delete all episodes of Breaking Bad"');
    expect(html).toContain('aria-label="Delete Breaking Bad S01E01"');
  });
});

describe('renderMoviesTable', () => {
  it('escapes the year value', () => {
    const html = mod.renderMoviesTable([
      {key: 'evil', value: {title: 'Evil', year: '<img src=x onerror=alert(1)>'}},
    ], 'movies');
    expect(html).not.toContain('<img src=x');
    expect(html).toContain('&lt;img src=x');
  });

  it('still renders a numeric year and the em-dash fallback', () => {
    const html = mod.renderMoviesTable([
      {key: 'a', value: {title: 'A', year: 2009}},
      {key: 'b', value: {title: 'B'}},
    ], 'movies');
    expect(html).toContain('2009');
    expect(html).toContain('—');
  });

  it('labels the delete button with the movie title', () => {
    const html = mod.renderMoviesTable([{key: 'a', value: {title: 'Avatar'}}], 'movies');
    expect(html).toContain('aria-label="Delete Avatar"');
  });
});

// ── request builders ──────────────────────────────────────────────────────────

describe('dbDeleteEntryRequest', () => {
  it('targets the entries endpoint with the key in the JSON body', () => {
    const {url, options} = mod.dbDeleteEntryRequest('series', 'breaking bad|S01E01');
    expect(url).toBe('/api/db/entries/series');
    expect(options.method).toBe('DELETE');
    expect(JSON.parse(options.body)).toEqual({key: 'breaking bad|S01E01'});
  });

  it('URL-encodes the bucket name', () => {
    const {url} = mod.dbDeleteEntryRequest('seen:my task', 'k');
    expect(url).toBe('/api/db/entries/seen%3Amy%20task');
  });
});

describe('dbDeleteShowRequest', () => {
  it('targets the server-side show endpoint with the normalized name in the body', () => {
    const {url, options} = mod.dbDeleteShowRequest('breaking bad');
    expect(url).toBe('/api/db/series/show');
    expect(options.method).toBe('DELETE');
    expect(JSON.parse(options.body)).toEqual({series_name: 'breaking bad'});
  });

  it('sends an empty series_name for the unrecognized-entries group', () => {
    const {options} = mod.dbDeleteShowRequest('');
    expect(JSON.parse(options.body)).toEqual({series_name: ''});
  });
});

// ── delete flows: confirmation + state preservation ───────────────────────────

describe('dbDeleteEntry', () => {
  it('does nothing when the confirmation is declined', async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal('confirm', vi.fn(() => false));
    vi.stubGlobal('fetch', fetchSpy);
    await mod.dbDeleteEntry('series', 'k');
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('deletes and refreshes in place (filter/page state preserved) when confirmed', async () => {
    const refresh = vi.fn();
    mod.stub('dbRefreshBucket', refresh);
    vi.stubGlobal('confirm', vi.fn(() => true));
    vi.stubGlobal('fetch', vi.fn(async () => ({ok: true})));
    mod.setState({dbActiveBucket: 'series', dbFilterQuery: 'bad', dbCurrentCursor: 'Dark', dbCursorStack: ['']});

    await mod.dbDeleteEntry('series', 'breaking bad|S01E01');

    expect(globalThis.fetch).toHaveBeenCalledWith('/api/db/entries/series', expect.objectContaining({method: 'DELETE'}));
    expect(refresh).toHaveBeenCalledWith('series');
    // In-place refresh must not reset the user's context.
    const s = mod.state();
    expect(s.dbFilterQuery).toBe('bad');
    expect(s.dbCurrentCursor).toBe('Dark');
    expect(s.dbCursorStack).toEqual(['']);
  });

  it('surfaces server errors instead of refreshing', async () => {
    const refresh = vi.fn();
    const showError = vi.fn();
    mod.stub('dbRefreshBucket', refresh);
    mod.stub('dbShowError', showError);
    vi.stubGlobal('confirm', vi.fn(() => true));
    vi.stubGlobal('fetch', vi.fn(async () => ({ok: false, text: async () => 'boom'})));

    await mod.dbDeleteEntry('series', 'k');

    expect(refresh).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('boom');
  });
});

describe('dbDeleteShow', () => {
  it('confirms with the display label, then deletes via the show endpoint', async () => {
    const refresh = vi.fn();
    mod.stub('dbRefreshBucket', refresh);
    const confirmSpy = vi.fn(() => true);
    vi.stubGlobal('confirm', confirmSpy);
    vi.stubGlobal('fetch', vi.fn(async () => ({ok: true})));

    await mod.dbDeleteShow('breaking bad', 'Breaking Bad');

    expect(confirmSpy.mock.calls[0][0]).toContain('"Breaking Bad"');
    expect(globalThis.fetch).toHaveBeenCalledTimes(1);
    const [url, options] = globalThis.fetch.mock.calls[0];
    expect(url).toBe('/api/db/series/show');
    expect(JSON.parse(options.body)).toEqual({series_name: 'breaking bad'});
    expect(refresh).toHaveBeenCalledWith('series');
  });

  it('does nothing when declined', async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal('confirm', vi.fn(() => false));
    vi.stubGlobal('fetch', fetchSpy);
    await mod.dbDeleteShow('breaking bad', 'Breaking Bad');
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});

// ── bucket switch resets filter state AND input text ──────────────────────────

describe('selectDBBucket', () => {
  it('resets filter/pagination state and clears the visible input text', async () => {
    mod.stub('renderDBSidebar', vi.fn());
    mod.stub('fetchDBPage', vi.fn(async () => {}));
    const input = {value: 'stale filter'};
    vi.stubGlobal('document', {getElementById: (id) => (id === 'db-filter-input' ? input : null)});
    mod.setState({dbFilterQuery: 'stale filter', dbCurrentCursor: 'x', dbCursorStack: ['', 'x']});

    await mod.selectDBBucket('movies');

    const s = mod.state();
    expect(s.dbActiveBucket).toBe('movies');
    expect(s.dbFilterQuery).toBe('');
    expect(s.dbCurrentCursor).toBe('');
    expect(s.dbCursorStack).toEqual([]);
    expect(input.value).toBe('');
  });
});

// ── keyboard expansion ────────────────────────────────────────────────────────

describe('dbShowRowKeydown / toggleEps', () => {
  function fakeToggleTarget() {
    const classes = new Set();
    return {
      classList: {
        toggle: (c) => classes.has(c) ? classes.delete(c) : classes.add(c),
        contains: (c) => classes.has(c),
      },
    };
  }

  it('Enter and Space toggle the episode list and sync aria-expanded', () => {
    const eps = fakeToggleTarget();
    const chv = {textContent: '▸'};
    vi.stubGlobal('document', {
      getElementById: (id) => (id === 'eps-x' ? eps : id === 'chv-eps-x' ? chv : null),
    });
    const row = {attrs: {}, setAttribute(k, v) { this.attrs[k] = v; }};
    const ev = (key) => ({key, preventDefault: vi.fn()});

    const enter = ev('Enter');
    mod.dbShowRowKeydown(enter, 'eps-x', row);
    expect(enter.preventDefault).toHaveBeenCalled();
    expect(eps.classList.contains('open')).toBe(true);
    expect(row.attrs['aria-expanded']).toBe('true');
    expect(chv.textContent).toBe('▾');

    mod.dbShowRowKeydown(ev(' '), 'eps-x', row);
    expect(eps.classList.contains('open')).toBe(false);
    expect(row.attrs['aria-expanded']).toBe('false');
    expect(chv.textContent).toBe('▸');
  });

  it('ignores other keys', () => {
    const getById = vi.fn();
    vi.stubGlobal('document', {getElementById: getById});
    const ev = {key: 'a', preventDefault: vi.fn()};
    mod.dbShowRowKeydown(ev, 'eps-x', {setAttribute() {}});
    expect(ev.preventDefault).not.toHaveBeenCalled();
    expect(getById).not.toHaveBeenCalled();
  });
});
