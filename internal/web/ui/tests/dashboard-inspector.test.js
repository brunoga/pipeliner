/**
 * Tests for the run-inspector modal in dashboard.js.
 *
 * The inspector used to render inline inside each task card, so the dashboard's
 * 10 s poll — which re-renders every card — wiped an open inspector. It now
 * renders into a single floating modal appended to <body>, outside the task
 * grid, so a refresh can't remove it. These tests cover:
 *   - historyRowHtml wires the inspect button to openTrace (no inline panel)
 *   - traceModalBodyHtml renders entries / empty / truncated
 *   - the modal is created on <body>, opens, fetches, and closes
 */

import { describe, it, expect, vi } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(__dir, '..', 'dashboard.js'), 'utf8');

// ── DOM stub with body, id registry, querySelector and event listeners ───────

function makeNode(tag) {
  let _innerHTML = '';
  const node = {
    tagName: tag,
    id: '',
    className: '',
    textContent: '',
    hidden: false,
    children: [],
    _listeners: {},
    appendChild(c) { this.children.push(c); return c; },
    addEventListener(ev, fn) { (this._listeners[ev] ||= []).push(fn); },
    querySelector() { return makeNode('div'); }, // modal sub-elements: assignable, not asserted
  };
  Object.defineProperty(node, 'innerHTML', {
    get() { return _innerHTML; },
    set(v) { _innerHTML = v; },
  });
  return node;
}

function loadModule(fetchImpl) {
  const body = makeNode('body');
  const byId = {};
  const docListeners = {};
  const document = {
    body,
    hidden: false,
    getElementById: id => byId[id] || null,
    createElement: tag => makeNode(tag),
    createDocumentFragment: () => makeNode('#fragment'),
    addEventListener: (ev, fn) => { (docListeners[ev] ||= []).push(fn); },
    removeEventListener: (ev, fn) => {
      docListeners[ev] = (docListeners[ev] || []).filter(f => f !== fn);
    },
  };
  // Mirror ensureTraceModal's body.appendChild into the id registry so the
  // second lookup reuses the same node instead of creating a duplicate.
  const origAppend = body.appendChild.bind(body);
  body.appendChild = c => { if (c.id) byId[c.id] = c; return origAppend(c); };

  const exports = {};
  const shims = `
    var dbLoaded = false;
    var dbNavItems = [];
    function renderDBSidebar() {}
  `;
  const fn = new Function(
    'exports', 'document', 'fetch', 'setTimeout', 'clearTimeout', 'requestAnimationFrame',
    shims + src + `
      exports.historyRowHtml = historyRowHtml;
      exports.traceModalBodyHtml = traceModalBodyHtml;
      exports.ensureModal = ensureModal;
      exports.openTrace = openTrace;
      exports.openRuns = openRuns;
      exports.closeModal = closeModal;
      exports.setHistory = (h) => { _lastHistory = h; };
    `
  );
  fn(exports, document, fetchImpl, () => null, () => {}, cb => cb());
  return { exports, document, body, docListeners };
}

function jsonResp(body) {
  return { ok: true, status: 200, json: async () => body, text: async () => JSON.stringify(body) };
}

// ── historyRowHtml wiring ────────────────────────────────────────────────────

describe('historyRowHtml inspect wiring', () => {
  const { exports } = loadModule(async () => jsonResp({}));

  it('wires the inspect button to openTrace and drops the inline panel', () => {
    const html = exports.historyRowHtml({ at: Date.now(), run_id: 'r1', accepted: 1 }, 'movies');
    expect(html).toContain('openTrace(');
    // Args are HTML-escaped by esc(JSON.stringify(...)), same as before.
    expect(html).toContain('movies');
    expect(html).toContain('r1');
    // The inline panel was the source of the wipe-on-refresh bug.
    expect(html).not.toContain('trace-panel');
  });

  it('omits the inspect button when the run has no run_id', () => {
    const html = exports.historyRowHtml({ at: Date.now(), accepted: 1 }, 'movies');
    expect(html).not.toContain('openTrace(');
  });
});

// ── traceModalBodyHtml ───────────────────────────────────────────────────────

describe('traceModalBodyHtml', () => {
  const { exports } = loadModule(async () => jsonResp({}));

  it('renders one row per entry', () => {
    const html = exports.traceModalBodyHtml({
      entries: [
        { title: 'A', final: 'accepted', steps: [] },
        { title: 'B', final: 'rejected', steps: [] },
      ],
    });
    expect(html).toContain('A');
    expect(html).toContain('B');
    expect(html).not.toContain('run produced no entries');
  });

  it('shows a placeholder when there are no entries', () => {
    expect(exports.traceModalBodyHtml({ entries: [] })).toContain('run produced no entries');
    expect(exports.traceModalBodyHtml({})).toContain('run produced no entries');
  });

  it('notes truncated entries', () => {
    const html = exports.traceModalBodyHtml({ entries: [{ title: 'A', final: 'accepted' }], truncated: 5 });
    expect(html).toContain('5 more entries');
  });
});

// ── modal lifecycle ──────────────────────────────────────────────────────────

describe('floating modals', () => {
  it('creates a modal on <body>, not in the task grid, and reuses it', () => {
    const { exports, body } = loadModule(async () => jsonResp({ entries: [] }));
    const m1 = exports.ensureModal('trace-modal', 'x');
    expect(m1.id).toBe('trace-modal');
    expect(body.children).toContain(m1); // lives on body → survives card re-renders
    const m2 = exports.ensureModal('trace-modal', 'x');
    expect(m2).toBe(m1); // reused, not duplicated
    expect(body.children.length).toBe(1);
  });

  it('openRuns opens the runs list from the last-rendered history', () => {
    const { exports, document } = loadModule(async () => jsonResp({}));
    exports.setHistory({ movies: [{ at: Date.now(), run_id: 'r1', accepted: 2 }] });
    exports.openRuns('movies');
    const m = document.getElementById('runs-modal');
    expect(m).toBeTruthy();
    expect(m.hidden).toBe(false);
  });

  it('openTrace fetches the trace and stacks its own modal', async () => {
    const fetchMock = vi.fn(async () => jsonResp({ entries: [] }));
    const { exports, document, docListeners } = loadModule(fetchMock);

    await exports.openTrace('movies', 'run-42');
    const m = document.getElementById('trace-modal');
    expect(m.hidden).toBe(false);
    expect(fetchMock).toHaveBeenCalledWith('/api/traces/movies/run-42');
    expect((docListeners.keydown || []).length).toBe(1); // Esc handler attached

    exports.closeModal('trace-modal');
    expect(m.hidden).toBe(true);
    expect((docListeners.keydown || []).length).toBe(0); // handler removed
  });

  it('Esc closes only the topmost modal (inspector stacked over runs)', () => {
    const { exports, document, docListeners } = loadModule(async () => jsonResp({}));
    exports.setHistory({ movies: [] });
    exports.openRuns('movies');
    // openTrace is async but the modal is shown synchronously before the fetch.
    exports.openTrace('movies', 'r1');
    // Fire Escape: closes the top (trace), leaves runs open.
    (docListeners.keydown || []).forEach(fn => fn({ key: 'Escape' }));
    expect(document.getElementById('trace-modal').hidden).toBe(true);
    expect(document.getElementById('runs-modal').hidden).toBe(false);
    expect((docListeners.keydown || []).length).toBe(1); // still one modal open
  });
});
