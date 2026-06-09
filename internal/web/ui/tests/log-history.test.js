/**
 * Tests for the file-backed log viewer in dashboard.js.
 *
 * The viewer maintains a sliding window of rendered lines over the
 * rotating log file. These tests cover the state machine: boot via
 * /api/logs/tail, scroll-up paging via /before, SSE delivery with
 * live-follow vs paused, filter teardown, and window cap eviction.
 *
 * The runtime depends on EventSource and DOM, neither of which exist in
 * vitest's environment. We evaluate dashboard.js against a controlled
 * stand-in: minimal DOM nodes, a stubbed fetch, and a stubbed
 * EventSource we can drive from inside each test.
 */

import { describe, it, expect, beforeEach } from 'vitest';
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
    hidden: false,
    style: { display: '' },
    classList: {
      _set: new Set(),
      add(c) { this._set.add(c); },
      remove(c) { this._set.delete(c); },
      toggle(c, on) {
        if (on === undefined) {
          if (this._set.has(c)) this._set.delete(c); else this._set.add(c);
        } else if (on) this._set.add(c); else this._set.delete(c);
      },
      contains(c) { return this._set.has(c); },
    },
    children: [],
    parentNode: null,
    nodeType: 1,
    _listeners: {},
    addEventListener(name, fn) {
      (this._listeners[name] ||= []).push(fn);
    },
    fireEvent(name) {
      (this._listeners[name] || []).forEach(fn => fn({target: this}));
    },
    appendChild(c) {
      if (c.tagName === '#fragment') {
        const items = c.children.slice();
        for (const item of items) { item.parentNode = this; this.children.push(item); }
        c.children.length = 0;
        return c;
      }
      c.parentNode = this;
      this.children.push(c);
      return c;
    },
    insertBefore(c, ref) {
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
    set(v) {
      _innerHTML = v;
      // Mirror real DOM behavior: setting innerHTML='' clears children.
      if (v === '') node.children.length = 0;
    },
  });
  return node;
}

function makeDOM() {
  const con = makeNode('div');
  con.scrollTop = 0;
  con.clientHeight = 300;
  Object.defineProperty(con, 'scrollHeight', {
    get() { return Math.max(this.children.length * 20, this.clientHeight); },
  });

  const filter = makeNode('input');
  filter.value = '';

  const elements = {
    'log-console':        con,
    'log-filter':         filter,
    'log-dot':            makeNode('div'),
    'log-status-text':    makeNode('span'),
    'log-tail-pill':      makeNode('button'),
    'log-tail-pill-count': makeNode('span'),
    'log-filter-spinner': makeNode('div'),
  };
  return {
    elements,
    document: {
      getElementById: id => elements[id],
      createElement: tag => makeNode(tag),
      createDocumentFragment: () => makeNode('#fragment'),
    },
  };
}

// ── stubbed EventSource ──────────────────────────────────────────────────────

function makeEventSourceStub() {
  const instances = [];
  function ES(url) {
    this.url = url;
    this.onopen = null;
    this.onmessage = null;
    this.onerror = null;
    this.closed = false;
    this._listeners = {};
    instances.push(this);
  }
  ES.prototype.addEventListener = function(name, fn) {
    (this._listeners[name] ||= []).push(fn);
  };
  ES.prototype.close = function() { this.closed = true; };
  ES.prototype.fire = function(name, data, eventId) {
    const ev = {data, lastEventId: String(eventId || '')};
    if (name === 'message') {
      if (this.onmessage) this.onmessage(ev);
    } else if (name === 'open') {
      if (this.onopen) this.onopen(ev);
    } else {
      (this._listeners[name] || []).forEach(fn => fn(ev));
    }
  };
  ES.instances = instances;
  return ES;
}

// ── module loader ────────────────────────────────────────────────────────────

