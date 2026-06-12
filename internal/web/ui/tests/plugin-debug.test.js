/**
 * Tests for the plugin-debug settings panel (plugin-debug.js).
 *
 * The panel:
 *   - calls /api/plugins + /api/log-debug-plugins on tab open
 *   - renders a grouped, sorted checkbox list
 *   - PUTs the new set on every toggle and updates local state from the response
 *   - reverts the UI and shows an error when PUT fails
 *
 * These tests stub fetch and the DOM elements the script reaches into.
 */

import { describe, it, expect, beforeAll, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'plugin-debug.js'), 'utf8');

let loadPluginDebugSettings, togglePluginDebug, filterPluginDebug, renderPluginDebugList;
let dom, fetchCalls;

beforeAll(() => {
  // Stub globals the script reaches into. esc is defined in dashboard.js;
  // here we provide a minimal shim that escapes HTML quote and tag chars.
  const setup = `
    function esc(s) {
      return String(s)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;').replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }
  `;
  const mod = new Function('exports', 'document', 'fetch', 'setTimeout', 'clearTimeout',
    setup + src + `
      exports.loadPluginDebugSettings = loadPluginDebugSettings;
      exports.togglePluginDebug = togglePluginDebug;
      exports.filterPluginDebug = filterPluginDebug;
      exports.renderPluginDebugList = renderPluginDebugList;
      exports._reset = () => { pdLoaded = false; pdPlugins = []; pdEnabled = new Set(); pdFilter = ''; };
    `);
  const exports = {};

  // DOM shim — only the nodes the script touches.
  function node() {
    const n = {
      innerHTML: '', textContent: '', style: { display: '' },
      classList: { _set: new Set(),
        add(c) { this._set.add(c); }, remove(c) { this._set.delete(c); },
        toggle(c, on) {
          if (on === undefined) this._set.has(c) ? this._set.delete(c) : this._set.add(c);
          else if (on) this._set.add(c); else this._set.delete(c);
        },
        contains(c) { return this._set.has(c); },
      },
      closest() { return null },
    };
    return n;
  }
  dom = {
    'plugin-debug-list': node(),
    'plugin-debug-status': node(),
  };
  const documentStub = {
    getElementById(id) { return dom[id] || null; },
  };

  // fetch stub — records calls and resolves with whatever the test pushes.
  fetchCalls = [];
  let nextResponses = [];
  globalThis._pdSetFetchResponses = (rs) => { nextResponses = rs.slice(); };
  const fetchStub = (url, init) => {
    fetchCalls.push({url, init});
    const r = nextResponses.shift();
    if (!r) return Promise.reject(new Error('no response queued for ' + url));
    return Promise.resolve({
      ok: r.ok !== false,
      status: r.status || 200,
      json: () => Promise.resolve(r.body),
    });
  };

  mod(exports, documentStub, fetchStub, setTimeout, clearTimeout);
  loadPluginDebugSettings = exports.loadPluginDebugSettings;
  togglePluginDebug       = exports.togglePluginDebug;
  filterPluginDebug       = exports.filterPluginDebug;
  renderPluginDebugList   = exports.renderPluginDebugList;
  globalThis._pdReset = exports._reset;
});

beforeEach(() => {
  globalThis._pdReset();
  fetchCalls.length = 0;
  dom['plugin-debug-list'].innerHTML = '';
  dom['plugin-debug-status'].textContent = '';
  dom['plugin-debug-status'].classList._set.clear();
});

describe('loadPluginDebugSettings', () => {
  it('fetches plugins + current debug set, renders the list grouped by role', async () => {
    globalThis._pdSetFetchResponses([
      { body: [
        {name: 'rss', role: 'source', description: 'rss source'},
        {name: 'metainfo_bluray', role: 'processor', description: 'bluray enricher'},
        {name: 'transmission', role: 'sink', description: 'transmission sink'},
        {name: 'metainfo_tvdb', role: 'processor', description: 'tvdb enricher'},
      ]},
      { body: { plugins: ['metainfo_bluray'] }},
    ]);
    await loadPluginDebugSettings();

    expect(fetchCalls[0].url).toBe('/api/plugins');
    expect(fetchCalls[1].url).toBe('/api/log-debug-plugins');

    const html = dom['plugin-debug-list'].innerHTML;
    // Section headers in role order.
    expect(html.indexOf('source')).toBeGreaterThan(-1);
    expect(html.indexOf('processor')).toBeGreaterThan(html.indexOf('source'));
    expect(html.indexOf('sink')).toBeGreaterThan(html.indexOf('processor'));
    // Plugins sorted alphabetically WITHIN role (metainfo_bluray before metainfo_tvdb).
    expect(html.indexOf('metainfo_bluray')).toBeLessThan(html.indexOf('metainfo_tvdb'));
    // The already-enabled plugin has its checkbox pre-checked and active class.
    const bluraySnippet = html.slice(html.indexOf('metainfo_bluray') - 200,
                                    html.indexOf('metainfo_bluray') + 50);
    expect(bluraySnippet).toContain('checked');
    expect(bluraySnippet).toContain('plugin-debug-row-active');
  });

  it('hides the section when the backend returns 501 (not wired)', async () => {
    // section.closest() returns null in our DOM stub, so we just confirm
    // we don't try to render plugins on 501. Add a fake parent that returns
    // a section node we can inspect.
    const section = { style: { display: '' } };
    dom['plugin-debug-list'].closest = (sel) => sel === '.settings-section' ? section : null;

    globalThis._pdSetFetchResponses([
      { body: [{name: 'x', role: 'processor', description: ''}] },
      { ok: false, status: 501, body: null },
    ]);
    await loadPluginDebugSettings();

    expect(section.style.display).toBe('none');
  });
});

