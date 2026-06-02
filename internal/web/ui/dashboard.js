'use strict';

// ── theme ─────────────────────────────────────────────────────────────────────

function applyTheme(theme) {
  document.body.classList.remove('light', 'dark');
  if (theme === 'light' || theme === 'dark') document.body.classList.add(theme);
  ['dark', 'auto', 'light'].forEach(t => {
    const btn = document.getElementById('theme-btn-' + t);
    if (btn) btn.classList.toggle('active', t === theme);
  });
}

function setTheme(theme) {
  localStorage.setItem('pipeliner-theme', theme);
  applyTheme(theme);
}

// ── polling ───────────────────────────────────────────────────────────────────

const MAX_LINES = 500;
const LOG_HISTORY_LIMIT = 200;
const LOG_HISTORY_SCROLL_THRESHOLD = 40;
let logLines = []; // [{el, raw}] — lines fed by the SSE stream
let historyLines = []; // [{el, raw}] — older lines lazy-loaded from /api/logs/history
let logHistory = { offset: 0, exhausted: false, loading: false, endMarker: null };
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

// True only for the very first render after page load or tab switch.
// Polling refreshes set this to false so cards don't re-animate every 10 s.
let _dashboardFirstRender = true;

function render(tasks, history) {
  const grid = document.getElementById('task-grid');
  if (!tasks.length) {
    grid.innerHTML = '<div class="no-tasks">No tasks configured.</div>';
    _dashboardFirstRender = true;
    return;
  }
  // Suppress animation during background refreshes by adding no-anim before
  // replacing innerHTML — new card nodes inherit animation:none from the class.
  if (!_dashboardFirstRender) grid.classList.add('no-anim');
  grid.innerHTML = tasks.map((t, i) => card(t, (history[t.name] || [])[0], i)).join('');
  _dashboardFirstRender = false;
}

function card(t, last, idx = 0) {
  const nextDate   = t.nextRun ? new Date(t.nextRun) : null;
  const schedLabel = nextDate ? fmtDatetime(nextDate) : (t.schedule ? t.schedule : 'manual');
  const schedOpacity = (!nextDate && !t.schedule) ? ' style="opacity:.5"' : '';
  const schedBadge = `<span class="task-schedule"${schedOpacity} title="${esc(t.schedule || '')}">${esc(schedLabel)}</span>`;

  const nextStr = nextDate ? 'in ' + relTime(nextDate) : '—';
  const dryBadge = (last && last.dry_run) ? ' <span class="dry-badge" title="Last run was a dry-run">DRY</span>' : '';
  const lastStr = last ? relTime(new Date(last.at)) + ' ago' + dryBadge : 'never';
  const durStr  = last ? ' · ' + last.duration : '';

  // Card accent stripe color reflects last run health at a glance.
  let cardColor = 'var(--border)';
  if (t.running)          cardColor = 'var(--accent)';
  else if (last?.failed)  cardColor = 'var(--red)';
  else if (last?.rejected)cardColor = 'var(--amber)';
  else if (last?.accepted)cardColor = 'var(--green)';

  const stats = last
    ? `<div class="task-stats">
         <div class="stat a"><div class="stat-val a">${last.accepted}</div><div class="stat-lbl">accepted</div></div>
         <div class="stat r"><div class="stat-val r">${last.rejected}</div><div class="stat-lbl">rejected</div></div>
         <div class="stat f"><div class="stat-val f">${last.failed}</div><div class="stat-lbl">failed</div></div>
       </div>`
    : `<div class="task-empty">No runs yet</div>`;

  const errLine = (last && last.err)
    ? `<div class="task-err">⚠ ${esc(last.err)}</div>` : '';

  const runBtns = t.running
    ? `<button class="btn-run running" disabled>Running…</button>`
    : `<div class="btn-run-group">
         <button class="btn-run" onclick="triggerRun(${esc(JSON.stringify(t.name))}, this, false)" title="Run with side effects and tracker commits">Run now</button>
         <button class="btn-run btn-dry" onclick="triggerRun(${esc(JSON.stringify(t.name))}, this, true)" title="Dry run — no side effects, no tracker advance">Dry</button>
       </div>`;

  // nth-child handles first 10 cards; inline delay covers beyond that.
  const extraDelay = idx >= 10 ? `;animation-delay:${idx * 50}ms` : '';

  return `
    <div class="task-card" style="--card-color:${cardColor}${extraDelay}">
      <div class="task-card-header">
        <div class="task-name">${esc(t.name)}</div>
        ${schedBadge}
      </div>
      <div class="task-timing">
        <span><span>Next run</span><b>${nextStr}</b></span>
        <span><span>Last run</span><b>${lastStr}${esc(durStr)}</b></span>
      </div>
      ${stats}
      ${errLine}
      ${runBtns}
    </div>`;
}