function loadModule(fetchImpl) {
  const dom = makeDOM();
  const ES = makeEventSourceStub();
  const window = {
    EventSource: ES,
    setTimeout: (fn, ms) => null,    // suppress reconnect timer
    clearTimeout: () => {},
    requestAnimationFrame: fn => fn(),
  };
  const exports = {};
  const fn = new Function(
    'exports', 'document', 'fetch', 'window', 'EventSource',
    'setTimeout', 'clearTimeout', 'requestAnimationFrame',
    src + `
      exports.loadInitialTail = loadInitialTail;
      exports.handleSSELine = handleSSELine;
      exports.handleRotation = handleRotation;
      exports.maybeLoadOlder = maybeLoadOlder;
      exports.maybeLoadNewer = maybeLoadNewer;
      exports.onLogScroll = onLogScroll;
      exports.applyFilter = applyFilter;
      exports.clearLog = clearLog;
      exports.resumeLiveTail = resumeLiveTail;
      exports.connectLogs = connectLogs;
      exports.renderLogLine = renderLogLine;
      exports.state = () => veLog;
    `
  );
  fn(
    exports, dom.document, fetchImpl, window, ES,
    window.setTimeout, window.clearTimeout, window.requestAnimationFrame,
  );
  return { exports, dom, ES };
}

function jsonResp(body) {
  return { ok: true, json: async () => body };
}

function makeTailBody(lines, {exhausted = false, olderCursor = null} = {}) {
  // Cumulative byte positions so a contiguous SSE line at
  // pos = cumulative + nextText.length + 1 satisfies hasLogGap=false.
  let cumulative = 0;
  const items = lines.map((text) => {
    cumulative += text.length + 1;
    return {pos: `0:${cumulative}`, text};
  });
  return {
    lines: items,
    older_cursor: exhausted ? '' : (olderCursor || (items[0] ? items[0].pos : '')),
    exhausted,
  };
}

// posAfter computes the next contiguous file position given a prior pos
// string and the new line's text length. Useful for synthesizing
// gap-free SSE positions in tests.
function posAfter(prevPos, text) {
  const m = /^(\d+):(\d+)$/.exec(prevPos);
  if (!m) return '0:0';
  const f = parseInt(m[1], 10);
  const b = parseInt(m[2], 10) + text.length + 1;
  return `${f}:${b}`;
}

// ── tests ────────────────────────────────────────────────────────────────────

describe('log viewer — boot via /api/logs/tail', () => {
  it('hits /tail on boot and renders the lines into the console', async () => {
    const calls = [];
    const fetchStub = async (url) => {
      calls.push(url);
      return jsonResp(makeTailBody(['a', 'b', 'c']));
    };
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatch(/^\/api\/logs\/tail\?/);
    expect(calls[0]).toMatch(/limit=300/);
    const con = dom.elements['log-console'];
    expect(con.children.map(c => c.innerHTML)).toEqual(['a', 'b', 'c']);
    const st = exports.state();
    expect(st.rendered).toHaveLength(3);
    // First line 'a' (1 char + '\n' = 2 bytes) ⇒ pos 0:2.
    expect(st.topCursor).toBe('0:2');
    expect(st.topExhausted).toBe(false);
    expect(st.bottomAtTail).toBe(true);
    expect(st.liveFollow).toBe(true);
  });

  it('marks topExhausted=true when /tail reports exhausted', async () => {
    const fetchStub = async () => jsonResp(makeTailBody(['only'], {exhausted: true}));
    const { exports } = loadModule(fetchStub);
    await exports.loadInitialTail();
    expect(exports.state().topExhausted).toBe(true);
  });
});

