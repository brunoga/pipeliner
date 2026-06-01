/**
 * Tests for the dashboard log-scrollback feature in dashboard.js.
 *
 * dashboard.js leans heavily on the DOM and global fetch, so we evaluate it
 * fresh per test inside a minimal mock environment: a tiny stand-in for
 * #log-console (with the few properties our scrollback code touches), a stub
 * for #log-filter, and a controllable fetch.
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'dashboard.js'), 'utf8');

// ── tiny DOM stub ────────────────────────────────────────────────────────────
//
// We model only what dashboard.js touches: a console container, its children
// list (so insertBefore/firstChild behave), and a filter input. Each "element"
// is a plain object; the scrollback code reads/writes scrollTop, scrollHeight,
// className, innerHTML, style.display, and DOM traversal helpers.

function makeNode(tag) {
  const node = {
    tagName: tag,
    className: '',
    innerHTML: '',
    textContent: '',
    style: { display: '' },
    children: [],
    parentNode: null,
    nodeType: 1,
    appendChild(c) {
      // Document fragments append their children, not themselves.
      if (c.tagName === '#fragment') {
        const items = c.children.slice();
        for (const item of items) {
          item.parentNode = this;
          this.children.push(item);
        }
        c.children.length = 0;
        return c;
      }
      c.parentNode = this;
      this.children.push(c);
      return c;
    },
    insertBefore(c, ref) {
      // ref === null means "append at end"
      if (ref == null) return this.appendChild(c);
      const idx = this.children.indexOf(ref);
      if (idx === -1) return this.appendChild(c);
      if (c.tagName === '#fragment') {
        const items = c.children.slice();
        for (const item of items) item.parentNode = this;
        this.children.splice(idx, 0, ...items);
        c.children.length = 0;
      } else {
        c.parentNode = this;
        this.children.splice(idx, 0, c);
      }
      return c;
    },
    get firstChild() { return this.children[0] || null; },
    addEventListener() {},
  };
  return node;
}

function makeDOM() {
  const console_ = makeNode('div');
  console_.scrollTop = 0;
  console_.clientHeight = 300;
  // scrollHeight grows with each appended child so prepend math has something
  // to measure. 20px per row is arbitrary — the only thing under test is the
  // delta, not the absolute height.
  Object.defineProperty(console_, 'scrollHeight', {
    get() { return this.children.length * 20; },
  });

  const filter = { value: '' };
  const dot   = { classList: { add() {}, remove() {} } };
  const text  = { textContent: '' };

  const elements = {
    'log-console':     console_,
    'log-filter':      filter,
    'log-dot':         dot,
    'log-status-text': text,
  };

  return {
    elements,
    document: {
      getElementById: id => elements[id],
      createElement(tag) {
        const n = makeNode(tag);
        return n;
      },
      createDocumentFragment() {
        const n = makeNode('#fragment');
        return n;
      },
    },
  };
}

// loadModule evaluates dashboard.js fresh against a clean DOM + a stub fetch,
// then returns the exports we need plus a handle on the mock for assertions.
function loadModule(fetchImpl) {
  const dom = makeDOM();
  const mod = new Function(
    'exports', 'document', 'fetch', 'window',
    src + `
      exports.loadLogHistory       = loadLogHistory;
      exports.maybeLoadLogHistory  = maybeLoadLogHistory;
      exports.prependHistoryLines  = prependHistoryLines;
      exports.clearLog             = clearLog;
      exports.applyFilter          = applyFilter;
      exports.appendLog            = appendLog;
      exports.getState = () => ({
        logHistory:    logHistory,
        historyLines:  historyLines,
        logLines:      logLines,
      });
    `
  );
  const exports = {};
  mod(exports, dom.document, fetchImpl, {});
  return { exports, dom };
}

function jsonResponse(body) {
  return { ok: true, json: async () => body };
}

// ── tests ────────────────────────────────────────────────────────────────────

describe('log scrollback', () => {
  it('fetches /api/logs/history with offset=0 on first near-top scroll', async () => {
    const calls = [];
    const fetchStub = async (url) => {
      calls.push(url);
      return jsonResponse({ lines: ['a', 'b', 'c'] });
    };
    const { exports, dom } = loadModule(fetchStub);

    dom.elements['log-console'].scrollTop = 0;
    await exports.maybeLoadLogHistory();

    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatch(/offset=0/);
    expect(calls[0]).toMatch(/limit=200/);

    const state = exports.getState();
    expect(state.logHistory.offset).toBe(3);
    expect(state.historyLines).toHaveLength(3);
  });

  it('does not fetch when scrolled away from the top', async () => {
    let called = false;
    const { exports, dom } = loadModule(async () => {
      called = true;
      return jsonResponse({ lines: [] });
    });

    dom.elements['log-console'].scrollTop = 200; // well past threshold
    await exports.maybeLoadLogHistory();

    expect(called).toBe(false);
  });

  it('increments offset across multiple chunks', async () => {
    const responses = [
      { lines: Array.from({ length: 200 }, (_, i) => 'newer-' + i) },
      { lines: Array.from({ length: 200 }, (_, i) => 'older-' + i) },
    ];
    let i = 0;
    const fetchStub = async () => jsonResponse(responses[i++]);
    const { exports, dom } = loadModule(fetchStub);

    dom.elements['log-console'].scrollTop = 0;
    await exports.loadLogHistory();
    expect(exports.getState().logHistory.offset).toBe(200);
    expect(exports.getState().logHistory.exhausted).toBe(false);

    await exports.loadLogHistory();
    expect(exports.getState().logHistory.offset).toBe(400);
  });

  it('marks history exhausted when the server returns a short chunk', async () => {
    const fetchStub = async () => jsonResponse({ lines: ['only', 'two'] });
    const { exports } = loadModule(fetchStub);

    await exports.loadLogHistory();
    const state = exports.getState();
    expect(state.logHistory.exhausted).toBe(true);
    expect(state.logHistory.endMarker).not.toBeNull();
  });

  it('skips further fetches once exhausted', async () => {
    let count = 0;
    const fetchStub = async () => {
      count++;
      return jsonResponse({ lines: ['x'] }); // short ⇒ marks exhausted
    };
    const { exports, dom } = loadModule(fetchStub);

    dom.elements['log-console'].scrollTop = 0;
    await exports.loadLogHistory();
    expect(count).toBe(1);

    await exports.maybeLoadLogHistory();
    await exports.maybeLoadLogHistory();
    expect(count).toBe(1); // no extra fetches after exhaustion
  });

  it('prepends history above existing lines in chronological order', async () => {
    // Round 1: a full-size chunk of 'n*' (newer). Must be exactly LOG_HISTORY_LIMIT
    // so the loader doesn't mark itself exhausted and short-circuit round 2.
    // Round 2: a small 'o*' chunk (older) — these should land above the n* in the DOM.
    const responses = [
      { lines: Array.from({ length: 200 }, (_, i) => 'n' + i) },
      { lines: ['o0', 'o1', 'o2'] },
    ];
    let i = 0;
    const fetchStub = async () => jsonResponse(responses[i++]);
    const { exports, dom } = loadModule(fetchStub);

    await exports.loadLogHistory(); // 200 n*
    await exports.loadLogHistory(); // 3 o* prepended above

    const con = dom.elements['log-console'];
    // Total = 203 history rows + 1 end-marker (round 2 was short).
    expect(con.children.length).toBe(204);
    // First child is the end-marker. Rows after it must be in chronological
    // order: o0..o2 (older) then n0..n199 (newer).
    const rows = con.children.slice(1).map(c => c.innerHTML);
    expect(rows.slice(0, 4)).toEqual(['o0', 'o1', 'o2', 'n0']);
  });

  it('preserves the user\'s scroll position after a prepend', async () => {
    // The contract: after lazy-loading older lines, the user's view should not
    // jump — scrollTop must shift by exactly the height of the prepended block
    // so the previously-visible rows stay at the same physical position.
    const { exports, dom } = loadModule(async () => jsonResponse({
      lines: Array.from({ length: 200 }, (_, i) => 'h' + i),
    }));
    const con = dom.elements['log-console'];

    exports.appendLog('live-1');
    exports.appendLog('live-2');
    con.scrollTop = 5; // user scrolled near the top

    const before = con.scrollHeight;
    await exports.loadLogHistory();
    const after = con.scrollHeight;

    expect(after).toBeGreaterThan(before);
    expect(con.scrollTop).toBe(5 + (after - before));
  });

  it('clearLog resets history state so the next scroll starts over', async () => {
    let calls = 0;
    const fetchStub = async () => {
      calls++;
      return jsonResponse({ lines: Array.from({length: 200}, (_, i) => 'L' + i) });
    };
    const { exports } = loadModule(fetchStub);

    await exports.loadLogHistory();
    expect(exports.getState().logHistory.offset).toBe(200);

    exports.clearLog();
    const cleared = exports.getState();
    expect(cleared.logHistory.offset).toBe(0);
    expect(cleared.logHistory.exhausted).toBe(false);
    expect(cleared.historyLines).toHaveLength(0);

    await exports.loadLogHistory();
    expect(calls).toBe(2);
    expect(exports.getState().logHistory.offset).toBe(200);
  });

  it('applies the filter to history lines too', async () => {
    const fetchStub = async () => jsonResponse({ lines: ['cat', 'dog', 'fish'] });
    const { exports, dom } = loadModule(fetchStub);

    await exports.loadLogHistory();
    dom.elements['log-filter'].value = 'cat';
    exports.applyFilter();

    const hist = exports.getState().historyLines;
    const visible = hist.filter(h => h.el.style.display !== 'none').map(h => h.raw);
    expect(visible).toEqual(['cat']);
  });

  it('survives concurrent maybeLoad calls — only one fetch in flight', async () => {
    let inFlight = 0;
    let maxInFlight = 0;
    const fetchStub = async () => {
      inFlight++;
      maxInFlight = Math.max(maxInFlight, inFlight);
      // Microtask gap so a parallel call could try to slip in.
      await Promise.resolve();
      inFlight--;
      return jsonResponse({ lines: Array.from({length: 200}, (_, i) => 'L' + i) });
    };
    const { exports, dom } = loadModule(fetchStub);
    dom.elements['log-console'].scrollTop = 0;

    await Promise.all([
      exports.maybeLoadLogHistory(),
      exports.maybeLoadLogHistory(),
      exports.maybeLoadLogHistory(),
    ]);

    expect(maxInFlight).toBe(1);
  });
});
