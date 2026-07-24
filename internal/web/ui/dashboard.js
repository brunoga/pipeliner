'use strict';

// ── extra dashboard stylesheet ────────────────────────────────────────────────
//
// index.html is owned by other tooling, so dashboard-specific CSS additions
// live in dashboard-extra.css and are injected here. Guarded so the vitest
// harness (which stubs a minimal document) skips the injection.
(function () {
  if (typeof document === 'undefined' || !document.head || typeof document.head.appendChild !== 'function') return;
  const link = document.createElement('link');
  link.rel = 'stylesheet';
  link.href = 'dashboard-extra.css';
  document.head.appendChild(link);
})();

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

// Fallback uptime base: the page's own age. Replaced by the server's
// started_at from /api/status as soon as the first poll succeeds, so the
// header shows daemon uptime rather than browser-tab uptime.
let startedAt = Date.now();
let serverStartedAt = null; // epoch ms parsed from /api/status started_at

// ── log viewer state ─────────────────────────────────────────────────────────
//
// The log viewer is a sliding window over the on-disk rotating log file.
// rendered[] holds the lines currently in the DOM (oldest-first). topCursor
// is the position of the oldest rendered line — the next /before fetch
// pages from there. bottomCursor is the position of the newest rendered
// non-live line; when bottomAtTail is true, the SSE stream owns the bottom
// edge. Window eviction keeps the DOM bounded but the file is always the
// source of truth — scrolling re-fetches anything we evicted.
const LOG_WINDOW_CAP        = 2000;  // max rendered DOM lines
const LOG_PAGE_SIZE         = 200;   // /tail, /before, /after page size
const LOG_BOOT_TAIL         = 300;   // first /tail call
const LOG_SCROLL_EDGE_PX    = 120;   // re-fetch trigger distance from edge
const LOG_FILTER_DEBOUNCE_MS = 150;

let veLog = newLogState();

function newLogState() {
  return {
    rendered: [],         // [{el, raw, pos, seq, live}], oldest-first
    topCursor: null,      // string cursor; null = haven't loaded any yet
    topExhausted: false,  // start-of-history reached
    bottomCursor: null,   // string cursor of newest non-live rendered line
    bottomAtTail: true,   // false ⇒ /after needed to reach live edge
    loadingTop: false,
    loadingBottom: false,
    liveFollow: true,
    pendingLive: 0,
    filter: '',
    es: null,
    lastSeq: 0,
    lastLivePos: null,    // pos string of the most recent SSE-delivered line
    startMarker: null,
    emptyMarker: null,
    placeholder: null,    // "waiting for log output…" element when console is empty
    filterToken: 0,       // bumps to cancel in-flight fetches after re-filter
    filterDebounce: null,
    bridgeBusy: false,    // true while runBridge() is fetching missed lines
    pendingLiveQueue: [], // live lines buffered behind an in-flight bridge
  };
}

// ── polling ───────────────────────────────────────────────────────────────────

async function refresh() {
  // Don't poll while the tab is hidden — the visibilitychange listener
  // fires a refresh as soon as the tab becomes visible again.
  if (typeof document !== 'undefined' && document.hidden) return;
  try {
    const [sr, hr] = await Promise.all([fetch('/api/status'), fetch('/api/history')]);
    if (!sr.ok || !hr.ok) throw new Error('http ' + sr.status + '/' + hr.status);
    const status  = await sr.json();
    const history = await hr.json();
    const tasks   = status.tasks || [];
    if (status.started_at) {
      const t = Date.parse(status.started_at);
      if (!isNaN(t)) serverStartedAt = t;
    }
    render(tasks, history);
    // Only refresh the DB sidebar when the DB tab is actually visible —
    // rebuilding it in the background steals keyboard focus for nothing.
    if (dbLoaded && isDBTabVisible()) refreshDBSidebarIfChanged();
    const upBase = serverStartedAt !== null ? serverStartedAt : startedAt;
    document.getElementById('header-meta').textContent =
      'up ' + fmtUptime(Math.max(0, Math.round((Date.now() - upBase) / 1000))) +
      ' · ' + tasks.length + ' task' + (tasks.length !== 1 ? 's' : '');
    const allRunning = tasks.length > 0 && tasks.every(t => t.running);
    const runAllBtn = document.getElementById('btn-run-all');
    runAllBtn.disabled = allRunning || _runAllPending;
    runAllBtn.title = allRunning ? 'All tasks are already running' : '';
    const runAllDryBtn = document.getElementById('btn-run-all-dry');
    if (runAllDryBtn) {
      runAllDryBtn.disabled = allRunning || _runAllPending;
      if (allRunning) runAllDryBtn.title = 'All tasks are already running';
      else runAllDryBtn.title = 'Dry-run every task — no side effects, no tracker advance';
    }
  } catch (e) {
    document.getElementById('header-meta').textContent = 'error — retrying';
  }
}

// Refresh immediately when the tab becomes visible again (polling is
// suspended while hidden). Guarded for the vitest document stub.
if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) refresh();
  });
}

function isDBTabVisible() {
  const tab = document.getElementById('tab-db');
  return !!tab && tab.style && tab.style.display !== 'none';
}

