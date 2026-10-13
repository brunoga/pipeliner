// ── config editor ─────────────────────────────────────────────────────────────

// True when the last /api/config load failed. While set, saving is blocked —
// an empty editor after a failed load is indistinguishable from an empty
// config, and saving it would overwrite the real file.
let configLoadFailed = false;

// Content as last loaded from / accepted by the server. Drives the
// beforeunload unsaved-changes prompt. null until the first successful load.
let lastSavedContent = null;

function configLoadErrorMsg(detail) {
  return 'could not load config (' + detail + ') — refusing to save until reload succeeds. Click Validate to retry.';
}

// Which request is in flight, if any: null | 'save' | 'validate'.
//
// This is a second, independent reason for a toolbar button to be disabled —
// the first being configLoadFailed, which persists until a load succeeds.
// They have to compose: finishing a save must not re-enable a button that a
// failed load disabled, which is why neither one writes `disabled` directly.
let configBusy = null;

// A save is not a quick round trip. The server parses the config, validates
// every graph, builds every plugin, writes the file and resets the scheduler
// before it answers — seconds, not milliseconds, on a config of any size.
// Until this existed the toolbar gave no sign of that: the status line was
// blank and the button stayed live, so the natural response to "nothing
// happened" was to click again, and POST /api/config has no version check to
// catch the resulting race.
const CONFIG_BUTTONS = {
  validate: { id: 'btn-config-validate', idle: 'Validate',      busy: 'Validating…' },
  save:     { id: 'btn-config-save',     idle: 'Save & Reload', busy: 'Saving…' },
};

// Bound on how long a button may stay disabled. fetch() has no timeout of its
// own, so without this a server that accepts the connection and then never
// answers would leave the toolbar permanently dead — a worse failure than the
// double-click this guards against, and one introduced by the guard itself.
const CONFIG_REQUEST_TIMEOUT_MS = 120000;

// updateConfigButtons is the only place that writes either button's enabled
// state or label, so the two reasons for disabling cannot fight each other.
function updateConfigButtons() {
  if (typeof document.getElementById !== 'function') return;
  for (const which of Object.keys(CONFIG_BUTTONS)) {
    const spec = CONFIG_BUTTONS[which];
    const btn = document.getElementById(spec.id);
    if (!btn) continue;
    const busy = configBusy === which;
    // Validate stays available through a failed load — it is the documented
    // way to retry one — so only Save answers to configLoadFailed.
    btn.disabled = !!configBusy || (which === 'save' && configLoadFailed);
    btn.textContent = busy ? spec.busy : spec.idle;
    btn.title = configBusy
      ? 'Waiting for the server…'
      : (which === 'save' && configLoadFailed)
        ? 'Saving is disabled: the config failed to load. Click Validate to retry loading.'
        : '';
  }
}

// setSaveBlocked records that the config failed to load and refreshes the
// toolbar. It owns configLoadFailed so the flag and the button cannot drift.
function setSaveBlocked(blocked) {
  configLoadFailed = blocked;
  updateConfigButtons();
}

// withConfigBusy runs one toolbar request with both buttons disabled and the
// active one relabelled, restoring them however it ends. A second call while
// one is in flight is dropped rather than queued: the two requests would race
// on the same file with no version check to order them.
async function withConfigBusy(which, fn) {
  if (configBusy) return;
  configBusy = which;
  updateConfigButtons();
  const ctl = typeof AbortController === 'function' ? new AbortController() : null;
  const timer = ctl ? setTimeout(() => ctl.abort(), CONFIG_REQUEST_TIMEOUT_MS) : null;
  try {
    return await fn(ctl ? ctl.signal : undefined);
  } finally {
    if (timer) clearTimeout(timer);
    configBusy = null;
    updateConfigButtons();
  }
}

async function loadConfig() {
  // switchView sets up canvas event listeners (initCanvasEvents) on first call.
  // It also does an initial sync on the (empty) editor — that's fine.
  switchView('visual');
  try {
    const r = await fetch('/api/config');
    if (!r.ok) {
      configLoadFailed = true;
      setSaveBlocked(true);
      showConfigErrors([configLoadErrorMsg('HTTP ' + r.status)]);
      return;
    }
    const { content } = await r.json();
    document.getElementById('config-editor').value = content;
    configLoaded = true;
    configLoadFailed = false;
    lastSavedContent = content;
    setSaveBlocked(false);
    showConfigErrors([]);
    syncHighlight();
    textToVisualSync(); // re-sync now that we have actual content
  } catch (e) {
    console.error('loadConfig:', e);
    configLoadFailed = true;
    setSaveBlocked(true);
    showConfigErrors([configLoadErrorMsg(networkErrMsg(e))]);
  }
}