describe('togglePluginDebug', () => {
  it('PUTs the new set and updates local state from the response', async () => {
    // Bootstrap with one already-enabled plugin so we test the add path.
    globalThis._pdSetFetchResponses([
      { body: [
        {name: 'metainfo_bluray', role: 'processor', description: 'b'},
        {name: 'metainfo_tvdb', role: 'processor', description: 't'},
      ]},
      { body: { plugins: ['metainfo_bluray'] }},
    ]);
    await loadPluginDebugSettings();

    // Toggle metainfo_tvdb on. Server canonicalises (sorts) and echoes back.
    globalThis._pdSetFetchResponses([
      { body: { plugins: ['metainfo_bluray', 'metainfo_tvdb'] }},
    ]);
    await togglePluginDebug('metainfo_tvdb', true);

    const put = fetchCalls[2];
    expect(put.url).toBe('/api/log-debug-plugins');
    expect(put.init.method).toBe('PUT');
    expect(JSON.parse(put.init.body)).toEqual({plugins: ['metainfo_bluray', 'metainfo_tvdb']});

    // Both plugins now appear with active class.
    const html = dom['plugin-debug-list'].innerHTML;
    expect(html.match(/plugin-debug-row-active/g)?.length).toBe(2);
    expect(dom['plugin-debug-status'].textContent).toContain('enabled debug for metainfo_tvdb');
  });

  it('reverts the local set and surfaces an error on PUT failure', async () => {
    globalThis._pdSetFetchResponses([
      { body: [{name: 'metainfo_bluray', role: 'processor', description: 'b'}] },
      { body: { plugins: [] }},
    ]);
    await loadPluginDebugSettings();

    // Server returns 500 on PUT.
    globalThis._pdSetFetchResponses([
      { ok: false, status: 500, body: null },
    ]);
    await togglePluginDebug('metainfo_bluray', true);

    // Row must NOT be marked active after the revert.
    const html = dom['plugin-debug-list'].innerHTML;
    expect(html).not.toContain('plugin-debug-row-active');
    expect(dom['plugin-debug-status'].textContent).toContain('error');
    expect(dom['plugin-debug-status'].classList._set.has('plugin-debug-status-error')).toBe(true);
  });
});

describe('filterPluginDebug', () => {
  it('matches on name, description, or role', async () => {
    globalThis._pdSetFetchResponses([
      { body: [
        {name: 'rss', role: 'source', description: 'rss feed'},
        {name: 'metainfo_bluray', role: 'processor', description: 'bluray catalog'},
        {name: 'transmission', role: 'sink', description: 'BitTorrent client'},
      ]},
      { body: { plugins: [] }},
    ]);
    await loadPluginDebugSettings();

    filterPluginDebug('blu');
    let html = dom['plugin-debug-list'].innerHTML;
    expect(html).toContain('metainfo_bluray');
    expect(html).not.toContain('rss');
    expect(html).not.toContain('transmission');

    filterPluginDebug('sink');
    html = dom['plugin-debug-list'].innerHTML;
    expect(html).toContain('transmission');
    expect(html).not.toContain('rss');
    expect(html).not.toContain('metainfo_bluray');

    filterPluginDebug('feed'); // description match
    html = dom['plugin-debug-list'].innerHTML;
    expect(html).toContain('rss');
    expect(html).not.toContain('metainfo_bluray');

    filterPluginDebug('');
    html = dom['plugin-debug-list'].innerHTML;
    expect(html).toContain('rss');
    expect(html).toContain('metainfo_bluray');
    expect(html).toContain('transmission');
  });
});
