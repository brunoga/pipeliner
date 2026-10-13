/**
 * Tests for config-editor.js load-failure handling and the unsaved-changes
 * guard:
 *
 *   - a failed /api/config load surfaces an error and blocks saveConfig
 *     (an empty editor after a failed load must never overwrite the real
 *     config on disk)
 *   - Validate retries the load when the last one failed
 *   - configDirty tracks the editor against the last loaded/saved content
 *     (drives the beforeunload prompt)
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __dir = dirname(fileURLToPath(import.meta.url));
const src   = readFileSync(join(__dir, '..', 'config-editor.js'), 'utf8');

function makeNode() {
  return {
    value: '',
    textContent: '',
    className: '',
    disabled: false,
    title: '',
    style: { display: '' },
  };
}

function loadModule(fetchImpl) {
  const saveBtn     = makeNode();
  const validateBtn = makeNode();
  const elements = {
    'config-editor':      makeNode(),
    'config-status':      makeNode(),
    'config-errors':      makeNode(),
    'config-warnings':    makeNode(),
    'btn-config-save':     saveBtn,
    'btn-config-validate': validateBtn,
  };
  const documentStub = {
    getElementById: id => elements[id],
    querySelector: sel => (sel === '.btn-config.primary' ? saveBtn : null),
  };
  const beforeUnloadHandlers = [];
  const windowStub = {
    addEventListener: (name, fn) => { if (name === 'beforeunload') beforeUnloadHandlers.push(fn); },
  };
  // Shims for functions defined in other scripts (dashboard.js, visual-editor.js).
  const shims = `
    var configLoaded = false;
    function switchView() {}
    function syncHighlight() {}
    function textToVisualSync() {}
    function refresh() {}
    function pollUntilIdleShim() {}
  `;
  const exports = {};
  const fn = new Function(
    'exports', 'document', 'window', 'fetch', 'setTimeout', 'clearTimeout', 'console',
    shims + src + `
      exports.loadConfig = loadConfig;
      exports.validateConfig = validateConfig;
      exports.saveConfig = saveConfig;
      exports.configDirty = configDirty;
      exports.isLoadFailed = () => configLoadFailed;
      exports.isConfigLoaded = () => configLoaded;
    `
  );
  fn(exports, documentStub, windowStub, fetchImpl, () => null, () => {},
     { error: () => {}, warn: () => {} });
  return { exports, elements, saveBtn, validateBtn, beforeUnloadHandlers };
}

describe('loadConfig failure handling', () => {
  it('shows an error and disables Save when /api/config returns non-2xx', async () => {
    const { exports, elements, saveBtn } = loadModule(async () => ({ ok: false, status: 503 }));
    await exports.loadConfig();
    expect(exports.isLoadFailed()).toBe(true);
    expect(saveBtn.disabled).toBe(true);
    expect(saveBtn.title).toContain('Validate');
    expect(elements['config-errors'].textContent).toContain('could not load config (HTTP 503)');
    expect(elements['config-errors'].textContent).toContain('refusing to save');
    // The editor stays empty but configLoaded stays false — nothing to save.
    expect(exports.isConfigLoaded()).toBe(false);
  });

  it('shows an error and disables Save when the fetch throws', async () => {
    const { exports, saveBtn } = loadModule(async () => { throw new TypeError('Failed to fetch'); });
    await exports.loadConfig();
    expect(exports.isLoadFailed()).toBe(true);
    expect(saveBtn.disabled).toBe(true);
  });

  it('clears the failure state and re-enables Save on a successful load', async () => {
    let fail = true;
    const { exports, elements, saveBtn } = loadModule(async () =>
      fail ? { ok: false, status: 500 }
           : { ok: true, json: async () => ({ content: 'pipeline("x")' }) });
    await exports.loadConfig();
    expect(saveBtn.disabled).toBe(true);
    fail = false;
    await exports.loadConfig();
    expect(exports.isLoadFailed()).toBe(false);
    expect(saveBtn.disabled).toBe(false);
    expect(saveBtn.title).toBe('');
    expect(elements['config-editor'].value).toBe('pipeline("x")');
    expect(elements['config-errors'].style.display).toBe('none');
  });

  it('saveConfig refuses to POST while the load has failed', async () => {
    const posts = [];
    const { exports, elements } = loadModule(async (url, init) => {
      if (init && init.method === 'POST') { posts.push(url); return { ok: true, json: async () => ({}) }; }
      return { ok: false, status: 500 };
    });
    await exports.loadConfig();
    await exports.saveConfig();
    expect(posts).toEqual([]);
    expect(elements['config-status'].textContent).toContain('refusing to save');
  });

  it('validateConfig retries the load first and proceeds once it succeeds', async () => {
    let fail = true;
    const calls = [];
    const { exports } = loadModule(async (url, init) => {
      calls.push({ url, method: init?.method || 'GET' });
      if (!init || !init.method) {
        return fail ? { ok: false, status: 500 }
                    : { ok: true, json: async () => ({ content: 'pipeline("x")' }) };
      }
      return { ok: true, status: 200, json: async () => ({ warnings: [] }) };
    });
    await exports.loadConfig();          // fails
    expect(exports.isLoadFailed()).toBe(true);
    fail = false;
    await exports.validateConfig();      // retries load, then validates
    expect(exports.isLoadFailed()).toBe(false);
    const gets = calls.filter(c => c.method === 'GET').length;
    const posts = calls.filter(c => c.method === 'POST').length;
    expect(gets).toBe(2);  // initial failed load + retry
    expect(posts).toBe(1); // the validation POST went through
  });

  it('validateConfig still validates when the retry also fails, but Save stays blocked', async () => {
    const posts = [];
    const { exports, saveBtn } = loadModule(async (url, init) => {
      if (init && init.method === 'POST') {
        posts.push(JSON.parse(init.body));
        return { ok: true, status: 200, json: async () => ({ warnings: [] }) };
      }
      return { ok: false, status: 500 };
    });
    await exports.loadConfig();
    await exports.validateConfig();
    // Validation is a harmless dry-run — it must not be held hostage by a
    // broken /api/config, but saving remains blocked.
    expect(posts.length).toBe(1);
    expect(exports.isLoadFailed()).toBe(true);
    expect(saveBtn.disabled).toBe(true);
  });

  it('the retry never clobbers content typed while the editor was in the failed state', async () => {
    let fail = true;
    const { exports, elements } = loadModule(async (url, init) => {
      if (init && init.method === 'POST') return { ok: true, status: 200, json: async () => ({ warnings: [] }) };
      return fail ? { ok: false, status: 500 }
                  : { ok: true, json: async () => ({ content: 'server content' }) };
    });
    await exports.loadConfig(); // fails
    elements['config-editor'].value = 'typed by user';
    fail = false;
    await exports.validateConfig(); // retry succeeds
    expect(exports.isLoadFailed()).toBe(false);
    expect(elements['config-editor'].value).toBe('typed by user'); // not overwritten
  });
});

describe('unsaved-changes tracking', () => {
  async function loadedModule(extraFetch) {
    const mod = loadModule(async (url, init) => {
      if (init && init.method === 'POST') return extraFetch(url, init);
      return { ok: true, json: async () => ({ content: 'original' }) };
    });
    await mod.exports.loadConfig();
    return mod;
  }

  it('is clean right after load, dirty after an edit', async () => {
    const { exports, elements } = await loadedModule(async () => ({ ok: true, json: async () => ({}) }));
    expect(exports.configDirty()).toBe(false);
    elements['config-editor'].value = 'edited';
    expect(exports.configDirty()).toBe(true);
  });

  it('is clean before the first load (nothing to lose)', () => {
    const { exports, elements } = loadModule(async () => ({ ok: false, status: 500 }));
    elements['config-editor'].value = 'typed into a never-loaded editor';
    expect(exports.configDirty()).toBe(false);
  });

  it('becomes clean again after a successful save', async () => {
    const { exports, elements } = await loadedModule(async () => (
      { ok: true, status: 200, json: async () => ({ status: 'reloaded', warnings: [] }) }
    ));
    elements['config-editor'].value = 'edited';
    expect(exports.configDirty()).toBe(true);
    await exports.saveConfig();
    expect(exports.configDirty()).toBe(false);
  });

  it('stays dirty when the save is rejected with validation errors', async () => {
    const { exports, elements } = await loadedModule(async () => (
      { ok: false, status: 422, json: async () => ({ errors: ['bad'], warnings: [] }) }
    ));
    elements['config-editor'].value = 'edited';
    await exports.saveConfig();
    expect(exports.configDirty()).toBe(true);
  });

  it('registers a beforeunload handler that prompts only when dirty', async () => {
    const { exports, elements, beforeUnloadHandlers } = await loadedModule(async () => ({ ok: true, json: async () => ({}) }));
    expect(beforeUnloadHandlers.length).toBe(1);
    const handler = beforeUnloadHandlers[0];

    let ev = { returnValue: undefined, preventDefault() { this.prevented = true; } };
    handler(ev);
    expect(ev.prevented).toBeUndefined(); // clean → no prompt

    elements['config-editor'].value = 'edited';
    ev = { returnValue: undefined, preventDefault() { this.prevented = true; } };
    handler(ev);
    expect(ev.prevented).toBe(true);
    expect(ev.returnValue).toBe('');
  });
});

describe('toolbar busy state', () => {
  // A save is most of a restart server-side — parse, validate, build every
  // plugin, write the file, reset the scheduler. These pin that the toolbar
  // says so while it happens, and that it recovers however the request ends.

  // deferredFetch hands back a fetch stub plus the resolve/reject handles for
  // the request it is holding open, so a test can inspect the toolbar at the
  // exact moment the request is in flight.
  function deferredFetch(initial) {
    let release;
    const calls = [];
    const fetchImpl = async (url, opts) => {
      calls.push({ url, opts });
      // The first call is the initial GET /api/config load.
      if (!opts || opts.method !== 'POST') return initial;
      return new Promise((resolve, reject) => { release = { resolve, reject }; });
    };
    return { fetchImpl, calls, release: () => release };
  }

  const okLoad = { ok: true, json: async () => ({ content: 'src = input("rss")\n' }) };

  it('disables both buttons and relabels Save while the save is in flight', async () => {
    const { fetchImpl, release } = deferredFetch(okLoad);
    const { exports, saveBtn, validateBtn, elements } = loadModule(fetchImpl);
    await exports.loadConfig();
    expect(saveBtn.disabled).toBe(false);

    const pending = exports.saveConfig();
    expect(saveBtn.disabled).toBe(true);
    expect(saveBtn.textContent).toBe('Saving…');
    expect(validateBtn.disabled).toBe(true);
    expect(elements['config-status'].textContent).toContain('Saving and reloading');

    release().resolve({ ok: true, status: 200, json: async () => ({ status: 'reloaded', warnings: [] }) });
    await pending;

    expect(saveBtn.disabled).toBe(false);
    expect(saveBtn.textContent).toBe('Save & Reload');
    expect(validateBtn.disabled).toBe(false);
    expect(validateBtn.textContent).toBe('Validate');
  });

  it('relabels Validate, not Save, while validating', async () => {
    const { fetchImpl, release } = deferredFetch(okLoad);
    const { exports, saveBtn, validateBtn } = loadModule(fetchImpl);
    await exports.loadConfig();

    const pending = exports.validateConfig();
    expect(validateBtn.textContent).toBe('Validating…');
    expect(saveBtn.textContent).toBe('Save & Reload');
    expect(saveBtn.disabled).toBe(true); // still blocked while a request is out

    release().resolve({ ok: true, status: 200, json: async () => ({ warnings: [] }) });
    await pending;
    expect(validateBtn.textContent).toBe('Validate');
    expect(saveBtn.disabled).toBe(false);
  });

  it('drops a second save fired while the first is still in flight', async () => {
    const { fetchImpl, calls, release } = deferredFetch(okLoad);
    const { exports } = loadModule(fetchImpl);
    await exports.loadConfig();

    const first = exports.saveConfig();
    await exports.saveConfig(); // the double-click
    const posts = () => calls.filter(c => c.opts && c.opts.method === 'POST');
    expect(posts().length).toBe(1);

    release().resolve({ ok: true, status: 200, json: async () => ({ status: 'reloaded', warnings: [] }) });
    await first;
    expect(posts().length).toBe(1);
  });

  it('restores the toolbar when the request fails', async () => {
    const { fetchImpl, release } = deferredFetch(okLoad);
    const { exports, saveBtn, validateBtn, elements } = loadModule(fetchImpl);
    await exports.loadConfig();

    const pending = exports.saveConfig();
    expect(saveBtn.disabled).toBe(true);
    release().reject(new TypeError('Failed to fetch'));
    await pending;

    expect(saveBtn.disabled).toBe(false);
    expect(saveBtn.textContent).toBe('Save & Reload');
    expect(validateBtn.disabled).toBe(false);
    expect(elements['config-status'].textContent).toContain('No response from server');
  });

  // The two reasons a button is disabled are independent, and finishing a
  // request must not undo the other one.
  it('leaves Save blocked after a validate when the load had failed', async () => {
    let n = 0;
    const calls = [];
    const fetchImpl = async (url, opts) => {
      calls.push({ url, opts });
      if (!opts || opts.method !== 'POST') return { ok: false, status: 503 }; // load + retry both fail
      n++;
      return { ok: true, status: 200, json: async () => ({ warnings: [] }) };
    };
    const { exports, saveBtn, validateBtn } = loadModule(fetchImpl);
    await exports.loadConfig();
    expect(saveBtn.disabled).toBe(true);

    await exports.validateConfig();
    expect(n).toBe(1);                      // validation still ran
    expect(validateBtn.disabled).toBe(false); // Validate is usable again
    expect(saveBtn.disabled).toBe(true);      // but Save is still blocked
    expect(saveBtn.title).toContain('Validate to retry');
  });
});