// ── manual trigger ────────────────────────────────────────────────────────────

async function triggerRun(name, btn, dryRun) {
  const original = btn.textContent;
  btn.disabled = true;
  btn.textContent = dryRun ? 'Dry…' : 'Triggered…';
  btn.classList.add('triggered');
  // Disable the sibling button too so the user can't fire both back-to-back
  // (the daemon would just drop the second, but it's confusing visually).
  const siblings = btn.parentElement.querySelectorAll('button');
  siblings.forEach(s => { if (s !== btn) s.disabled = true; });
  try {
    await fetch('/api/tasks/' + encodeURIComponent(name) + '/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dry_run: !!dryRun }),
    });
  } catch (_) {}
  setTimeout(() => {
    btn.disabled = false;
    btn.textContent = original;
    btn.classList.remove('triggered');
    siblings.forEach(s => { s.disabled = false; });
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

  // Lazy-load older history on scroll-up. Installed here so it only attaches
  // once per page lifetime, regardless of how many SSE reconnects happen.
  const con = document.getElementById('log-console');
  if (con && !con._historyWired) {
    con._historyWired = true;
    con.addEventListener('scroll', maybeLoadLogHistory);
  }
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
  historyLines = [];
  logHistory = { offset: 0, exhausted: false, loading: false, endMarker: null };
}

function applyFilter() {
  const filter = document.getElementById('log-filter').value.toLowerCase();
  for (const {el, raw} of historyLines) {
    el.style.display = (!filter || raw.toLowerCase().includes(filter)) ? '' : 'none';
  }
  for (const {el, raw} of logLines) {
    el.style.display = (!filter || raw.toLowerCase().includes(filter)) ? '' : 'none';
  }
}

// ── scrollback history ───────────────────────────────────────────────────────
//
// The SSE stream only carries lines emitted after the page connected (with a
// short in-memory ring as warm-up). For deeper scrollback we lazy-load older
// chunks from /api/logs/history when the user scrolls within
// LOG_HISTORY_SCROLL_THRESHOLD px of the top.

async function maybeLoadLogHistory() {
  if (logHistory.exhausted || logHistory.loading) return;
  const con = document.getElementById('log-console');
  if (!con) return;
  if (con.scrollTop > LOG_HISTORY_SCROLL_THRESHOLD) return;
  await loadLogHistory();
}

async function loadLogHistory() {
  if (logHistory.exhausted || logHistory.loading) return;
  logHistory.loading = true;
  try {
    const url = `/api/logs/history?offset=${logHistory.offset}&limit=${LOG_HISTORY_LIMIT}`;
    const r = await fetch(url);
    if (!r.ok) return;
    const body = await r.json();
    const lines = Array.isArray(body.lines) ? body.lines : [];
    // The first chunk's newest end usually overlaps the SSE warm-up tail
    // already in the DOM (server reads lines from EOF; the warm-up came
    // from the broadcaster's ring of those same lines). Trim that overlap
    // before prepending so we don't render the visible tail twice — the
    // file copy carries no ANSI codes so the duplicates would also lose
    // their colors. Advance offset by the *original* chunk length so the
    // server-side cursor still skips past the whole fetched window.
    const fresh = trimHistoryOverlap(lines);
    if (fresh.length > 0) {
      prependHistoryLines(fresh);
    }
    logHistory.offset += lines.length;
    if (lines.length < LOG_HISTORY_LIMIT) {
      logHistory.exhausted = true;
      showHistoryEndMarker();
    }
  } catch (_) {
    // Transient fetch failure — leave loading=false so the next scroll retries.
  } finally {
    logHistory.loading = false;
  }
}

// trimHistoryOverlap drops the chunk's newest-end portion that matches
// the page's newest-end portion. The first history fetch always reads
// the file's tail (since `offset=0` means "from EOF"), which is exactly
// the slice the SSE warm-up already showed; without this trim the page
// would render the live tail twice (and the second copy plain because
// the on-disk log carries no ANSI). Comparison strips ANSI so SSE-
// colored lines match their plain file copies. Returns the chunk
// truncated to its genuinely-older prefix.
function trimHistoryOverlap(chunk) {
  if (chunk.length === 0) return chunk;
  // Page lines in chronological order (newest visible line at the end).
  const page = [];
  for (const {raw} of historyLines) page.push(raw);
  for (const {raw} of logLines) page.push(raw);
  if (page.length === 0) return chunk;

  // Walk both arrays backwards from their newest end. Each matching pair
  // is a line on both sides; stop at the first mismatch (older lines in
  // the chunk that the page doesn't have yet).
  let i = chunk.length - 1;
  let j = page.length - 1;
  let matched = 0;
  while (i >= 0 && j >= 0 &&
         stripAnsi(chunk[i]) === stripAnsi(page[j])) {
    matched++;
    i--;
    j--;
  }
  return chunk.slice(0, chunk.length - matched);
}

function stripAnsi(s) {
  return s.replace(/\x1b\[[0-9;]*m/g, '');
}

function prependHistoryLines(lines) {
  const con = document.getElementById('log-console');
  const filterInput = document.getElementById('log-filter');
  const filter = (filterInput && filterInput.value || '').toLowerCase();
  const oldHeight = con.scrollHeight;
  const oldTop    = con.scrollTop;
  const frag = document.createDocumentFragment();
  const newEntries = [];
  for (const raw of lines) {
    const el = document.createElement('div');
    el.className = 'log-line log-history';
    el.innerHTML = renderLogLine(raw);
    if (filter && !raw.toLowerCase().includes(filter)) {
      el.style.display = 'none';
    }
    frag.appendChild(el);
    newEntries.push({el, raw});
  }
  con.insertBefore(frag, con.firstChild);
  // Newest history first in the DOM (since each chunk is older than the previous
  // one), so prepend in array order: oldest of this chunk at index 0.
  historyLines = newEntries.concat(historyLines);
  // Preserve visual position so the user's view doesn't jump after a prepend.
  con.scrollTop = oldTop + (con.scrollHeight - oldHeight);
}

function showHistoryEndMarker() {
  const con = document.getElementById('log-console');
  if (!con || logHistory.endMarker) return;
  const marker = document.createElement('div');
  marker.className = 'log-history-end';
  marker.textContent = '── start of recorded history ──';
  con.insertBefore(marker, con.firstChild);
  logHistory.endMarker = marker;
}

// Convert a log line to HTML. Live SSE lines arrive with ANSI codes from
// the colorizing clog handler; file-sourced scrollback lines do not (the
// rotating file writer strips ANSI so on-disk logs stay grep-friendly).
// Detect which by looking for an ESC byte and dispatch — plainLogToHtml
// mirrors the server's coloring scheme so scrolled-back history keeps
// the same level colors and keyword highlights as the live tail.
function renderLogLine(line) {
  if (line.indexOf('\x1b') !== -1) return ansiToHtml(line);
  return plainLogToHtml(line);
}

// Mirrors clog/handler.go's renderMsg + level color choice for lines
// that come from the on-disk log (no ANSI codes). The format is:
//   "<YYYY-MM-DD HH:MM:SS.mmm> <LEVEL %-5s> <msg><attrs…>"
// Lines that don't match the format render plain so we never corrupt
// non-standard output (e.g. third-party libraries writing to stderr).
const PLAIN_LOG_RE =
  /^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}) (DEBUG|INFO|WARN|ERROR)(\s+)(.*)$/;

