'use strict';


const MAX_LINES = 500;
let logLines = []; // [{el, raw}]
let startedAt = Date.now();

// ── polling ───────────────────────────────────────────────────────────────────

async function refresh() {
  try {
    const [sr, hr] = await Promise.all([fetch('/api/status'), fetch('/api/history')]);
    const status  = await sr.json();
    const history = await hr.json();
    const tasks   = status.tasks || [];
    render(tasks, history);
    if (dbLoaded) loadDBSidebar();
    document.getElementById('header-meta').textContent =
      'up ' + fmtUptime(Math.round((Date.now() - startedAt) / 1000)) +
      ' · ' + tasks.length + ' task' + (tasks.length !== 1 ? 's' : '');
    const allRunning = tasks.length > 0 && tasks.every(t => t.running);
    const runAllBtn = document.getElementById('btn-run-all');
    runAllBtn.disabled = allRunning;
    runAllBtn.title = allRunning ? 'All tasks are already running' : '';
  } catch (e) {
    document.getElementById('header-meta').textContent = 'error — retrying';
  }
}

function render(tasks, history) {
  const grid = document.getElementById('task-grid');
  if (!tasks.length) {
    grid.innerHTML = '<div class="no-tasks">No tasks configured.</div>';
    return;
  }
  grid.innerHTML = tasks.map(t => card(t, (history[t.name] || [])[0])).join('');
}

function card(t, last) {
  const nextDate   = t.nextRun ? new Date(t.nextRun) : null;
  const schedLabel = nextDate ? fmtDatetime(nextDate) : (t.schedule ? t.schedule : 'manual');
  const schedOpacity = (!nextDate && !t.schedule) ? ' style="opacity:.5"' : '';
  const schedBadge = `<span class="task-schedule"${schedOpacity} title="${esc(t.schedule || '')}">${esc(schedLabel)}</span>`;

  const nextStr = nextDate ? 'in ' + relTime(nextDate) : '—';
  const lastStr = last ? relTime(new Date(last.at)) + ' ago' : 'never';
  const durStr  = last ? ' · ' + last.duration : '';

  const stats = last
    ? `<div class="task-stats">
         <div class="stat"><div class="stat-val a">${last.accepted}</div><div class="stat-lbl">accepted</div></div>
         <div class="stat"><div class="stat-val r">${last.rejected}</div><div class="stat-lbl">rejected</div></div>
         <div class="stat"><div class="stat-val f">${last.failed}</div><div class="stat-lbl">failed</div></div>
       </div>`
    : `<div class="task-empty">No runs yet</div>`;

  const errLine = (last && last.err)
    ? `<div class="task-err">⚠ ${esc(last.err)}</div>` : '';

  const runBtn = t.running
    ? `<button class="btn-run running" disabled>Running…</button>`
    : `<button class="btn-run" onclick="triggerRun(${esc(JSON.stringify(t.name))}, this)">Run now</button>`;

  return `
    <div class="task-card">
      <div class="task-card-header">
        <div class="task-name">${esc(t.name)}</div>
        <div style="display:flex;gap:6px;align-items:center">${schedBadge}</div>
      </div>
      <div class="task-timing">
        <span><span>Next run</span><b>${nextStr}</b></span>
        <span><span>Last run</span><b>${lastStr}${esc(durStr)}</b></span>
      </div>
      ${stats}
      ${errLine}
      ${runBtn}
    </div>`;
}

// ── manual trigger ────────────────────────────────────────────────────────────

async function triggerRun(name, btn) {
  btn.disabled = true;
  btn.textContent = 'Triggered…';
  btn.classList.add('triggered');
  try {
    await fetch('/api/tasks/' + encodeURIComponent(name) + '/run', { method: 'POST' });
  } catch (_) {}
  setTimeout(() => {
    btn.disabled = false;
    btn.textContent = 'Run now';
    btn.classList.remove('triggered');
    refresh();
  }, 3000);
}

// ── SSE log stream ────────────────────────────────────────────────────────────

function connectLogs() {
  const dot  = document.getElementById('log-dot');
  const text = document.getElementById('log-status-text');
  const es   = new EventSource('/api/logs');

  es.onopen = () => { dot.classList.add('on'); text.textContent = 'connected'; };

  es.onmessage = ev => appendLog(ev.data);

  es.onerror = () => {
    dot.classList.remove('on');
    text.textContent = 'reconnecting…';
    es.close();
    setTimeout(connectLogs, 3000);
  };
}

function appendLog(line) {
  const el = document.createElement('div');
  el.className = 'log-line';
  el.innerHTML = renderLogLine(line);

  const filter = document.getElementById('log-filter').value.toLowerCase();
  if (filter && !line.toLowerCase().includes(filter)) {
    el.style.display = 'none';
  }

  const con = document.getElementById('log-console');
  const atBottom = con.scrollHeight - con.scrollTop - con.clientHeight < 60;
  con.appendChild(el);
  logLines.push({el, raw: line});
  if (logLines.length > MAX_LINES) { logLines.shift().el.remove(); }
  if (atBottom) con.scrollTop = con.scrollHeight;
}