// refreshDBSidebarIfChanged re-renders the DB sidebar only when the bucket
// list actually changed since the previous poll. The unconditional rebuild
// used to steal keyboard focus from the filter box every 10 s.
let _dbSidebarLastJSON = null;
async function refreshDBSidebarIfChanged() {
  try {
    const r = await fetch('/api/db/buckets');
    if (!r.ok) return;
    const text = await r.text();
    if (text === _dbSidebarLastJSON) return;
    _dbSidebarLastJSON = text;
    const { buckets } = JSON.parse(text);
    for (const item of dbNavItems) {
      const b = buckets.find(x => x.name === item.bucket);
      item.count = b?.count ?? 0;
    }
    renderDBSidebar();
  } catch (_) { /* transient — next poll retries */ }
}

// True only for the very first render after page load or tab switch.
// Polling refreshes set this to false so cards don't re-animate every 10 s.
let _dashboardFirstRender = true;

// Cached last-rendered data so toggling a card's history panel can re-render
// without waiting for (or issuing) a network round-trip.
let _lastTasks = [];
let _lastHistory = {};

// Task names whose run-history panel is expanded. Module-level so the state
// survives the innerHTML replacement done by every 10 s poll re-render.
const _expandedHistory = new Set();

function toggleTaskHistory(name) {
  if (_expandedHistory.has(name)) _expandedHistory.delete(name);
  else _expandedHistory.add(name);
  if (_lastTasks.length) render(_lastTasks, _lastHistory);
}

function render(tasks, history) {
  _lastTasks = tasks;
  _lastHistory = history || {};
  const grid = document.getElementById('task-grid');
  if (!tasks.length) {
    grid.innerHTML = '<div class="no-tasks">No tasks configured.</div>';
    _dashboardFirstRender = true;
    return;
  }
  // Suppress animation during background refreshes by adding no-anim before
  // replacing innerHTML — new card nodes inherit animation:none from the class.
  if (!_dashboardFirstRender) grid.classList.add('no-anim');
  grid.innerHTML = tasks.map((t, i) => card(t, _lastHistory[t.name] || [], i)).join('');
  _dashboardFirstRender = false;
}

// hasRecentError reports whether any of the newest 5 runs errored — drives
// the subtle indicator on the collapsed card so a failure that happened a
// few runs ago is still discoverable.
function hasRecentError(runs) {
  return (runs || []).slice(0, 5).some(r => r && r.err);
}

// historyHtml renders the expanded run-history panel for one task.
function historyHtml(runs, taskName) {
  const rows = (runs || []).map(r => historyRowHtml(r, taskName)).join('');
  return `<div class="task-history">${rows || '<div class="task-history-empty">No recorded runs.</div>'}</div>`;
}

function historyRowHtml(r, taskName) {
  const d = new Date(r.at);
  const abs = isNaN(d.getTime()) ? '' : d.toLocaleString();
  const when = `<span class="task-history-when" title="${esc(abs)}">${esc(relTime(d))} ago</span>`;
  const dry = r.dry_run ? ' <span class="dry-badge" title="Dry-run — no side effects">DRY</span>' : '';
  const counts =
    `<span class="task-history-counts">` +
    `<span class="thc a" title="accepted">${r.accepted ?? 0}</span>` +
    `<span class="thc r" title="rejected">${r.rejected ?? 0}</span>` +
    `<span class="thc f" title="failed">${r.failed ?? 0}</span>` +
    `<span class="thc u" title="undecided">${r.undecided ?? 0}</span>` +
    `</span>`;
  const err = r.err ? `<div class="task-err">⚠ ${esc(r.err)}</div>` : '';
  // Inspect: only runs that recorded a trace (run_id present) get the link.
  const inspect = r.run_id && taskName
    ? ` <button class="thr-inspect" onclick="event.stopPropagation();toggleTrace(${esc(JSON.stringify(taskName))},${esc(JSON.stringify(r.run_id))},this)">inspect</button>`
    : '';
  return `<div class="task-history-row${r.err ? ' has-err' : ''}">
      <div class="task-history-line">${when}${dry}<span class="task-history-dur">${esc(r.duration || '')}</span>${counts}${inspect}</div>
      ${err}<div class="trace-panel" id="trace-${esc(taskName || '')}-${esc(r.run_id || '')}" hidden></div>
    </div>`;
}

// ── run inspector ─────────────────────────────────────────────────────────────

// traceStateHtml renders one entry's final state as a colored chip.
function traceChip(state) {
  const cls = {accepted: 'a', rejected: 'r', failed: 'f', consumed: 'a', undecided: 'u'}[state] || 'u';
  return `<span class="thc ${cls}">${esc(state)}</span>`;
}

// traceEntryHtml renders one entry row with its per-node steps.
function traceEntryHtml(e) {
  const steps = (e.steps || []).map(st =>
    `<div class="trace-step">${traceChip(st.state)} <code>${esc(st.node)}</code>${st.reason ? ` — ${esc(st.reason)}` : ''}</div>`
  ).join('');
  return `<details class="trace-entry">
      <summary>${traceChip(e.final)} <span class="trace-title">${esc(e.title)}</span>${e.reason ? ` <span class="trace-reason">${esc(e.reason)}</span>` : ''}</summary>
      <div class="trace-steps">${steps || '<div class="trace-step">no recorded steps</div>'}</div>
    </details>`;
}

