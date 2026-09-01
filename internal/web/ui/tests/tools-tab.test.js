/**
 * Tests for the Tools-tab wiring in database.js: the TOOLS registry, the
 * sidebar renderer, and selectTool's dispatch. The individual tool renderers
 * are covered by their own tests; here we replace them with spies so we test
 * only the tab plumbing.
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'database.js'), 'utf8');

function load() {
  const prelude = `function esc(s){return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}\n`;
  const els = {};
  const document = {
    getElementById: (id) => (els[id] ||= { id, innerHTML: '' }),
  };
  const mod = new Function('exports', 'document', prelude + src + `
    exports.TOOLS = TOOLS;
    exports.selectTool = selectTool;
    exports.renderToolsSidebar = renderToolsSidebar;
    exports.loadToolsTab = loadToolsTab;
    exports.getActive = () => toolsActiveId;
  `);
  const exports = {};
  mod(exports, document);
  return { ...exports, els };
}

describe('Tools tab registry', () => {
  it('lists the seven tools with stable ids', () => {
    const { TOOLS } = load();
    expect(TOOLS.map(t => t.id)).toEqual([
      'match_tester', 'quality_tester', 'mark_downloaded', 'trace_search',
      'download_history', 'stale_favorites', 'failure_log',
    ]);
    for (const t of TOOLS) {
      expect(typeof t.render).toBe('function');
      expect(t.label.length).toBeGreaterThan(0);
    }
  });
});

describe('renderToolsSidebar', () => {
  it('renders a button per tool under a Tools header and marks the active one', () => {
    const api = load();
    api.TOOLS.forEach(t => { t.render = () => {}; });
    api.selectTool('trace_search');
    const html = api.els['tools-sidebar'].innerHTML;
    expect(html).toContain('Tools');
    for (const t of api.TOOLS) expect(html).toContain(t.label);
    // The active tool's button carries the active class.
    const activeIdx = html.indexOf('🧭 Trace search');
    const activeBtn = html.lastIndexOf('db-nav-btn active', activeIdx);
    expect(activeBtn).toBeGreaterThanOrEqual(0);
  });
});

describe('selectTool', () => {
  let api;
  beforeEach(() => {
    api = load();
    api.calls = [];
    api.TOOLS.forEach(t => { t.render = () => api.calls.push(t.id); });
  });

  it('dispatches to the chosen tool and records it active', () => {
    api.selectTool('failure_log');
    expect(api.getActive()).toBe('failure_log');
    expect(api.calls).toEqual(['failure_log']);
  });

  it('falls back to the first tool for an unknown id', () => {
    api.selectTool('nope');
    expect(api.getActive()).toBe('match_tester');
    expect(api.calls).toEqual(['match_tester']);
  });

  it('loadToolsTab opens the first tool by default, then remembers the last', () => {
    api.loadToolsTab();
    expect(api.getActive()).toBe('match_tester');
    api.selectTool('stale_favorites');
    api.loadToolsTab();
    expect(api.getActive()).toBe('stale_favorites');
  });
});