function clearLog() {
  document.getElementById('log-console').innerHTML = '';
  logLines = [];
}

function applyFilter() {
  const filter = document.getElementById('log-filter').value.toLowerCase();
  for (const {el, raw} of logLines) {
    el.style.display = (!filter || raw.toLowerCase().includes(filter)) ? '' : 'none';
  }
}

// Convert ANSI escape sequences in a log line to HTML spans.
// The server sends ANSI-colored output; this renders it faithfully without
// any message-content guessing.
function renderLogLine(line) {
  return ansiToHtml(line);
}

const ANSI_COLORS = {
  '1':  { bold: true },
  '31': { color: '#f85149' }, // red
  '32': { color: '#3fb950' }, // green
  '33': { color: '#d29922' }, // amber
  '35': { color: '#bc8cff' }, // magenta
  '36': { color: '#58a6ff' }, // cyan
  '90': { color: '#8b949e' }, // gray
};

function ansiToHtml(text) {
  // Split on ESC[...m sequences; odd parts are code strings, even parts are text.
  const parts = text.split(/\x1b\[([0-9;]*)m/);
  let style = {};
  let out = '';

  for (let i = 0; i < parts.length; i++) {
    if (i % 2 === 0) {
      // Text segment — wrap in span only when a style is active.
      const content = parts[i]
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      if (!content) continue;
      const css = [];
      if (style.color) css.push('color:' + style.color);
      if (style.bold)  css.push('font-weight:600');
      out += css.length
        ? `<span style="${css.join(';')}">${content}</span>`
        : content;
    } else {
      // ANSI code segment — update current style state.
      const codes = parts[i] === '' ? ['0'] : parts[i].split(';');
      for (const code of codes) {
        if (code === '0' || code === '') {
          style = {};
        } else {
          const def = ANSI_COLORS[code];
          if (def) Object.assign(style, def);
        }
      }
    }
  }
  return out;
}

// ── helpers ───────────────────────────────────────────────────────────────────

function esc(s) {
  return String(s)
    .replace(/&/g,'&amp;').replace(/</g,'&lt;')
    .replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function relTime(d) {
  const s = Math.round(Math.abs(Date.now() - d) / 1000);
  if (s < 90)    return s + 's';
  if (s < 5400)  return Math.round(s / 60) + 'm';
  if (s < 86400) return Math.round(s / 3600) + 'h';
  return Math.round(s / 86400) + 'd';
}

function fmtUptime(s) {
  if (s < 60)   return s + 's';
  if (s < 3600) return Math.floor(s/60) + 'm ' + (s%60) + 's';
  return Math.floor(s/3600) + 'h ' + Math.floor((s%3600)/60) + 'm';
}

function fmtDatetime(d) {
  return d.toLocaleString(undefined, {month:'short', day:'numeric', hour:'numeric', minute:'2-digit'});
}

// ── run all tasks ─────────────────────────────────────────────────────────────

async function runAll(btn) {
  btn.disabled = true;
  btn.classList.add('triggered');
  btn.textContent = 'Triggered…';
  try {
    await fetch('/api/tasks/run', { method: 'POST' });
  } catch (_) {}
  setTimeout(() => {
    btn.disabled = false;
    btn.classList.remove('triggered');
    btn.textContent = 'Run all';
    refresh();
  }, 3000);
}


// ── tabs ──────────────────────────────────────────────────────────────────────

let configLoaded = false;

function showTab(name) {
  document.getElementById('tab-dashboard').style.display  = name === 'dashboard' ? '' : 'none';
  document.getElementById('tab-config').style.display     = name === 'config'    ? '' : 'none';
  document.getElementById('tab-db').style.display         = name === 'db'        ? '' : 'none';
  document.getElementById('tab-settings').style.display   = name === 'settings'  ? '' : 'none';
  document.getElementById('tab-btn-dashboard').classList.toggle('active', name === 'dashboard');
  document.getElementById('tab-btn-config').classList.toggle('active', name === 'config');
  document.getElementById('tab-btn-db').classList.toggle('active', name === 'db');
  document.getElementById('tab-btn-settings').classList.toggle('active', name === 'settings');
  if (name === 'config' && !configLoaded) loadConfig();
  if (name === 'db') {
    if (!dbLoaded) loadDBTab();
    else {
      loadDBSidebar(); // refresh counts whenever the tab is revisited
      if (dbActiveBucket) selectDBBucket(dbActiveBucket); // refresh content too
    }
  }
}


// ── boot ──────────────────────────────────────────────────────────────────────

refresh();
setInterval(refresh, 10000);
connectLogs();
</script>
