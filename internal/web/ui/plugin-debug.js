'use strict';

// ── plugin debug toggles ──────────────────────────────────────────────────────
//
// Settings-tab section that lets the user enable DEBUG-level output for one
// or more plugins at runtime (matches the cmdline --log-level=debug +
// --log-plugin=name combo, but applies without restart). Backed by the
// PUT /api/log-debug-plugins endpoint, which atomically replaces the override
// set held by clog.PerPluginLevel on the daemon.

let pdLoaded = false;
let pdPlugins = []; // [{name, role, description}]
let pdEnabled = new Set(); // names currently enabled
let pdFilter = '';
let pdStatusTimer = null;

async function loadPluginDebugSettings() {
  if (pdLoaded) return;
  const list = document.getElementById('plugin-debug-list');
  if (!list) return;
  try {
    const [plRes, enRes] = await Promise.all([
      fetch('/api/plugins'),
      fetch('/api/log-debug-plugins'),
    ]);
    if (!plRes.ok) throw new Error('plugins: HTTP ' + plRes.status);
    if (enRes.status === 501) {
      // Backend not wired (rare — happens in tests). Hide the section to
      // avoid showing an inert UI.
      const section = list.closest('.settings-section');
      if (section) section.style.display = 'none';
      return;
    }
    if (!enRes.ok) throw new Error('log-debug-plugins: HTTP ' + enRes.status);
    pdPlugins = (await plRes.json()).sort((a, b) => a.name.localeCompare(b.name));
    const en = await enRes.json();
    pdEnabled = new Set(en.plugins || []);
    pdLoaded = true;
    renderPluginDebugList();
  } catch (e) {
    list.innerHTML = '<div class="plugin-debug-loading">Error: ' + esc(e.message) + '</div>';
  }
}

function filterPluginDebug(value) {
  pdFilter = (value || '').toLowerCase().trim();
  renderPluginDebugList();
}

function renderPluginDebugList() {
  const list = document.getElementById('plugin-debug-list');
  if (!list) return;
  const shown = pdPlugins.filter(p =>
    !pdFilter
    || p.name.toLowerCase().includes(pdFilter)
    || (p.description || '').toLowerCase().includes(pdFilter)
    || (p.role || '').toLowerCase().includes(pdFilter));
  if (!shown.length) {
    list.innerHTML = '<div class="plugin-debug-loading">No plugins match.</div>';
    return;
  }
  // Group by role so source/processor/sink stay visually distinct.
  const byRole = {source: [], processor: [], sink: []};
  for (const p of shown) {
    (byRole[p.role] || byRole.processor).push(p);
  }
  let html = '';
  for (const role of ['source', 'processor', 'sink']) {
    if (!byRole[role].length) continue;
    html += `<div class="plugin-debug-role">${role}</div>`;
    for (const p of byRole[role]) {
      const checked = pdEnabled.has(p.name) ? ' checked' : '';
      const active = pdEnabled.has(p.name) ? ' plugin-debug-row-active' : '';
      html += `<label class="plugin-debug-row${active}">
        <input type="checkbox" class="plugin-debug-cb"${checked}
          onchange="togglePluginDebug(${esc(JSON.stringify(p.name))}, this.checked)">
        <span class="plugin-debug-name">${esc(p.name)}</span>
        <span class="plugin-debug-desc">${esc(p.description || '')}</span>
      </label>`;
    }
  }
  list.innerHTML = html;
}

async function togglePluginDebug(name, on) {
  if (on) pdEnabled.add(name); else pdEnabled.delete(name);
  const sorted = [...pdEnabled].sort();
  try {
    const r = await fetch('/api/log-debug-plugins', {
      method: 'PUT',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({plugins: sorted}),
    });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    const back = await r.json();
    pdEnabled = new Set(back.plugins || []);
    pluginDebugStatus(sorted.length === 0
      ? 'all plugin debug disabled'
      : (on ? 'enabled debug for ' + name : 'disabled debug for ' + name));
  } catch (e) {
    // Revert the local change and re-render so the checkbox snaps back.
    if (on) pdEnabled.delete(name); else pdEnabled.add(name);
    pluginDebugStatus('error: ' + e.message, true);
  }
  renderPluginDebugList();
}

function pluginDebugStatus(msg, isError) {
  const el = document.getElementById('plugin-debug-status');
  if (!el) return;
  el.textContent = msg;
  el.classList.toggle('plugin-debug-status-error', !!isError);
  clearTimeout(pdStatusTimer);
  pdStatusTimer = setTimeout(() => { el.textContent = ''; }, 4000);
}
