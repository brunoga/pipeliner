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

let startedAt = Date.now();

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
    filterToken: 0,       // bumps to cancel in-flight fetches after re-filter
    filterDebounce: null,
    bridgeBusy: false,    // true while runBridge() is fetching missed lines
    pendingLiveQueue: [], // live lines buffered behind an in-flight bridge
  };
}

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
        <span><span>Last run${dryBadge}</span><b>${lastStr}${esc(durStr)}</b></span>
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
  const lines = (body && Array.isArray(body.lines)) ? body.lines : [];
  for (const ln of lines) appendRenderedLine(ln.text, ln.pos, false);
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
  if (veLog.bridgeBusy) {
    // A bridge is in flight; queue this line and let runBridge() drain
    // it after the missed window arrives. Out-of-order delivery breaks
    // chronological order, so queueing is required even for non-gap lines.
    veLog.pendingLiveQueue.push(line);
    return;
  }
  if (veLog.lastLivePos && line.pos && hasLogGap(veLog.lastLivePos, line)) {
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
function hasLogGap(lastPosStr, line) {
  const last = parseLinePosClient(lastPosStr);
  const cur = parseLinePosClient(line.pos);
  if (!last || !cur) return false;
  if (last.fileIdx !== cur.fileIdx) return false;
  const predicted = last.byteEnd + (line.text ? line.text.length : 0) + 1;
  return cur.byteEnd > predicted;
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
  // Bump cursors and refetch from tail to re-anchor everything.
  veLog.filterToken++;
  return loadInitialTail();
}

function appendRenderedLine(text, pos, live) {
  const con = document.getElementById('log-console');
  if (!con) return;
  // Drop any "no matches" hint once content arrives.
  removeEmptyMarker();
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

function capWindowFromBottom() {
  if (veLog.rendered.length <= LOG_WINDOW_CAP) return;
  const drop = veLog.rendered.length - LOG_WINDOW_CAP;
  const tail = veLog.rendered.length;
  for (let i = tail - drop; i < tail; i++) {
    veLog.rendered[i].el.remove();
  }
  veLog.rendered = veLog.rendered.slice(0, tail - drop);
  veLog.bottomAtTail = false;
  veLog.bottomCursor = veLog.rendered.length
    ? veLog.rendered[veLog.rendered.length - 1].pos
    : veLog.bottomCursor;
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

// clearLog wipes the rendered window and re-boots from /tail. The filter
// stays as-is (use the X next to the input to clear that).
function clearLog() {
  veLog.filterToken++;
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
  loadInitialTail();
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