// configDirty reports whether the editor differs from the last content
// loaded from or saved to the server.
function configDirty() {
  if (lastSavedContent === null) return false;
  const ta = document.getElementById('config-editor');
  return !!ta && ta.value !== lastSavedContent;
}

// Warn before navigating away with unsaved config edits.
if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('beforeunload', e => {
    if (!configDirty()) return;
    e.preventDefault();
    e.returnValue = ''; // required by Chrome to show the prompt
  });
}

// retryConfigLoad re-attempts the /api/config fetch after a failed load.
// Unlike loadConfig it never clobbers text the user typed into the editor
// while it was in the failed state — it only fills an empty editor. On
// success the failure flag clears and Save is unblocked.
async function retryConfigLoad() {
  try {
    const r = await fetch('/api/config');
    if (!r.ok) return; // still failing — Save stays blocked
    const { content } = await r.json();
    const ta = document.getElementById('config-editor');
    if (ta && ta.value === '') {
      ta.value = content;
      syncHighlight();
      textToVisualSync();
    }
    configLoaded = true;
    configLoadFailed = false;
    lastSavedContent = content;
    setSaveBlocked(false);
    showConfigErrors([]);
  } catch (_) { /* still failing — Save stays blocked */ }
}

async function validateConfig() {
  // A failed initial load leaves the editor empty and Save blocked; retry
  // the load so the user has a recovery path without a page refresh.
  // Validation itself is a harmless dry-run, so it proceeds either way.
  if (configLoadFailed) await retryConfigLoad();
  return withConfigBusy('validate', signal => validateConfigRequest(signal));
}

async function validateConfigRequest(signal) {
  const content = document.getElementById('config-editor').value;
  setConfigStatus('pending', '⏳ Validating…');
  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content, dry_run: true}),
      signal,
    });
    const body = await r.json();
    if (r.status === 422) {
      showConfigErrors(body.errors || []);
      showConfigWarnings(body.warnings || []);
      setConfigStatus('err', '✗ ' + (body.errors||[]).length + ' error' + ((body.errors||[]).length !== 1 ? 's' : ''));
    } else if (r.ok) {
      showConfigErrors([]);
      showConfigWarnings(body.warnings || []);
      const wc = (body.warnings||[]).length;
      setConfigStatus('ok', wc ? `✓ Valid (${wc} warning${wc !== 1 ? 's' : ''})` : '✓ Config is valid');
      textToVisualSync();
    } else {
      setConfigStatus('err', 'Server error: ' + r.status);
    }
  } catch (e) {
    setConfigStatus('err', networkErrMsg(e));
  }
}

async function saveConfig() {
  if (configLoadFailed) {
    // Defense in depth: the button is disabled, but block here too in case
    // the handler is invoked some other way.
    setConfigStatus('err', '✗ Config failed to load — refusing to save. Click Validate to retry loading.');
    return;
  }
  return withConfigBusy('save', signal => saveConfigRequest(signal));
}

async function saveConfigRequest(signal) {
  const content = document.getElementById('config-editor').value;
  // Named for what the server is actually doing, which is most of a restart:
  // the wait is the reload, not the write.
  setConfigStatus('pending', '⏳ Saving and reloading…');
  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content}),
      signal,
    });
    if (r.status === 422) {
      const resp = await r.json();
      showConfigErrors(resp.errors || []);
      showConfigWarnings(resp.warnings || []);
      setConfigStatus('err', '✗ ' + (resp.errors||[]).length + ' error' + ((resp.errors||[]).length !== 1 ? 's' : '') + ' — not saved');
      return;
    }
    if (!r.ok) { setConfigStatus('err', 'Save failed: ' + r.status); return; }
    showConfigErrors([]);
    lastSavedContent = content; // accepted by the server — clears the unsaved-changes prompt
    const body = await r.json();
    showConfigWarnings(body.warnings || []);
    if (body.status === 'reloaded') {
      setConfigStatus('ok', '✓ Saved and reloaded');
      refresh();
      textToVisualSync();
    } else if (body.status === 'saved') {
      // Config was saved but the reload step failed.
      setConfigStatus('err', '⚠ Saved — reload failed: ' + (body.warning || 'unknown error'));
    } else {
      // Tasks were running; reload is queued for when they finish.
      setConfigStatus('pending', '⏳ Saved — will reload when tasks finish');
      pollUntilIdle();
    }
  } catch (e) {
    setConfigStatus('err', networkErrMsg(e));
  }
}

// pollUntilIdle polls /api/status until no tasks are running, then refreshes
// the dashboard and clears the pending config status. Called after a save
// that returned "pending" because tasks were running at the time.
// Gives up after 5 minutes in case the server gets stuck.
function pollUntilIdle() {
  const deadline = Date.now() + 5 * 60 * 1000;
  const check = async () => {
    if (Date.now() > deadline) {
      setConfigStatus('err', '⚠ Saved — reload timed out. Check the live log for details.');
      return;
    }
    try {
      const r = await fetch('/api/status');
      if (!r.ok) return;
      const { tasks } = await r.json();
      if (!(tasks || []).some(t => t.running)) {
        setConfigStatus('ok', '✓ Saved and reloaded');
        refresh();
        textToVisualSync();
        return;
      }
    } catch (_) { /* keep polling */ }
    setTimeout(check, 2000);
  };
  setTimeout(check, 1000);
}