describe('log viewer — scroll-up paging /before', () => {
  it('fetches /before with the topCursor and prepends older lines', async () => {
    let call = 0;
    const responses = [
      makeTailBody(['n1', 'n2', 'n3']),  // boot
      {
        lines: [
          {pos: '0:1', text: 'o1'},
          {pos: '0:2', text: 'o2'},
        ],
        older_cursor: '0:1',
        exhausted: false,
      },
    ];
    const calls = [];
    const fetchStub = async (url) => {
      calls.push(url);
      return jsonResp(responses[call++]);
    };
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    dom.elements['log-console'].scrollTop = 0; // near top
    await exports.maybeLoadOlder();

    expect(calls[1]).toMatch(/^\/api\/logs\/before\?/);
    // makeTailBody(['n1','n2','n3']) → first line ends at byte 3.
    expect(calls[1]).toMatch(/cursor=0%3A3/);
    const st = exports.state();
    expect(st.rendered.map(r => r.raw)).toEqual(['o1', 'o2', 'n1', 'n2', 'n3']);
    expect(st.topCursor).toBe('0:1');
  });

  it('marks topExhausted and skips further /before calls', async () => {
    let call = 0;
    const responses = [
      makeTailBody(['n']),
      { lines: [{pos: '0:5', text: 'o'}], older_cursor: '', exhausted: true },
    ];
    const fetchStub = async () => jsonResp(responses[call++]);
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    dom.elements['log-console'].scrollTop = 0;
    await exports.maybeLoadOlder();
    expect(exports.state().topExhausted).toBe(true);

    let extra = 0;
    const fetch2 = async () => { extra++; return jsonResp({lines: [], older_cursor: '', exhausted: true}); };
    // Re-bind fetch by directly calling: the state is set, maybeLoadOlder should bail.
    // (We just verify the local count doesn't grow on the original.)
    const before = call;
    await exports.maybeLoadOlder();
    await exports.maybeLoadOlder();
    expect(call).toBe(before);
  });
});

describe('log viewer — live SSE delivery', () => {
  it('appends live lines while at bottom (liveFollow)', async () => {
    const fetchStub = async () => jsonResp(makeTailBody(['a']));
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    // Synthesize contiguous positions so the reconnect bridge stays out
    // of the way; we test the bridge separately.
    const p1 = posAfter(exports.state().lastLivePos, 'live1');
    const p2 = posAfter(p1, 'live2');
    exports.handleSSELine({pos: p1, text: 'live1', seq: 1});
    exports.handleSSELine({pos: p2, text: 'live2', seq: 2});

    const con = dom.elements['log-console'];
    expect(con.children.map(c => c.innerHTML)).toEqual(['a', 'live1', 'live2']);
    expect(exports.state().bottomCursor).toBe(p2);
    expect(exports.state().pendingLive).toBe(0);
  });

  it('buffers under the pill while paused (not following)', async () => {
    const fetchStub = async () => jsonResp(makeTailBody(['a']));
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    // Simulate user having scrolled away from the bottom by toggling
    // liveFollow off directly — the geometry-based detection inside
    // onLogScroll is exercised separately.
    exports.state().liveFollow = false;

    const p1 = posAfter(exports.state().lastLivePos, 'live1');
    const p2 = posAfter(p1, 'live2');
    exports.handleSSELine({pos: p1, text: 'live1', seq: 1});
    exports.handleSSELine({pos: p2, text: 'live2', seq: 2});

    const pill = dom.elements['log-tail-pill'];
    const countEl = dom.elements['log-tail-pill-count'];
    expect(pill.hidden).toBe(false);
    expect(countEl.textContent).toBe('2');
    const con = dom.elements['log-console'];
    expect(con.children.map(c => c.innerHTML)).toEqual(['a', 'live1', 'live2']);
  });

  it('resumeLiveTail clears the pending counter and re-engages follow', async () => {
    const fetchStub = async () => jsonResp(makeTailBody(['a']));
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    exports.state().liveFollow = false;
    const p = posAfter(exports.state().lastLivePos, 'live');
    exports.handleSSELine({pos: p, text: 'live', seq: 1});
    expect(exports.state().pendingLive).toBe(1);

    await exports.resumeLiveTail();
    expect(exports.state().liveFollow).toBe(true);
    expect(exports.state().pendingLive).toBe(0);
    expect(dom.elements['log-tail-pill'].hidden).toBe(true);
  });
});

