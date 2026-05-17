// ── config editor ─────────────────────────────────────────────────────────────

async function loadConfig() {
  // switchView sets up canvas event listeners (initCanvasEvents) on first call.
  // It also does an initial sync on the (empty) editor — that's fine.
  switchView('visual');
  try {
    const r = await fetch('/api/config');
    if (!r.ok) return;
    const { content } = await r.json();
    document.getElementById('config-editor').value = content;
    configLoaded = true;
    syncHighlight();
    textToVisualSync(); // re-sync now that we have actual content
  } catch (e) { console.error('loadConfig:', e); }
}

async function validateConfig() {
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
    } else {
      setConfigStatus('err', 'Server error: ' + r.status);
    }
  } catch (e) {
    setConfigStatus('err', networkErrMsg(e));
  }
}

async function saveConfig() {
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
    const body = await r.json();
    showConfigWarnings(body.warnings || []);
    if (body.status === 'reloaded') {
      setConfigStatus('ok', '✓ Saved and reloaded');
      refresh();
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
function pollUntilIdle() {
  const check = async () => {
    try {
      const r = await fetch('/api/status');
      if (!r.ok) return;
      const { tasks } = await r.json();
      if (!(tasks || []).some(t => t.running)) {
        setConfigStatus('ok', '✓ Saved and reloaded');
        refresh();
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