// networkErrMsg converts a fetch() exception into a human-readable message.
// "TypeError: Failed to fetch" means the server closed the connection without
// responding — usually a panic or crash in the request handler.
function networkErrMsg(e) {
  // An abort here is our own timeout firing, not the user navigating away:
  // nothing else aborts these requests.
  if (e && (e.name === 'AbortError' || String(e).includes('AbortError'))) {
    return 'No answer from the server after ' + Math.round(CONFIG_REQUEST_TIMEOUT_MS / 1000) +
           's — it may still be reloading. Check the live log before retrying, and reload this page to see the result.';
  }
  if (e instanceof TypeError && e.message.toLowerCase().includes('fetch')) {
    return 'No response from server — pipeliner is not running, or the server crashed while parsing the config. Check the live log for details.';
  }
  return String(e);
}

function setConfigStatus(cls, msg) {
  const el = document.getElementById('config-status');
  el.className = 'config-status' + (cls ? ' ' + cls : '');
  el.textContent = msg;
}

function showConfigErrors(errors) {
  const el = document.getElementById('config-errors');
  if (!errors || errors.length === 0) {
    el.style.display = 'none';
    el.textContent = '';
  } else {
    el.style.display = 'block';
    el.textContent = errors.join('\n');
  }
}

function showConfigWarnings(warnings) {
  const el = document.getElementById('config-warnings');
  if (!el) return;
  if (!warnings || warnings.length === 0) {
    el.style.display = 'none';
    el.textContent = '';
  } else {
    el.style.display = 'block';
    el.textContent = warnings.join('\n');
  }
}


// ── config history ───────────────────────────────────────────────────────────
// Every successful save snapshots the replaced config server-side. History
// opens the version list in a floating panel; picking one loads it into the
// TEXT editor for review — saving it is the rollback (which itself snapshots
// the config it replaces, so rollbacks are undoable).

async function openConfigHistory() {
  let versions = [];
  try {
    const r = await fetch('/api/config/history');
    if (r.ok) versions = (await r.json()).versions || [];
  } catch (e) { /* rendered as empty below */ }

  let m = document.getElementById('config-history-modal');
  if (!m) {
    m = document.createElement('div');
    m.id = 'config-history-modal';
    m.className = 'app-modal';
    m.innerHTML = `
      <div class="app-modal-backdrop" onclick="closeConfigHistory()"></div>
      <div class="app-modal-panel" role="dialog" aria-modal="true" aria-label="Config history">
        <div class="app-modal-header">
          <span class="app-modal-title">Config history</span>
          <button class="app-modal-close" onclick="closeConfigHistory()" aria-label="Close">✕</button>
        </div>
        <div class="app-modal-body" id="config-history-body"></div>
      </div>`;
    document.body.appendChild(m);
  }
  document.getElementById('config-history-body').innerHTML = configHistoryHTML(versions);
  m.hidden = false;
}

function closeConfigHistory() {
  const m = document.getElementById('config-history-modal');
  if (m) m.hidden = true;
}

// configHistoryHTML renders the version list. Pure for testing.
function configHistoryHTML(versions) {
  if (!versions.length) {
    return '<div class="db-empty">No snapshots yet — one is taken automatically each time the config is saved.</div>';
  }
  let html = '<div class="match-hint">Load a version into the text editor to review it; Save &amp; Reload applies it (the replaced config is snapshotted too, so a rollback is undoable).</div>';
  for (const v of versions) {
    const when = v.saved_at ? new Date(v.saved_at).toLocaleString() : v.id;
    html += `<div class="task-history-row"><div class="task-history-line">
      <span class="task-history-when">${esc(when)}</span>
      <span class="task-history-dur">${(v.size / 1024).toFixed(1)} KB</span>
      <button class="thr-inspect" onclick="loadConfigVersion(${esc(JSON.stringify(v.id))})">load</button>
    </div></div>`;
  }
  return html;
}

async function loadConfigVersion(id) {
  try {
    const r = await fetch('/api/config/history/' + encodeURIComponent(id));
    if (!r.ok) return;
    const { content } = await r.json();
    const ta = document.getElementById('config-editor');
    ta.value = content;
    ve_textDirty = true;
    syncHighlight();
    closeConfigHistory();
    switchView('text');
    setConfigStatus('ok', 'Loaded snapshot — review, then Save & Reload to roll back');
  } catch (e) { /* leave the editor untouched */ }
}