describe('log viewer — filter', () => {
  it('refetches /tail with q when the filter changes', async () => {
    const calls = [];
    let call = 0;
    const responses = [
      makeTailBody(['a', 'b']),       // initial boot
      makeTailBody(['match-x']),      // refetch after filter
    ];
    const fetchStub = async (url) => {
      calls.push(url);
      return jsonResp(responses[call++] || {lines: [], older_cursor: '', exhausted: true});
    };
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    dom.elements['log-filter'].value = 'match';
    await exports.applyFilter();

    expect(exports.state().filter).toBe('match');
    expect(calls[1]).toMatch(/q=match/);
    expect(dom.elements['log-console'].children.map(c => c.innerHTML)).toEqual(['match-x']);
  });

  it('applyFilter is idempotent when filter unchanged', async () => {
    let call = 0;
    const fetchStub = async () => { call++; return jsonResp(makeTailBody(['a'])); };
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();
    dom.elements['log-filter'].value = '';
    await exports.applyFilter();
    expect(call).toBe(1); // only the initial boot
  });
});

describe('log viewer — rendering', () => {
  it('renders ANSI lines via ansiToHtml', async () => {
    const { exports } = loadModule(async () => jsonResp({lines: [], older_cursor: '', exhausted: true}));
    const html = exports.renderLogLine('\x1b[36mhello\x1b[0m');
    expect(html).toContain('color:#58a6ff');
    expect(html).toContain('hello');
  });

  it('colorizes a plain INFO line', async () => {
    const { exports } = loadModule(async () => jsonResp({lines: [], older_cursor: '', exhausted: true}));
    const html = exports.renderLogLine(
      '2026-06-02 19:31:22.991 INFO  scheduled pipeline=movies-3d'
    );
    expect(html).toContain('color:#58a6ff">INFO</span>');
    expect(html).toContain('scheduled');
  });

  it('falls back to plain text for non-conforming lines', async () => {
    const { exports } = loadModule(async () => jsonResp({lines: [], older_cursor: '', exhausted: true}));
    const html = exports.renderLogLine('not a structured line');
    expect(html).toBe('not a structured line');
  });
});

describe('log viewer — window cap eviction', () => {
  it('evicts oldest lines and re-anchors topCursor when window exceeds cap', async () => {
    const fetchStub = async () => jsonResp(makeTailBody(['boot']));
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    // Drive enough live lines to overflow LOG_WINDOW_CAP (2000).
    // We push 2010 lines, then assert the rendered window holds exactly
    // 2000 with the OLDEST being one of the live lines (the boot line
    // was evicted) and topCursor pointing at that new oldest. Positions
    // are made contiguous so the reconnect bridge stays out of the way.
    const total = 2010;
    let lastPos = exports.state().lastLivePos;
    for (let i = 0; i < total; i++) {
      const text = 'live-' + i;
      const p = posAfter(lastPos, text);
      exports.handleSSELine({pos: p, text, seq: i + 1});
      lastPos = p;
    }

    const st = exports.state();
    expect(st.rendered.length).toBe(2000);
    const expectedNewOldestText = `live-${total - 2000}`; // boot + earlier live lines were evicted
    expect(st.rendered[0].raw).toBe(expectedNewOldestText);
    expect(st.topCursor).toBe(st.rendered[0].pos);
    expect(st.topExhausted).toBe(false);
    expect(dom.elements['log-console'].children.length).toBe(2000);
  });
});

describe('log viewer — resumeLiveTail iterates', () => {
  it('pages /after until at_tail and then re-engages live-follow', async () => {
    const responses = [
      makeTailBody(['boot']),
      // Two non-tail /after responses, then a tail response.
      {
        lines: [{pos: '0:100', text: 'page1-a'}, {pos: '0:110', text: 'page1-b'}],
        newer_cursor: '0:110',
        at_tail: false,
      },
      {
        lines: [{pos: '0:120', text: 'page2-a'}],
        newer_cursor: '0:120',
        at_tail: false,
      },
      {
        lines: [{pos: '0:130', text: 'final'}],
        newer_cursor: '0:130',
        at_tail: true,
      },
    ];
    let i = 0;
    const calls = [];
    const fetchStub = async (url) => { calls.push(url); return jsonResp(responses[i++]); };
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    // Put the viewer into a paged-history state: pretend the user
    // already scrolled forward off live.
    exports.state().bottomAtTail = false;
    exports.state().bottomCursor = '0:90';
    exports.state().liveFollow = false;
    exports.state().pendingLive = 3;

    await exports.resumeLiveTail();

    const st = exports.state();
    expect(st.bottomAtTail).toBe(true);
    expect(st.liveFollow).toBe(true);
    expect(st.pendingLive).toBe(0);
    expect(dom.elements['log-console'].children.map(c => c.innerHTML))
      .toEqual(['boot', 'page1-a', 'page1-b', 'page2-a', 'final']);
    // Three /after calls were issued, with cursors advancing.
    const afterCalls = calls.filter(u => u.startsWith('/api/logs/after'));
    expect(afterCalls).toHaveLength(3);
    expect(afterCalls[0]).toMatch(/cursor=0%3A90/);
    expect(afterCalls[1]).toMatch(/cursor=0%3A110/);
    expect(afterCalls[2]).toMatch(/cursor=0%3A120/);
  });
});

