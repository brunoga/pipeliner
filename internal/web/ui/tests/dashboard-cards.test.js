/**
 * Tests for the task-card side of dashboard.js:
 *
 *   - relTime / nextRunLabel ("overdue" instead of a bogus future time)
 *   - run-history panel rendering + expansion state across re-renders
 *   - recent-error indicator on collapsed cards
 *   - triggerRun / runAll failure feedback and double-trigger protection
 *   - clearLog being client-side only (no /tail re-boot)
 *   - refresh(): hidden-tab early return, server uptime, run-all-dry gating
 *   - empty log console placeholder
 *
 * Same harness approach as log-history.test.js: evaluate dashboard.js
 * against a stubbed document/fetch/EventSource.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'dashboard.js'), 'utf8');

// ── tiny DOM stub ────────────────────────────────────────────────────────────

function makeNode(tag) {
  let _innerHTML = '';
  const node = {
    tagName: tag,
    className: '',
    textContent: '',
    title: '',
    disabled: false,
    hidden: false,
    style: { display: '' },
    classList: {
      _set: new Set(),
      add(...cs) { cs.forEach(c => this._set.add(c)); },
      remove(...cs) { cs.forEach(c => this._set.delete(c)); },
      toggle(c, on) {
        if (on === undefined) {
          if (this._set.has(c)) this._set.delete(c); else this._set.add(c);
        } else if (on) this._set.add(c); else this._set.delete(c);
      },
      contains(c) { return this._set.has(c); },
    },
    children: [],
    parentNode: null,
    parentElement: null,
    addEventListener() {},
    appendChild(c) { c.parentNode = this; this.children.push(c); return c; },
    insertBefore(c, ref) {
      const idx = ref ? this.children.indexOf(ref) : -1;
      c.parentNode = this;
      if (idx === -1) this.children.push(c);
      else this.children.splice(idx, 0, c);
      return c;
    },
    remove() {
      if (!this.parentNode) return;
      const i = this.parentNode.children.indexOf(this);
      if (i >= 0) this.parentNode.children.splice(i, 1);
      this.parentNode = null;
    },
    get firstChild() { return this.children[0] || null; },
  };
  Object.defineProperty(node, 'innerHTML', {
    get() { return _innerHTML; },
    set(v) { _innerHTML = v; if (v === '') node.children.length = 0; },
  });
  return node;
}

function makeDOM() {
  const elements = {
    'task-grid':          makeNode('div'),
    'header-meta':        makeNode('span'),
    'btn-run-all':        makeNode('button'),
    'btn-run-all-dry':    makeNode('button'),
    'tab-db':             makeNode('div'),
    'log-console':        makeNode('div'),
    'log-filter':         makeNode('input'),
    'log-dot':            makeNode('div'),
    'log-status-text':    makeNode('span'),
    'log-tail-pill':      makeNode('button'),
    'log-tail-pill-count': makeNode('span'),
    'log-filter-spinner': makeNode('div'),
  };
  elements['tab-db'].style.display = 'none';
  const document = {
    hidden: false,
    getElementById: id => elements[id],
    createElement: tag => makeNode(tag),
    createDocumentFragment: () => makeNode('#fragment'),
  };
  return { elements, document };
}

// ── module loader ────────────────────────────────────────────────────────────

function loadModule(fetchImpl) {
  const dom = makeDOM();
  const exports = {};
  // dashboard.js reaches into globals owned by database.js; shim them.
  const shims = `
    var dbLoaded = false;
    var dbNavItems = [];
    function renderDBSidebar() {}
  `;
  const fn = new Function(
    'exports', 'document', 'fetch', 'setTimeout', 'clearTimeout', 'requestAnimationFrame',
    shims + src + `
      exports.relTime = relTime;
      exports.nextRunLabel = nextRunLabel;
      exports.hasRecentError = hasRecentError;
      exports.historyHtml = historyHtml;
      exports.historyRowHtml = historyRowHtml;
      exports.card = card;
      exports.render = render;
      exports.toggleTaskHistory = toggleTaskHistory;
      exports.triggerRun = triggerRun;
      exports.runAll = runAll;
      exports.refresh = refresh;
      exports.clearLog = clearLog;
      exports.renderInitialTail = renderInitialTail;
      exports.appendRenderedLine = appendRenderedLine;
      exports.state = () => veLog;
      exports.pendingTriggers = () => _pendingTriggers;
      exports.expandedHistory = () => _expandedHistory;
    `
  );
  fn(exports, dom.document, fetchImpl, () => null, () => {}, cb => cb());
  return { exports, dom };
}

function jsonResp(body) {
  return { ok: true, status: 200, json: async () => body, text: async () => JSON.stringify(body) };
}

function makeBtn(label) {
  const btn = makeNode('button');
  btn.textContent = label;
  btn.parentElement = { querySelectorAll: () => [btn] };
  return btn;
}

// ── relTime / nextRunLabel ───────────────────────────────────────────────────

describe('relTime', () => {
  const { exports } = loadModule(async () => jsonResp({}));
  const now = 1_000_000_000_000;

  it('formats seconds, minutes, hours, days', () => {
    expect(exports.relTime(new Date(now - 30 * 1000), now)).toBe('30s');
    expect(exports.relTime(new Date(now - 10 * 60 * 1000), now)).toBe('10m');
    expect(exports.relTime(new Date(now - 3 * 3600 * 1000), now)).toBe('3h');
    expect(exports.relTime(new Date(now - 3 * 86400 * 1000), now)).toBe('3d');
  });
});

describe('nextRunLabel', () => {
  const { exports } = loadModule(async () => jsonResp({}));
  const now = 1_000_000_000_000;

  it('returns em-dash for no next run', () => {
    expect(exports.nextRunLabel(null, now)).toBe('—');
  });

  it('returns "in Xs" for a future run', () => {
    expect(exports.nextRunLabel(new Date(now + 30 * 1000), now)).toBe('in 30s');
  });

  it('returns "overdue" for a next run in the past (not "in 30s")', () => {
    expect(exports.nextRunLabel(new Date(now - 30 * 1000), now)).toBe('overdue');
  });
});

// ── history rendering ────────────────────────────────────────────────────────

const runOK   = { at: new Date(Date.now() - 60_000).toISOString(), accepted: 2, rejected: 1, failed: 0, undecided: 3, duration: '1.5s' };
const runFail = { at: new Date(Date.now() - 7_200_000).toISOString(), accepted: 0, rejected: 0, failed: 4, undecided: 0, duration: '0.2s', err: 'connect: <refused>' };
const runDry  = { at: new Date(Date.now() - 120_000).toISOString(), accepted: 5, rejected: 0, failed: 0, undecided: 0, duration: '2s', dry_run: true };

describe('history rendering helpers', () => {
  const { exports } = loadModule(async () => jsonResp({}));

  it('hasRecentError only checks the newest 5 runs', () => {
    expect(exports.hasRecentError([runOK, runOK, runFail])).toBe(true);
    expect(exports.hasRecentError([runOK, runOK, runOK, runOK, runOK, runFail])).toBe(false);
    expect(exports.hasRecentError([])).toBe(false);
  });

  it('renders counts, duration, and relative time with absolute title', () => {
    const html = exports.historyRowHtml(runOK);
    expect(html).toContain('title="accepted">2<');
    expect(html).toContain('title="rejected">1<');
    expect(html).toContain('title="failed">0<');
    expect(html).toContain('title="undecided">3<');
    expect(html).toContain('1.5s');
    expect(html).toContain('ago');
    expect(html).toMatch(/task-history-when" title="[^"]+"/);
  });

  it('renders escaped error text for failed runs', () => {
    const html = exports.historyRowHtml(runFail);
    expect(html).toContain('task-err');
    expect(html).toContain('has-err');
    expect(html).toContain('&lt;refused&gt;');
    expect(html).not.toContain('<refused>');
  });

  it('renders a DRY badge for dry runs', () => {
    expect(exports.historyRowHtml(runDry)).toContain('dry-badge');
    expect(exports.historyRowHtml(runOK)).not.toContain('dry-badge');
  });

  it('renders an empty-state row when there are no runs', () => {
    expect(exports.historyHtml([])).toContain('No recorded runs');
  });
});

// ── card expansion ───────────────────────────────────────────────────────────

describe('run-history expansion', () => {
  let exports, dom;
  const tasks = [{ name: 'tv', schedule: '1h' }];
  const history = { tv: [runOK, runFail] };

  beforeEach(() => {
    ({ exports, dom } = loadModule(async () => jsonResp({})));
  });

  it('is collapsed by default but shows an error indicator for recent failures', () => {
    exports.render(tasks, history);
    const html = dom.elements['task-grid'].innerHTML;
    expect(html).not.toContain('task-history-row');
    expect(html).toContain('task-err-dot');
  });

  it('expands on toggle, showing the full run list including old errors', () => {
    exports.render(tasks, history);
    exports.toggleTaskHistory('tv');
    const html = dom.elements['task-grid'].innerHTML;
    expect(html).toContain('task-history-row');
    expect((html.match(/task-history-row/g) || []).length).toBe(2);
    expect(html).toContain('&lt;refused&gt;'); // the 2h-old failed run is visible
  });

  it('preserves expansion state across poll re-renders', () => {
    exports.render(tasks, history);
    exports.toggleTaskHistory('tv');
    exports.render(tasks, history); // simulated 10s poll
    expect(dom.elements['task-grid'].innerHTML).toContain('task-history-row');
    exports.toggleTaskHistory('tv'); // collapse again
    expect(dom.elements['task-grid'].innerHTML).not.toContain('task-history-row');
  });

  it('hides the collapsed error dot when no recent run failed', () => {
    exports.render(tasks, { tv: [runOK] });
    expect(dom.elements['task-grid'].innerHTML).not.toContain('task-err-dot');
  });
});

// ── triggerRun / runAll failure feedback ─────────────────────────────────────

describe('triggerRun', () => {
  it('shows the pending state and keeps it across re-renders via the pending map', async () => {
    const calls = [];
    const { exports } = loadModule(async (url, init) => {
      calls.push({ url, init });
      return { ok: true, status: 202, text: async () => '' };
    });
    const btn = makeBtn('Run now');
    await exports.triggerRun('tv', btn, false);
    expect(calls[0].url).toBe('/api/tasks/tv/run');
    expect(exports.pendingTriggers().has('tv')).toBe(true);
    // A poll re-render mid-window renders the disabled triggered button.
    const html = exports.card({ name: 'tv' }, []);
    expect(html).toContain('Triggered…');
    expect(html).toContain('disabled');
  });

  it('shows a failure state and warns on non-2xx responses', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { exports } = loadModule(async () => (
      { ok: false, status: 404, text: async () => 'task not found' }
    ));
    const btn = makeBtn('Run now');
    await exports.triggerRun('gone', btn, false);
    expect(btn.textContent).toBe('Failed — see log');
    expect(btn.classList.contains('trigger-failed')).toBe(true);
    expect(btn.classList.contains('triggered')).toBe(false);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('HTTP 404'));
    // The pending map re-applies the failure state on re-render.
    const html = exports.card({ name: 'gone' }, []);
    expect(html).toContain('Failed — see log');
    expect(html).toContain('trigger-failed');
    warn.mockRestore();
  });

  it('shows a failure state on network errors', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { exports } = loadModule(async () => { throw new TypeError('Failed to fetch'); });
    const btn = makeBtn('Run now');
    await exports.triggerRun('tv', btn, false);
    expect(btn.textContent).toBe('Failed — see log');
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  it('ignores a second trigger while one is in flight', async () => {
    let calls = 0;
    const { exports } = loadModule(async () => { calls++; return { ok: true, status: 202, text: async () => '' }; });
    const btn = makeBtn('Run now');
    await exports.triggerRun('tv', btn, false);
    await exports.triggerRun('tv', btn, false); // still pending (timers suppressed)
    expect(calls).toBe(1);
  });
});

describe('runAll', () => {
  it('shows a failure state and warns when the request fails', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { exports } = loadModule(async () => (
      { ok: false, status: 500, text: async () => 'boom' }
    ));
    const btn = makeBtn('Run all');
    await exports.runAll(btn, false);
    expect(btn.textContent).toBe('Failed — see log');
    expect(btn.classList.contains('trigger-failed')).toBe(true);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('HTTP 500'));
    warn.mockRestore();
  });

  it('keeps the triggered label on success until the timer resets it', async () => {
    const { exports } = loadModule(async () => ({ ok: true, status: 202, text: async () => '' }));
    const btn = makeBtn('Run all');
    await exports.runAll(btn, true);
    expect(btn.textContent).toBe('Dry…');
    expect(btn.disabled).toBe(true);
  });
});

// ── refresh() gating ─────────────────────────────────────────────────────────

describe('refresh', () => {
  it('early-returns without fetching when the page is hidden', async () => {
    let calls = 0;
    const { exports, dom } = loadModule(async () => { calls++; return jsonResp({ tasks: [] }); });
    dom.document.hidden = true;
    await exports.refresh();
    expect(calls).toBe(0);
  });

  it('uses started_at from /api/status for the uptime display', async () => {
    const startedAt = new Date(Date.now() - 3600 * 1000).toISOString();
    const { exports, dom } = loadModule(async url => {
      if (url === '/api/status') return jsonResp({ tasks: [{ name: 'a', running: false }], started_at: startedAt });
      return jsonResp({});
    });
    await exports.refresh();
    expect(dom.elements['header-meta'].textContent).toContain('up 1h 0m');
  });

  it('disables both run-all buttons while every task is running', async () => {
    const { exports, dom } = loadModule(async url => {
      if (url === '/api/status') return jsonResp({ tasks: [{ name: 'a', running: true }, { name: 'b', running: true }] });
      return jsonResp({});
    });
    await exports.refresh();
    expect(dom.elements['btn-run-all'].disabled).toBe(true);
    expect(dom.elements['btn-run-all-dry'].disabled).toBe(true);
  });

  it('re-enables both run-all buttons when a task is idle', async () => {
    const { exports, dom } = loadModule(async url => {
      if (url === '/api/status') return jsonResp({ tasks: [{ name: 'a', running: true }, { name: 'b', running: false }] });
      return jsonResp({});
    });
    dom.elements['btn-run-all'].disabled = true;
    dom.elements['btn-run-all-dry'].disabled = true;
    await exports.refresh();
    expect(dom.elements['btn-run-all'].disabled).toBe(false);
    expect(dom.elements['btn-run-all-dry'].disabled).toBe(false);
  });

  it('does not fetch DB buckets when the DB tab is hidden', async () => {
    const urls = [];
    const { exports } = loadModule(async url => { urls.push(url); return jsonResp({ tasks: [] }); });
    await exports.refresh();
    expect(urls).not.toContain('/api/db/buckets');
  });
});

// ── clearLog / placeholder ───────────────────────────────────────────────────

describe('clearLog', () => {
  it('clears client-side only: no /tail refetch, marker inserted, live lines resume', async () => {
    const urls = [];
    const { exports, dom } = loadModule(async url => {
      urls.push(url);
      return jsonResp({ lines: [], exhausted: true });
    });
    const con = dom.elements['log-console'];
    exports.appendRenderedLine('line one', '0:10', false);
    exports.appendRenderedLine('line two', '0:20', false);
    expect(con.children.length).toBe(2);
    const cursorBefore = exports.state().bottomCursor;

    urls.length = 0;
    exports.clearLog();

    expect(urls).toEqual([]); // no re-boot from /api/logs/tail
    expect(con.children.length).toBe(1);
    expect(con.children[0].textContent).toBe('── cleared ──');
    expect(exports.state().rendered.length).toBe(0);
    // Cursors survive so paging/live-dedup still work.
    expect(exports.state().bottomCursor).toBe(cursorBefore);

    // New live lines append below the marker.
    exports.appendRenderedLine('line three', '0:30', true);
    expect(con.children.length).toBe(2);
    expect(con.children[1].className).toBe('log-line');
  });
});

describe('empty log placeholder', () => {
  it('shows a placeholder for an empty console and removes it on first line', async () => {
    const { exports, dom } = loadModule(async () => jsonResp({}));
    const con = dom.elements['log-console'];
    exports.renderInitialTail({ lines: [], exhausted: true });
    expect(con.children.some(c => c.className === 'log-placeholder')).toBe(true);
    expect(con.children.find(c => c.className === 'log-placeholder').textContent)
      .toBe('waiting for log output…');

    exports.appendRenderedLine('first line', '0:11', true);
    expect(con.children.some(c => c.className === 'log-placeholder')).toBe(false);
  });

  it('does not show the placeholder when the tail has lines', async () => {
    const { exports, dom } = loadModule(async () => jsonResp({}));
    exports.renderInitialTail({ lines: [{ pos: '0:10', text: 'hello' }], exhausted: false, older_cursor: '0:0' });
    expect(dom.elements['log-console'].children.some(c => c.className === 'log-placeholder')).toBe(false);
  });
});
