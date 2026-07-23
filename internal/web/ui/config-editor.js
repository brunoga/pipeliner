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

// setSaveBlocked disables/enables the Save & Reload button. The button has
// no id in index.html, so it is located by its toolbar classes.
function setSaveBlocked(blocked) {
  if (typeof document.querySelector !== 'function') return;
  const btn = document.querySelector('.btn-config.primary');
  if (!btn) return;
  btn.disabled = blocked;
  btn.title = blocked ? 'Saving is disabled: the config failed to load. Click Validate to retry loading.' : '';
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
  const content = document.getElementById('config-editor').value;
  setConfigStatus('', '');
  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content, dry_run: true}),
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
  const content = document.getElementById('config-editor').value;
  setConfigStatus('', '');
  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({content}),
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