// toggleTrace loads (once) and shows/hides the trace panel for one run.
async function toggleTrace(task, runId, btn) {
  const panel = document.getElementById(`trace-${task}-${runId}`);
  if (!panel) return;
  if (!panel.hidden) { panel.hidden = true; return; }
  if (!panel.dataset.loaded) {
    panel.innerHTML = '<div class="trace-loading">loading…</div>';
    panel.hidden = false;
    try {
      const r = await fetch(`/api/traces/${encodeURIComponent(task)}/${encodeURIComponent(runId)}`);
      if (!r.ok) { panel.innerHTML = `<div class="trace-loading">trace unavailable (${r.status})</div>`; return; }
      const rt = await r.json();
      const truncated = rt.truncated ? `<div class="trace-loading">…and ${rt.truncated} more entries ran untraced (cap)</div>` : '';
      panel.innerHTML = (rt.entries || []).map(traceEntryHtml).join('') + truncated
        || '<div class="trace-loading">run produced no entries</div>';
      panel.dataset.loaded = '1';
    } catch (e) {
      panel.innerHTML = '<div class="trace-loading">trace fetch failed</div>';
    }
  } else {
    panel.hidden = false;
  }
}

function card(t, runs, idx = 0) {
  runs = runs || [];
  const last = runs[0];
  const expanded = _expandedHistory.has(t.name);
  const nextDate   = t.nextRun ? new Date(t.nextRun) : null;
  const schedLabel = nextDate ? fmtDatetime(nextDate) : (t.schedule ? t.schedule : 'manual');
  const schedOpacity = (!nextDate && !t.schedule) ? ' style="opacity:.5"' : '';
  const schedBadge = `<span class="task-schedule"${schedOpacity} title="${esc(t.schedule || '')}">${esc(schedLabel)}</span>`;

  let nextStr = nextRunLabel(nextDate);
  // Trigger-dependent pipelines show their parent instead of a bogus dash.
  if (nextStr === '—' && t.after) {
    const [parent, cond] = String(t.after).split(':');
    nextStr = `after ${parent}${cond === 'accepted' ? ' (on accepts)' : ''}`;
  }
  // DRY badge lives next to the "Last run" label, not next to the timing
  // text — placing it inside the right-side <b> with "1m ago · 1.5s" made
  // the whole line wrap inside narrow cards, pushing the stats grid down.
  // Anchored to the left label it never widens the right side, so the
  // row stays single-line regardless of card width.
  const dryBadge = (last && last.dry_run) ? ' <span class="dry-badge" title="Last run was a dry-run">DRY</span>' : '';
  const lastStr = last ? relTime(new Date(last.at)) + ' ago' : 'never';
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
         <div class="stat u"><div class="stat-val u">${last.undecided ?? 0}</div><div class="stat-lbl">undecided</div></div>
       </div>`
    : `<div class="task-empty">No runs yet</div>`;

  const errLine = (last && last.err)
    ? `<div class="task-err">⚠ ${esc(last.err)}</div>` : '';

  // Subtle indicator on the collapsed card when a recent (last 5) run
  // errored — a failed run followed by a successful one is otherwise
  // invisible without expanding the history.
  const errDot = (!expanded && hasRecentError(runs))
    ? ` <span class="task-err-dot" title="A recent run failed — click to see run history">●</span>` : '';

  // A trigger in flight (or recently failed) survives poll re-renders:
  // card() consults the map so the button state is re-applied every render.
  const pend = _pendingTriggers.get(t.name);
  const runBtns = t.running
    ? `<button class="btn-run running" disabled>Running…</button>`
    : pend
    ? `<button class="btn-run ${pend.cls}" disabled>${esc(pend.label)}</button>`
    : `<div class="btn-run-group">
         <button class="btn-run" onclick="triggerRun(${esc(JSON.stringify(t.name))}, this, false)" title="Run with side effects and tracker commits">Run now</button>
         <button class="btn-run btn-dry" onclick="triggerRun(${esc(JSON.stringify(t.name))}, this, true)" title="Dry run — no side effects, no tracker advance">Dry</button>
       </div>`;

  const historyPanel = expanded ? historyHtml(runs, t.name) : '';
  const chevron = `<span class="task-history-chevron">${expanded ? '▾' : '▸'}</span>`;

  // nth-child handles first 10 cards; inline delay covers beyond that.
  const extraDelay = idx >= 10 ? `;animation-delay:${idx * 50}ms` : '';

  return `
    <div class="task-card" style="--card-color:${cardColor}${extraDelay}">
      <div class="task-card-header task-history-toggle" onclick="toggleTaskHistory(${esc(JSON.stringify(t.name))})" title="Click to ${expanded ? 'hide' : 'show'} recent runs">
        <div class="task-name">${esc(t.name)}${errDot}</div>
        ${schedBadge}${chevron}
      </div>
      <div class="task-timing">
        <span><span>Next run</span><b>${nextStr}</b></span>
        <span><span>Last run${dryBadge}</span><b>${lastStr}${esc(durStr)}</b></span>
      </div>
      ${stats}
      ${errLine}
      ${historyPanel}
      ${runBtns}
    </div>`;
}

// ── manual trigger ────────────────────────────────────────────────────────────

// name → {label, cls} for manual triggers that are in flight or recently
// failed. Rendered by card() so the 10 s poll can't wipe the feedback, and
// consulted by triggerRun to prevent double-triggers.
const _pendingTriggers = new Map();

async function triggerRun(name, btn, dryRun) {
  if (_pendingTriggers.has(name)) return; // request already in flight
  const original = btn.textContent;
  const pendingLabel = dryRun ? 'Dry…' : 'Triggered…';
  _pendingTriggers.set(name, {label: pendingLabel, cls: 'triggered'});
  btn.disabled = true;
  btn.textContent = pendingLabel;
  btn.classList.add('triggered');
  // Disable the sibling button too so the user can't fire both back-to-back
  // (the daemon would just drop the second, but it's confusing visually).
  const siblings = btn.parentElement ? btn.parentElement.querySelectorAll('button') : [];
  siblings.forEach(s => { if (s !== btn) s.disabled = true; });
  let failDetail = null;
  try {
    const r = await fetch('/api/tasks/' + encodeURIComponent(name) + '/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dry_run: !!dryRun }),
    });
    if (!r.ok) {
      let detail = '';
      try { detail = await r.text(); } catch (_) {}
      failDetail = 'HTTP ' + r.status + (detail ? ' — ' + detail.trim() : '');
    }
  } catch (e) {
    failDetail = String(e);
  }
  if (failDetail) {
    console.warn('trigger for task "' + name + '" failed: ' + failDetail);
    _pendingTriggers.set(name, {label: 'Failed — see log', cls: 'trigger-failed'});
    btn.textContent = 'Failed — see log';
    btn.classList.remove('triggered');
    btn.classList.add('trigger-failed');
  }
  setTimeout(() => {
    _pendingTriggers.delete(name);
    btn.disabled = false;
    btn.textContent = original;
    btn.classList.remove('triggered', 'trigger-failed');
    siblings.forEach(s => { s.disabled = false; });
    refresh();
  }, failDetail ? 4000 : 3000);
}

// ── SSE log stream ────────────────────────────────────────────────────────────

function connectLogs() {
  const dot  = document.getElementById('log-dot');
  const text = document.getElementById('log-status-text');
  // Boot path: load initial tail before the SSE stream begins so we have
  // cursors anchored to the on-disk file. SSE then takes over the live edge.
  if (!veLog.bootStarted) {
    veLog.bootStarted = true;
    loadInitialTail();
  }
  startSSE(dot, text);

  const con = document.getElementById('log-console');
  if (con && !con._scrollWired) {
    con._scrollWired = true;
    con.addEventListener('scroll', onLogScroll);
  }
}

function startSSE(dot, text) {
  const url = veLog.filter ? `/api/logs?q=${encodeURIComponent(veLog.filter)}` : '/api/logs';
  const es = new EventSource(url);
  veLog.es = es;

  es.onopen = () => { dot.classList.add('on'); text.textContent = 'connected'; };

  es.onmessage = ev => {
    let payload;
    try { payload = JSON.parse(ev.data); } catch (_) { return; }
    if (!payload || typeof payload.text !== 'string') return;
    const seq = parseInt(ev.lastEventId || '0', 10) || 0;
    handleSSELine({pos: payload.pos || '', text: payload.text, seq});
  };

  es.addEventListener('rotate', () => {
    // Rotation invalidates client-held cursors. Refresh from /tail.
    handleRotation();
  });

  es.onerror = () => {
    dot.classList.remove('on');
    text.textContent = 'reconnecting…';
    es.close();
    veLog.es = null;
    setTimeout(() => startSSE(dot, text), 3000);
  };
}

async function loadInitialTail() {
  setFilterSpinner(true);
  const token = ++veLog.filterToken;
  try {
    const url = buildLogUrl('/api/logs/tail', {limit: LOG_BOOT_TAIL});
    const body = await fetchJSON(url);
    if (token !== veLog.filterToken) return;
    renderInitialTail(body);
  } catch (_) {
  } finally {
    if (token === veLog.filterToken) setFilterSpinner(false);
  }
}

function renderInitialTail(body) {
  const con = document.getElementById('log-console');
  if (!con) return;
  con.innerHTML = '';
  veLog.rendered = [];
  veLog.startMarker = null;
  veLog.emptyMarker = null;
  veLog.placeholder = null;
  const lines = (body && Array.isArray(body.lines)) ? body.lines : [];
  for (const ln of lines) appendRenderedLine(ln.text, ln.pos, false);
  // An empty console is a big unlabeled black void — label it. The
  // placeholder is removed as soon as the first line arrives.
  if (lines.length === 0 && !veLog.filter) {
    const ph = document.createElement('div');
    ph.className = 'log-placeholder';
    ph.textContent = 'waiting for log output…';
    con.appendChild(ph);
    veLog.placeholder = ph;
  }
  veLog.topCursor = body.older_cursor || (lines[0] ? lines[0].pos : null);
  veLog.topExhausted = !!body.exhausted;
  veLog.bottomCursor = lines.length ? lines[lines.length - 1].pos : null;
  veLog.bottomAtTail = true;
  veLog.liveFollow = true;
  veLog.pendingLive = 0;
  veLog.lastLivePos = veLog.bottomCursor;
  updatePill();
  refreshMarkers();
  // Pin to bottom on initial boot.
  requestAnimationFrame(() => { con.scrollTop = con.scrollHeight; });
}

function handleSSELine(line) {
  veLog.lastSeq = line.seq || veLog.lastSeq;
  // The server already filters SSE lines server-side when ?q= is set, so
  // every event we receive matches the current filter.
  //
  // The broadcaster replays its in-memory ring (up to 256 events) to every
  // new subscriber, including the very first connect. Those events are
  // typically older than the most recent line returned by /tail, so without
  // this guard they get appended at the bottom of the view as if newer than
  // what /tail produced — which puts the live tail out of chronological
  // order. Skipping covered positions is also what we want on reconnect:
  // anything Last-Event-ID resurfaced and we already had gets ignored.
  if (positionCovered(veLog.lastLivePos, line.pos)) {
    return;
  }
  if (veLog.bridgeBusy) {
    // A bridge is in flight; queue this line and let runBridge() drain
    // it after the missed window arrives. Out-of-order delivery breaks
    // chronological order, so queueing is required even for non-gap lines.
    veLog.pendingLiveQueue.push(line);
    return;
  }
  // Byte-position gap detection only makes sense when the SSE stream
  // delivers every line in the file. Under a server-side filter the
  // stream skips non-matching lines, so two consecutive matches have
  // non-adjacent positions by design — running the bridge per event
  // would issue an /api/logs/after fetch for every live match and
  // produce the visible slowdowns the user reported. Drops under a
  // filter are unrecoverable from byte positions alone; accept that.
  if (!veLog.filter && veLog.lastLivePos && line.pos && hasLogGap(veLog.lastLivePos, line)) {
    veLog.pendingLiveQueue.push(line);
    runBridge();
    return;
  }
  applyLiveLine(line);
}

function applyLiveLine(line) {
  if (!veLog.bottomAtTail) {
    // We're paging forward from history; new live lines aren't part of
    // the rendered window yet. They'll come in when /after returns
    // at_tail=true or the user clicks the pill.
    veLog.pendingLive++;
    updatePill();
    return;
  }
  appendRenderedLine(line.text, line.pos, true);
  veLog.bottomCursor = line.pos || veLog.bottomCursor;
  veLog.lastLivePos = line.pos || veLog.lastLivePos;
  if (veLog.liveFollow) {
    scrollLogToBottom();
    capWindowFromTop();
  } else {
    veLog.pendingLive++;
    updatePill();
  }
}

// runBridge fills the gap between the client's last-seen live position
// and a freshly-arrived SSE line that's NOT contiguous with it. Such a
// gap appears when an SSE reconnect spans more lines than the server's
// in-memory ring can replay. The bridge fetches /api/logs/after pages
// until the queued line is either covered or accepted.
async function runBridge() {
  if (veLog.bridgeBusy) return;
  veLog.bridgeBusy = true;
  try {
    // Hard guard against unbounded loops on pathological positions; in
    // practice each /after page advances lastLivePos forward.
    let guard = 50;
    while (veLog.pendingLiveQueue.length > 0 && guard-- > 0) {
      const next = veLog.pendingLiveQueue[0];
      // Already covered by a previous bridge result.
      if (positionCovered(veLog.lastLivePos, next.pos)) {
        veLog.pendingLiveQueue.shift();
        continue;
      }
      if (!hasLogGap(veLog.lastLivePos, next)) {
        veLog.pendingLiveQueue.shift();
        applyLiveLine(next);
        continue;
      }
      // Genuine gap — fetch the missed window.
      const body = await fetchJSON(buildLogUrl('/api/logs/after', {
        cursor: veLog.lastLivePos,
        limit: LOG_PAGE_SIZE,
      }));
      const lines = (body && Array.isArray(body.lines)) ? body.lines : [];
      let appliedCount = 0;
      for (const ln of lines) {
        if (positionCovered(veLog.lastLivePos, ln.pos)) continue;
        applyLiveLine({pos: ln.pos, text: ln.text, seq: 0});
        appliedCount++;
      }
      if (appliedCount === 0) {
        // Server returned no new content beyond what we already have.
        // Don't loop — accept the queued line so it doesn't get stuck.
        veLog.pendingLiveQueue.shift();
        applyLiveLine(next);
      }
    }
  } catch (_) {
    // On fetch failure, drop the queue rather than block live delivery.
    // The on-disk log still has the missed lines — the user can scroll
    // up to find them.
  } finally {
    veLog.bridgeBusy = false;
  }
}

// hasLogGap returns true when `line` is not contiguous with the previous
// live position — i.e., its starting byte (position - len(text) - 1) is
// past the previous line's byte end. Cross-file transitions don't qualify;
// the SSE rotate event handles those separately.
//
// Lengths must be compared in BYTES, not JS string units. The server's
// byteEnd is a UTF-8 byte offset, but `String.length` counts UTF-16 code
// units, so any line with non-ASCII characters (e.g. "Dünya") would
// otherwise look like a gap and trigger an unneeded bridge fetch.
function hasLogGap(lastPosStr, line) {
  const last = parseLinePosClient(lastPosStr);
  const cur = parseLinePosClient(line.pos);
  if (!last || !cur) return false;
  if (last.fileIdx !== cur.fileIdx) return false;
  const predicted = last.byteEnd + (line.text ? utf8ByteLength(line.text) : 0) + 1;
  return cur.byteEnd > predicted;
}

// utf8ByteLength returns the UTF-8 byte length of s, matching what the
// rotating log writer reports as byteEnd on the server side. TextEncoder
// is available in every browser the dashboard supports; in the vitest
// test environment a global is provided by Node's `util` module.
const _logTextEncoder = typeof TextEncoder !== 'undefined' ? new TextEncoder() : null;
function utf8ByteLength(s) {
  if (_logTextEncoder) return _logTextEncoder.encode(s).length;
  // Fallback: walk code points and sum their UTF-8 byte widths. Only used
  // in environments where TextEncoder is unavailable.
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xD800 && c <= 0xDBFF) { n += 4; i++; } // surrogate pair
    else n += 3;
  }
  return n;
}

function positionCovered(lastPosStr, posStr) {
  const last = parseLinePosClient(lastPosStr);
  const cur = parseLinePosClient(posStr);
  if (!last || !cur) return false;
  return last.fileIdx === cur.fileIdx && cur.byteEnd <= last.byteEnd;
}

function parseLinePosClient(s) {
  if (!s || typeof s !== 'string') return null;
  const i = s.indexOf(':');
  if (i < 0) return null;
  const f = parseInt(s.slice(0, i), 10);
  const b = parseInt(s.slice(i + 1), 10);
  if (isNaN(f) || isNaN(b)) return null;
  return {fileIdx: f, byteEnd: b};
}

function handleRotation() {
  // After rotation the base file's byte space restarts at 0, so every
  // client-held cursor (lastLivePos, topCursor, bottomCursor) refers
  // to a position in the now-archived file even though its fileIdx is
  // still 0. If we leave them in place, the positionCovered guard in
  // handleSSELine will compare brand-new (small) post-rotation byteEnds
  // against the (large) pre-rotation byteEnd and silently drop every
  // event arriving before loadInitialTail returns. Clear the cursors
  // so the guard treats the post-rotation stream as fresh.
  veLog.filterToken++;
  veLog.lastLivePos = null;
  veLog.topCursor = null;
  veLog.bottomCursor = null;
  return loadInitialTail();
}

function appendRenderedLine(text, pos, live) {
  const con = document.getElementById('log-console');
  if (!con) return;
  // Drop any "no matches" / "waiting…" hints once content arrives.
  removeEmptyMarker();
  removeLogPlaceholder();
  const el = document.createElement('div');
  el.className = 'log-line';
  el.innerHTML = renderLogLine(text);
  con.appendChild(el);
  veLog.rendered.push({el, raw: text, pos: pos || null, live: !!live});
}

function prependRenderedLines(items) {
  const con = document.getElementById('log-console');
  if (!con) return;
  removeEmptyMarker();
  removeLogPlaceholder();
  const oldHeight = con.scrollHeight;
  const oldTop = con.scrollTop;
  const frag = document.createDocumentFragment();
  const newEntries = [];
  for (const {pos, text} of items) {
    const el = document.createElement('div');
    el.className = 'log-line';
    el.innerHTML = renderLogLine(text);
    frag.appendChild(el);
    newEntries.push({el, raw: text, pos: pos || null, live: false});
  }
  // Insert after the start-of-history marker if it's present at the top.
  const anchor = veLog.startMarker || con.firstChild;
  if (anchor) con.insertBefore(frag, anchor);
  else con.appendChild(frag);
  veLog.rendered = newEntries.concat(veLog.rendered);
  con.scrollTop = oldTop + (con.scrollHeight - oldHeight);
}

function scrollLogToBottom() {
  const con = document.getElementById('log-console');
  if (con) con.scrollTop = con.scrollHeight;
}

// capWindowFromTop evicts oldest rendered rows when the window grows
// past LOG_WINDOW_CAP. The evicted lines are still on disk; the next
// scroll-up will re-fetch them. topCursor is advanced to the new oldest
// rendered line so /before paginates correctly. We never evict while the
// user has scrolled away — that would lose context they're reading.
function capWindowFromTop() {
  if (veLog.rendered.length <= LOG_WINDOW_CAP) return;
  const drop = veLog.rendered.length - LOG_WINDOW_CAP;
  for (let i = 0; i < drop; i++) {
    veLog.rendered[i].el.remove();
  }
  veLog.rendered = veLog.rendered.slice(drop);
  // We've thrown lines away — they're now older than what we have.
  veLog.topExhausted = false;
  veLog.topCursor = veLog.rendered.length ? veLog.rendered[0].pos : veLog.topCursor;
  if (veLog.startMarker) {
    veLog.startMarker.remove();
    veLog.startMarker = null;
  }
}

// ── scroll-driven paging ─────────────────────────────────────────────────────

function onLogScroll() {
  const con = document.getElementById('log-console');
  if (!con) return;
  const distFromBottom = con.scrollHeight - con.scrollTop - con.clientHeight;
  // Determine live-follow state based on the user's viewport, not on past
  // assumptions: at the bottom means following live.
  const wasFollowing = veLog.liveFollow;
  veLog.liveFollow = distFromBottom < 4;
  if (!wasFollowing && veLog.liveFollow) {
    // User scrolled back to bottom → drain pending pill counter.
    veLog.pendingLive = 0;
    updatePill();
  } else if (wasFollowing && !veLog.liveFollow) {
    updatePill();
  }

  if (con.scrollTop < LOG_SCROLL_EDGE_PX) {
    maybeLoadOlder();
  }
  if (distFromBottom < LOG_SCROLL_EDGE_PX && !veLog.bottomAtTail) {
    maybeLoadNewer();
  }
}

async function maybeLoadOlder() {
  if (veLog.loadingTop || veLog.topExhausted || !veLog.topCursor) return;
  veLog.loadingTop = true;
  if (veLog.filter) setFilterSpinner(true);
  const token = veLog.filterToken;
  try {
    const url = buildLogUrl('/api/logs/before', {
      cursor: veLog.topCursor,
      limit: LOG_PAGE_SIZE,
    });
    const body = await fetchJSON(url);
    if (token !== veLog.filterToken) return;
    const lines = (body && Array.isArray(body.lines)) ? body.lines : [];
    if (lines.length > 0) {
      prependRenderedLines(lines.map(l => ({pos: l.pos, text: l.text})));
      veLog.topCursor = body.older_cursor || lines[0].pos;
    } else if (body && body.older_cursor) {
      // No matches in this window but there's older content — advance the
      // cursor so the next scroll pages further.
      veLog.topCursor = body.older_cursor;
    }
    if (body && body.exhausted) {
      veLog.topExhausted = true;
    }
    refreshMarkers();
  } catch (_) {
  } finally {
    veLog.loadingTop = false;
    if (veLog.filter) setFilterSpinner(false);
  }
}

async function maybeLoadNewer() {
  if (veLog.loadingBottom || veLog.bottomAtTail || !veLog.bottomCursor) return;
  veLog.loadingBottom = true;
  if (veLog.filter) setFilterSpinner(true);
  const token = veLog.filterToken;
  try {
    const url = buildLogUrl('/api/logs/after', {
      cursor: veLog.bottomCursor,
      limit: LOG_PAGE_SIZE,
    });
    const body = await fetchJSON(url);
    if (token !== veLog.filterToken) return;
    const lines = (body && Array.isArray(body.lines)) ? body.lines : [];
    for (const ln of lines) appendRenderedLine(ln.text, ln.pos, false);
    if (lines.length > 0) {
      veLog.bottomCursor = body.newer_cursor || lines[lines.length - 1].pos;
    } else if (body && body.newer_cursor) {
      veLog.bottomCursor = body.newer_cursor;
    }
    if (body && body.at_tail) {
      veLog.bottomAtTail = true;
      veLog.bottomCursor = veLog.lastLivePos || veLog.bottomCursor;
    }
  } catch (_) {
  } finally {
    veLog.loadingBottom = false;
    if (veLog.filter) setFilterSpinner(false);
  }
}

// resumeLiveTail jumps to the live edge: fetch forward until at_tail,
// then scroll to bottom and re-engage live-follow.
async function resumeLiveTail() {
  while (!veLog.bottomAtTail && veLog.bottomCursor) {
    await maybeLoadNewer();
  }
  veLog.pendingLive = 0;
  veLog.liveFollow = true;
  updatePill();
  scrollLogToBottom();
}

// ── filter ──────────────────────────────────────────────────────────────────

function onLogFilterInput() {
  if (veLog.filterDebounce) clearTimeout(veLog.filterDebounce);
  veLog.filterDebounce = setTimeout(() => {
    veLog.filterDebounce = null;
    applyFilter();
  }, LOG_FILTER_DEBOUNCE_MS);
}

async function applyFilter() {
  const input = document.getElementById('log-filter');
  const newFilter = input ? input.value : '';
  if (newFilter === veLog.filter) return;
  veLog.filter = newFilter;
  veLog.filterToken++;  // cancel pending fetches under the old filter
  if (veLog.es) {
    veLog.es.close();
    veLog.es = null;
  }
  veLog.rendered = [];
  veLog.topCursor = null;
  veLog.topExhausted = false;
  veLog.bottomCursor = null;
  veLog.bottomAtTail = true;
  veLog.liveFollow = true;
  veLog.pendingLive = 0;
  veLog.lastLivePos = null;
  veLog.startMarker = null;
  veLog.emptyMarker = null;
  const con = document.getElementById('log-console');
  if (con) con.innerHTML = '';
  updatePill();
  await loadInitialTail();
  // Re-open SSE with the new filter (filter-aware server-side).
  const dot  = document.getElementById('log-dot');
  const text = document.getElementById('log-status-text');
  startSSE(dot, text);
}

// clearLog wipes the rendered window client-side only. The server log is
// untouched: no /tail re-boot happens, a "── cleared ──" marker takes the
// place of the removed lines, and new live SSE lines resume below it.
// Cursors are preserved so scrolling up still pages history from disk.
function clearLog() {
  veLog.rendered = [];
  veLog.pendingLive = 0;
  veLog.liveFollow = true;
  veLog.startMarker = null;
  veLog.emptyMarker = null;
  veLog.placeholder = null;
  const con = document.getElementById('log-console');
  if (con) {
    con.innerHTML = '';
    const m = document.createElement('div');
    m.className = 'log-history-end log-cleared';
    m.textContent = '── cleared ──';
    con.appendChild(m);
  }
  updatePill();
}

// ── UI affordances ───────────────────────────────────────────────────────────

function updatePill() {
  const pill = document.getElementById('log-tail-pill');
  const count = document.getElementById('log-tail-pill-count');
  if (!pill || !count) return;
  const show = !veLog.liveFollow && veLog.pendingLive > 0;
  pill.hidden = !show;
  if (show) count.textContent = String(veLog.pendingLive);
}

function setFilterSpinner(on) {
  const sp = document.getElementById('log-filter-spinner');
  if (!sp) return;
  sp.classList.toggle('on', !!on);
}

function refreshMarkers() {
  const con = document.getElementById('log-console');
  if (!con) return;
  if (veLog.topExhausted && veLog.rendered.length > 0) {
    if (!veLog.startMarker) {
      const m = document.createElement('div');
      m.className = 'log-history-end';
      m.textContent = '── start of recorded history ──';
      con.insertBefore(m, con.firstChild);
      veLog.startMarker = m;
    }
  } else if (veLog.startMarker) {
    veLog.startMarker.remove();
    veLog.startMarker = null;
  }
  if (veLog.rendered.length === 0 && veLog.filter && veLog.topExhausted) {
    if (!veLog.emptyMarker) {
      const m = document.createElement('div');
      m.className = 'log-no-matches';
      m.textContent = `no matches for "${veLog.filter}" in recorded history`;
      con.appendChild(m);
      veLog.emptyMarker = m;
    }
  } else {
    removeEmptyMarker();
  }
}

function removeEmptyMarker() {
  if (veLog.emptyMarker) {
    veLog.emptyMarker.remove();
    veLog.emptyMarker = null;
  }
}

// removeLogPlaceholder drops the "waiting for log output…" hint. Called
// when actual content arrives; kept separate from removeEmptyMarker so
// refreshMarkers (which runs right after the placeholder is added) does
// not immediately delete it.
function removeLogPlaceholder() {
  if (veLog.placeholder) {
    veLog.placeholder.remove();
    veLog.placeholder = null;
  }
}

// ── helpers ─────────────────────────────────────────────────────────────────

function buildLogUrl(base, params) {
  const usp = new URLSearchParams();
  for (const k of Object.keys(params)) {
    if (params[k] != null && params[k] !== '') usp.set(k, String(params[k]));
  }
  if (veLog.filter) usp.set('q', veLog.filter);
  const qs = usp.toString();
  return qs ? `${base}?${qs}` : base;
}

async function fetchJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error('http ' + r.status);
  return await r.json();
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

function relTime(d, now = Date.now()) {
  const s = Math.round(Math.abs(now - d) / 1000);
  if (s < 90)    return s + 's';
  if (s < 5400)  return Math.round(s / 60) + 'm';
  if (s < 86400) return Math.round(s / 3600) + 'h';
  return Math.round(s / 86400) + 'd';
}

// nextRunLabel formats the "Next run" cell. A next-run timestamp in the
// past reads "overdue" — relTime's Math.abs would otherwise render it as
// a bogus future time ("in 30s" for a run 30s late).
function nextRunLabel(nextDate, now = Date.now()) {
  if (!nextDate) return '—';
  if (nextDate.getTime() < now) return 'overdue';
  return 'in ' + relTime(nextDate, now);
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

// True while a run-all trigger is in flight or showing feedback, so the
// 10 s poll doesn't re-enable the button mid-window.
let _runAllPending = false;

async function runAll(btn, dryRun) {
  if (_runAllPending) return;
  _runAllPending = true;
  const original = btn.textContent;
  btn.disabled = true;
  btn.classList.add('triggered');
  btn.textContent = dryRun ? 'Dry…' : 'Triggered…';
  let failDetail = null;
  try {
    const r = await fetch('/api/tasks/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dry_run: !!dryRun }),
    });
    if (!r.ok) {
      let detail = '';
      try { detail = await r.text(); } catch (_) {}
      failDetail = 'HTTP ' + r.status + (detail ? ' — ' + detail.trim() : '');
    }
  } catch (e) {
    failDetail = String(e);
  }
  if (failDetail) {
    console.warn('run-all trigger failed: ' + failDetail);
    btn.textContent = 'Failed — see log';
    btn.classList.remove('triggered');
    btn.classList.add('trigger-failed');
  }
  setTimeout(() => {
    _runAllPending = false;
    btn.disabled = false;
    btn.classList.remove('triggered', 'trigger-failed');
    btn.textContent = original;
    refresh();
  }, failDetail ? 4000 : 3000);
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
  if (name === 'settings' && typeof loadPluginDebugSettings === 'function') {
    loadPluginDebugSettings();
  }
}
