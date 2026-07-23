/**
 * Tests for the Trakt device-flow UI (trakt.js):
 *
 *   - the Authorize button is re-enabled (as "Restart authorization") once
 *     the device code is displayed, instead of staying stuck on "Starting…"
 *   - code expiry stops polling, shows an actionable message, and resets
 *     the button so the flow can be restarted
 *   - the 3s poll pauses while document.hidden
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'trakt.js'), 'utf8');

function makeNode() {
  return {
    value: '',
    textContent: '',
    innerHTML: '',
    className: '',
    disabled: false,
    style: { display: '' },
  };
}

function loadModule(fetchImpl) {
  const elements = {
    'trakt-client-id':     Object.assign(makeNode(), { value: 'id' }),
    'trakt-client-secret': Object.assign(makeNode(), { value: 'secret' }),
    'trakt-auth-btn':      Object.assign(makeNode(), { textContent: 'Authorize' }),
    'trakt-auth-status':   makeNode(),
    'trakt-auth-body':     makeNode(),
    'trakt-countdown':     makeNode(),
  };
  const documentStub = {
    hidden: false,
    getElementById: id => elements[id] || null,
  };
  // Fake interval registry so tests can fire ticks and inspect cancellation.
  const intervals = [];
  const setIntervalStub = (fn, ms) => { const id = intervals.length; intervals.push({ fn, ms, cleared: false }); return id; };
  const clearIntervalStub = id => { if (intervals[id]) intervals[id].cleared = true; };
  const shims = `
    function esc(s) {
      return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    }
  `;
  const exports = {};
  const fn = new Function(
    'exports', 'document', 'fetch', 'setInterval', 'clearInterval',
    shims + src + `
      exports.traktStartAuth = traktStartAuth;
      exports.traktPoll = traktPoll;
      exports.traktExpireAuth = traktExpireAuth;
      exports.expiresAt = () => traktExpiresAt;
      exports.setExpiresAt = v => { traktExpiresAt = v; };
    `
  );
  fn(exports, documentStub, fetchImpl, setIntervalStub, clearIntervalStub);
  return { exports, elements, documentStub, intervals };
}

const startResp = {
  ok: true,
  json: async () => ({ user_code: 'ABCD1234', verification_url: 'https://trakt.tv/activate', expires_in: 600 }),
};

describe('traktStartAuth', () => {
  it('re-enables the button as a restart affordance once the code is shown', async () => {
    const { exports, elements } = loadModule(async () => startResp);
    await exports.traktStartAuth();
    const btn = elements['trakt-auth-btn'];
    expect(btn.disabled).toBe(false);
    expect(btn.textContent).toBe('Restart authorization');
    expect(elements['trakt-auth-body'].innerHTML).toContain('ABCD1234');
  });

  it('resets the button when the start request fails', async () => {
    const { exports, elements } = loadModule(async () => ({ ok: false, text: async () => 'bad credentials' }));
    await exports.traktStartAuth();
    const btn = elements['trakt-auth-btn'];
    expect(btn.disabled).toBe(false);
    expect(btn.textContent).toBe('Authorize');
    expect(elements['trakt-auth-body'].innerHTML).toContain('bad credentials');
  });
});

describe('code expiry', () => {
  it('countdown reaching zero stops both timers, shows a retry message, and resets the button', async () => {
    const { exports, elements, intervals } = loadModule(async () => startResp);
    await exports.traktStartAuth();
    expect(intervals.length).toBe(2); // countdown + poll

    // Force expiry and fire a countdown tick.
    exports.setExpiresAt(Date.now() - 1000);
    intervals[0].fn();

    expect(intervals[0].cleared).toBe(true);
    expect(intervals[1].cleared).toBe(true);
    expect(elements['trakt-auth-body'].innerHTML).toContain('Code expired');
    expect(elements['trakt-auth-body'].innerHTML).toContain('Authorize');
    expect(elements['trakt-auth-btn'].disabled).toBe(false);
    expect(elements['trakt-auth-btn'].textContent).toBe('Authorize');
    expect(exports.expiresAt()).toBe(null);
  });

  it('traktPoll detects expiry even if the countdown tick was missed', async () => {
    let polled = 0;
    const { exports, elements } = loadModule(async url => {
      if (url === '/api/trakt/auth/poll') { polled++; return { ok: true, json: async () => ({ status: 'pending' }) }; }
      return startResp;
    });
    await exports.traktStartAuth();
    exports.setExpiresAt(Date.now() - 1000);
    await exports.traktPoll();
    expect(polled).toBe(0); // expired → no poll request
    expect(elements['trakt-auth-body'].innerHTML).toContain('Code expired');
  });
});

describe('poll visibility gating', () => {
  it('skips the poll request while the page is hidden', async () => {
    let polled = 0;
    const { exports, documentStub } = loadModule(async url => {
      if (url === '/api/trakt/auth/poll') { polled++; return { ok: true, json: async () => ({ status: 'pending' }) }; }
      return startResp;
    });
    await exports.traktStartAuth();
    documentStub.hidden = true;
    await exports.traktPoll();
    expect(polled).toBe(0);
    documentStub.hidden = false;
    await exports.traktPoll();
    expect(polled).toBe(1);
  });
});

describe('poll terminal states', () => {
  it('authorized: stops polling and resets the button', async () => {
    const { exports, elements, intervals } = loadModule(async url => {
      if (url === '/api/trakt/auth/poll') return { ok: true, json: async () => ({ status: 'authorized' }) };
      return startResp;
    });
    await exports.traktStartAuth();
    await exports.traktPoll();
    expect(intervals[1].cleared).toBe(true);
    expect(elements['trakt-auth-btn'].textContent).toBe('Authorize');
    expect(elements['trakt-auth-body'].innerHTML).toContain('successful');
  });
});