describe('log viewer — SSE reconnect bridge', () => {
  it('detects a position gap and fills it via /after before applying the new live line', async () => {
    // Boot with a single line at pos 0:10.
    // Then SSE delivers a line at pos 0:200 whose byteEnd implies a
    // ~190-byte gap. The bridge should fetch /after?cursor=0:10 to
    // recover the missed lines.
    const responses = [
      makeTailBody(['boot']),
      // /after returns the missed lines AND a copy of the SSE line at the
      // same pos — the dedupe logic should drop the duplicate.
      {
        lines: [
          {pos: '0:50',  text: 'missed-1'},
          {pos: '0:120', text: 'missed-2'},
          {pos: '0:200', text: 'live-200'},
        ],
        newer_cursor: '0:200',
        at_tail: true,
      },
    ];
    let i = 0;
    const calls = [];
    const fetchStub = async (url) => { calls.push(url); return jsonResp(responses[i++]); };
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();
    // After boot: 'boot' is 4 chars → byteEnd 5.
    expect(exports.state().lastLivePos).toBe('0:5');

    // Deliver the suddenly-far-ahead live line.
    exports.handleSSELine({pos: '0:200', text: 'live-200', seq: 99});
    // Wait a microtask for runBridge to complete.
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));

    const bridgeCalls = calls.filter(u => u.startsWith('/api/logs/after'));
    expect(bridgeCalls).toHaveLength(1);
    expect(bridgeCalls[0]).toMatch(/cursor=0%3A5/);

    // The rendered window holds boot + 3 unique bridge lines (live-200
    // is the same pos as one of them, so it's deduped).
    const rendered = dom.elements['log-console'].children.map(c => c.innerHTML);
    expect(rendered).toEqual(['boot', 'missed-1', 'missed-2', 'live-200']);
    expect(exports.state().lastLivePos).toBe('0:200');
  });

  it('skips the bridge when the new line is contiguous', async () => {
    const fetchStub = async () => jsonResp(makeTailBody(['boot']));
    const calls = [];
    const wrapped = async (url) => { calls.push(url); return fetchStub(); };
    const { exports } = loadModule(wrapped);
    await exports.loadInitialTail();
    // boot ends at pos 0:5. SSE line 'next' (4 chars) at 0:10 = 5 + 4 + 1.
    exports.handleSSELine({pos: '0:10', text: 'next', seq: 2});
    // No /after call should fire.
    expect(calls.filter(u => u.startsWith('/api/logs/after'))).toHaveLength(0);
    expect(exports.state().lastLivePos).toBe('0:10');
  });
});

describe('log viewer — handleRotation', () => {
  it('triggers a full re-tail after a rotation event', async () => {
    let call = 0;
    const responses = [
      makeTailBody(['a']),
      makeTailBody(['fresh1', 'fresh2']),
    ];
    const calls = [];
    const fetchStub = async (url) => { calls.push(url); return jsonResp(responses[call++]); };
    const { exports, dom } = loadModule(fetchStub);
    await exports.loadInitialTail();

    await exports.handleRotation();
    // Flush any pending microtasks from the inner loadInitialTail.
    await new Promise(r => setTimeout(r, 0));

    expect(calls[1]).toMatch(/^\/api\/logs\/tail\?/);
    expect(dom.elements['log-console'].children.map(c => c.innerHTML))
      .toEqual(['fresh1', 'fresh2']);
  });
});