const LEVEL_STYLE = {
  DEBUG: { color: '#8b949e' },                 // gray
  INFO:  { color: '#58a6ff' },                 // cyan
  WARN:  { color: '#d29922' },                 // amber
  ERROR: { color: '#f85149', bold: true },     // red+bold
};

// Same keyword highlights clog applies inside the message body.
const MSG_KEYWORDS = [
  { word: 'accepted',  style: { color: '#3fb950', bold: true } },
  { word: 'rejected',  style: { color: '#f85149', bold: true } },
  { word: 'undecided', style: { color: '#d29922' } },
  { word: 'failed',    style: { color: '#bc8cff', bold: true } },
];

function plainLogToHtml(line) {
  const m = PLAIN_LOG_RE.exec(line);
  if (!m) return escHtml(line);
  const [, ts, level, gap, rest] = m;
  const levelStyle = LEVEL_STYLE[level];
  // Split the post-level portion into the bare message and the trailing
  // attrs — clog only colors the message and leaves "key=value" attrs in
  // the terminal's default color. Attrs start at the first " key=" we see
  // where key is a slog-style identifier; if there is no such match, the
  // whole remainder is the message.
  const attrSplit = rest.search(/ [A-Za-z_][\w.]*=/);
  const msg   = attrSplit === -1 ? rest : rest.slice(0, attrSplit);
  const attrs = attrSplit === -1 ? ''   : rest.slice(attrSplit);
  return (
    span({ color: '#8b949e' }, escHtml(ts)) + ' ' +
    span(levelStyle, escHtml(level)) + escHtml(gap) +
    renderPlainMsg(msg, levelStyle) +
    escHtml(attrs)
  );
}

// renderPlainMsg writes msg in the level's base color, swapping to each
// keyword's own color when it appears. Mirrors clog.renderMsg's
// left-to-right keyword scan so the result is byte-equivalent to what an
// ANSI-rendered line would have produced.
function renderPlainMsg(msg, baseStyle) {
  let out = '';
  let pos = 0;
  while (pos < msg.length) {
    let nextAt = -1, nextWord = '', nextStyle = null;
    for (const kw of MSG_KEYWORDS) {
      const at = msg.indexOf(kw.word, pos);
      if (at !== -1 && (nextAt === -1 || at < nextAt)) {
        nextAt = at; nextWord = kw.word; nextStyle = kw.style;
      }
    }
    if (nextAt === -1) {
      out += span(baseStyle, escHtml(msg.slice(pos)));
      break;
    }
    if (nextAt > pos) {
      out += span(baseStyle, escHtml(msg.slice(pos, nextAt)));
    }
    out += span(nextStyle, escHtml(nextWord));
    pos = nextAt + nextWord.length;
  }
  return out;
}

function span(style, content) {
  if (!style || !content) return content;
  const css = [];
  if (style.color) css.push('color:' + style.color);
  if (style.bold)  css.push('font-weight:600');
  return css.length ? `<span style="${css.join(';')}">${content}</span>` : content;
}

function escHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
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
  return String(s ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
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

async function runAll(btn, dryRun) {
  const original = btn.textContent;
  btn.disabled = true;
  btn.classList.add('triggered');
  btn.textContent = dryRun ? 'Dry…' : 'Triggered…';
  try {
    await fetch('/api/tasks/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dry_run: !!dryRun }),
    });
  } catch (_) {}
  setTimeout(() => {
    btn.disabled = false;
    btn.classList.remove('triggered');
    btn.textContent = original;
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
  if (name === 'dashboard') {
    // Re-play the entry animation on existing cards when switching to this tab.
    // Do NOT touch _dashboardFirstRender here — the animation is handled by the
    // reflow trick below and is independent of the polling-refresh suppression flag.
    // Setting _dashboardFirstRender = true here would cause the next polling
    // refresh to also animate (the blink we want to avoid).
    const grid = document.getElementById('task-grid');
    if (grid) {
      grid.classList.remove('no-anim');
      grid.querySelectorAll('.task-card').forEach((el, i) => {
        el.style.animationName = 'none';
        el.offsetHeight; // force reflow so the browser registers the reset
        el.style.animationName = '';
        el.style.animationDelay = `${Math.min(i, 9) * 50}ms`;
      });
    }
  }
  if (name === 'config' && !configLoaded) loadConfig();
  if (name === 'db') {
    if (!dbLoaded) loadDBTab();
    else {
      loadDBSidebar(); // refresh counts whenever the tab is revisited
      if (dbActiveBucket) selectDBBucket(dbActiveBucket); // refresh content too
    }
  }
}
