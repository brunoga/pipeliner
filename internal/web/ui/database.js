// ── database tab ─────────────────────────────────────────────────────────────

function toTitleCase(s) {
  return s.replace(/\b\w/g, c => c.toUpperCase());
}

let dbLoaded = false;
let dbActiveBucket = null;
let dbNavItems = []; // [{bucket, label, count, section}]  section ∈ 'trackers' | 'caches'
let dbPageSize = 20;
let dbFilterQuery = '';
let dbCursorStack = []; // stack of 'after' values; empty entry = first page
let dbCurrentCursor = ''; // 'after' param for the current page

// Fixed buckets always shown in the sidebar (even if empty).
const DB_FIXED_BUCKETS = [
  {bucket: 'series', label: 'Series'},
  {bucket: 'movies', label: 'Movies'},
  {bucket: 'seen',   label: 'Seen'},
];

async function loadDBTab() {
  const r = await fetch('/api/db/buckets');
  if (!r.ok) { document.getElementById('db-sidebar').innerHTML = '<div class="db-empty">Error.</div>'; return; }
  const { buckets } = await r.json();
  dbNavItems = [];
  // Fixed trackers first — always present, count from bucket list.
  for (const {bucket, label} of DB_FIXED_BUCKETS) {
    const b = buckets.find(b => b.name === bucket);
    dbNavItems.push({bucket, label, count: b?.count ?? 0, section: 'trackers'});
  }
  // Any per-task local seen buckets (seen:task-name).
  for (const b of buckets) {
    if (b.name.startsWith('seen:')) {
      dbNavItems.push({bucket: b.name, label: 'Seen: ' + b.name.slice(5), count: b.count, section: 'trackers'});
    }
  }
  // Caches: every bucket the server classified as a cache. Display label and
  // category come straight from the API, so adding a new cache_* bucket in Go
  // surfaces here without a JS change.
  const caches = buckets.filter(b => b.category === 'cache').sort((a, b) => a.display.localeCompare(b.display));
  for (const b of caches) {
    dbNavItems.push({bucket: b.name, label: b.display, count: b.count, section: 'caches'});
  }
  dbLoaded = true;
  renderDBSidebar();
  if (dbNavItems.length && !dbActiveBucket) selectDBBucket(dbNavItems[0].bucket);
}

function renderDBSidebar() {
  const trackers = dbNavItems.filter(i => i.section === 'trackers');
  const caches = dbNavItems.filter(i => i.section === 'caches');
  const renderSection = (items, title) => {
    if (!items.length) return '';
    let html = `<div class="db-sidebar-section">${title}</div>`;
    for (const item of items) {
      const active = dbActiveBucket === item.bucket ? ' active' : '';
      html += `<button class="db-nav-btn${active}" onclick="selectDBBucket(${esc(JSON.stringify(item.bucket))})">
        <span>${esc(item.label)}</span>
        <span class="db-nav-count">${item.count}</span>
      </button>`;
    }
    return html;
  };
  document.getElementById('db-sidebar').innerHTML =
    renderSection(trackers, 'Trackers') + renderSection(caches, 'Caches');
}

function dbPageURL(name) {
  const p = new URLSearchParams({limit: dbPageSize});
  if (dbCurrentCursor) p.set('after', dbCurrentCursor);
  if (dbFilterQuery)   p.set('q', dbFilterQuery);
  return '/api/db/buckets/' + encodeURIComponent(name) + '?' + p;
}

async function selectDBBucket(name) {
  dbActiveBucket = name;
  dbCurrentCursor = '';
  dbCursorStack = [];
  dbFilterQuery = '';
  renderDBSidebar();
  await fetchDBPage(name);
}

let _dbAbortController = null;

async function fetchDBPage(name) {
  const main = document.getElementById('db-main-content');
  // When the user is refreshing the same bucket (e.g. typed into the filter
  // box) keep the toolbar alive so the filter input doesn't lose focus
  // mid-keystroke; only swap the scroll/content area for the loading
  // indicator. On a fresh bucket (or first load) rebuild the whole panel.
  if (dbMainHasToolbarFor(main, name)) {
    const scrollHost = main.querySelector('.db-scroll');
    if (scrollHost) scrollHost.innerHTML = '<div class="db-loading">Loading…</div>';
  } else {
    main.innerHTML = '<div class="db-loading">Loading…</div>';
  }
  if (_dbAbortController) _dbAbortController.abort();
  _dbAbortController = new AbortController();
  try {
    const r = await fetch(dbPageURL(name), {signal: _dbAbortController.signal});
    if (!r.ok) { main.innerHTML = '<div class="db-empty">Error loading data.</div>'; return; }
    const data = await r.json();
    renderDBContent(name, data);
  } catch (e) {
    if (e.name !== 'AbortError') main.innerHTML = '<div class="db-empty">Error loading data.</div>';
  }
}

// dbMainHasToolbarFor reports whether main already contains a toolbar wired
// up for `name` — used to decide between a surgical refresh (keep the filter
// input alive) and a full panel rebuild (switching buckets, first load).
function dbMainHasToolbarFor(main, name) {
  const tb = main && main.querySelector ? main.querySelector('.db-toolbar') : null;
  return !!(tb && tb.dataset && tb.dataset.bucket === name);
}

async function dbNextPage() {
  if (!dbActiveBucket) return;
  // Save current cursor so Prev can come back here.
  dbCursorStack.push(dbCurrentCursor);
  // Advance cursor to the last key on the current page (returned as next_cursor).
  dbCurrentCursor = document.getElementById('db-next-cursor')?.value || '';
  await fetchDBPage(dbActiveBucket);
}

async function dbPrevPage() {
  if (!dbActiveBucket || !dbCursorStack.length) return;
  dbCurrentCursor = dbCursorStack.pop();
  await fetchDBPage(dbActiveBucket);
}

async function dbSetPageSize(n) {
  dbPageSize = n;
  dbCurrentCursor = '';
  dbCursorStack = [];
  if (dbActiveBucket) await fetchDBPage(dbActiveBucket);
}

let _dbFilterTimer = null;

function dbFilter(val) {
  dbFilterQuery = (val || '').toLowerCase().trim();
  dbCurrentCursor = '';
  dbCursorStack = [];
  clearTimeout(_dbFilterTimer);
  _dbFilterTimer = setTimeout(() => { if (dbActiveBucket) fetchDBPage(dbActiveBucket); }, 300);
}

function renderDBContent(name, data) {
  const item = dbNavItems.find(i => i.bucket === name) || {label: name};
  const main = document.getElementById('db-main-content');

  const hasPrev = dbCursorStack.length > 0;
  const hasNext = data.has_more;
  const total   = data.total ?? 0;
  const sizes   = [10, 20, 50, 100];
  const pager = `<div class="db-pager">
    <button class="btn-sm" onclick="dbPrevPage()" ${hasPrev ? '' : 'disabled'}>← Prev</button>
    <span class="db-pager-info">${total} total</span>
    <button class="btn-sm" onclick="dbNextPage()" ${hasNext ? '' : 'disabled'}>Next →</button>
    <label class="db-pager-size">Per page
      <select onchange="dbSetPageSize(+this.value)">${
        sizes.map(n => `<option value="${n}"${n === dbPageSize ? ' selected' : ''}>${n}</option>`).join('')
      }</select>
    </label>
    <input type="hidden" id="db-next-cursor" value="${esc(data.next_cursor || '')}">
  </div>`;

  let content = '';
  if (name === 'series') content = renderSeriesTable(data.grouped || [], name);
  else if (name === 'movies') content = renderMoviesTable(data.entries || [], name);
  else if (item.section === 'caches') content = renderCacheTable(data.entries || [], name);
  else content = renderSeenTable(data.entries || [], name);
  const scroll = `<div class="db-scroll">${content}</div>`;

  // Fast path: same bucket as last render. The toolbar (filter input + Clear
  // All) is already in the DOM and the user may be actively typing into the
  // input — replacing it would steal focus mid-keystroke. Swap only the pager
  // and the scroll region. The toolbar's filter input is left alone; its
  // value is the user's typing and never needs server-side re-rendering.
  if (dbMainHasToolbarFor(main, name)) {
    const pagerEl  = main.querySelector('.db-pager');
    const scrollEl = main.querySelector('.db-scroll');
    if (pagerEl && scrollEl) {
      pagerEl.outerHTML  = pager;
      // Re-query: outerHTML on pagerEl detaches the old element; scrollEl is
      // unaffected because it's a sibling, but be safe and re-find it.
      const scrollEl2 = main.querySelector('.db-scroll');
      if (scrollEl2) scrollEl2.outerHTML = scroll;
      return;
    }
  }

  // First render for this bucket (or main was wiped by an error path).
  // data-bucket lets the fast path above know which bucket the toolbar is for.
  const toolbar = `<div class="db-toolbar" data-bucket="${esc(name)}">
    <span class="db-title">${esc(item.label)}</span>
    <div style="display:flex;gap:8px;align-items:center">
      <input type="text" class="db-search" id="db-filter-input" placeholder="filter…"
        value="${esc(dbFilterQuery)}" oninput="dbFilter(this.value)">
      <button class="btn-danger" onclick="dbClearBucket(${esc(JSON.stringify(name))},${esc(JSON.stringify(item.label))})">Clear all</button>
    </div>
  </div>`;
  main.innerHTML = toolbar + pager + scroll;
}

// ── series ─────────────────────────────────────────────────────────────────────

function renderSeriesTable(shows, bucket) {
  if (!shows.length) return '<div class="db-empty">No tracked series.</div>';
  let html = `<table class="db-table" id="db-content-table">
    <thead><tr><th colspan="2">Show / Episode</th><th>Quality</th><th>Downloaded</th><th></th></tr></thead>`;
  for (const show of shows) {
    const sid = 'eps-' + btoa(encodeURIComponent(show.name)).replace(/=/g,'');
    html += `<tbody>
      <tr class="db-show-row" onclick="toggleEps('${sid}',this)">
        <td colspan="2"><span class="db-chevron" id="chv-${sid}">▸</span> <strong>${esc(toTitleCase(show.name))}</strong> <span style="color:var(--muted);font-size:12px">${show.episodes.length} ep${show.episodes.length !== 1 ? 's' : ''}</span></td>
        <td></td><td></td>
        <td style="text-align:right"><button class="btn-sm btn-sm-danger" onclick="event.stopPropagation();dbDeleteShow(${esc(JSON.stringify(show.name))})">Delete all</button></td>
      </tr>
    </tbody>
    <tbody class="db-eps" id="${sid}">`;
    for (const ep of show.episodes) {
      const key = show.name + '|' + ep.episode_id;
      const date = ep.downloaded_at ? new Date(ep.downloaded_at).toLocaleDateString() : '—';
      html += `<tr>
        <td style="width:16px"></td>
        <td style="color:var(--accent);font-family:monospace;font-size:12px">${esc(ep.episode_id)}</td>
        <td class="ep-quality">${esc(ep.quality || '—')}</td>
        <td style="color:var(--muted)">${date}</td>
        <td style="text-align:right"><button class="btn-sm btn-sm-danger" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(key))})">×</button></td>
      </tr>`;
    }
    html += '</tbody>';
  }
  return html + '</table>';
}

// ── movies ─────────────────────────────────────────────────────────────────────

function renderMoviesTable(entries, bucket) {
  if (!entries.length) return '<div class="db-empty">No tracked movies.</div>';
  let html = `<table class="db-table" id="db-content-table">
    <thead><tr><th>Title</th><th>Year</th><th>Quality</th><th>Downloaded</th><th></th></tr></thead><tbody>`;
  for (const e of entries) {
    const rec = e.value || {};
    const date = rec.downloaded_at ? new Date(rec.downloaded_at).toLocaleDateString() : '—';
    // 3D and non-3D versions of the same movie are tracked under distinct
    // bucket keys (recordKey() incorporates Is3D), so a user may legitimately
    // see two rows for one title. Surface a small 3D badge on the 3D one so
    // the distinction is obvious at a glance.
    const badge = rec.is_3d ? ' <span class="db-3d-badge">3D</span>' : '';
    html += `<tr>
      <td>${esc(toTitleCase(rec.title || e.key))}${badge}</td>
      <td style="color:var(--muted)">${rec.year || '—'}</td>
      <td class="ep-quality">${esc(rec.quality?.string || '—')}</td>
      <td style="color:var(--muted)">${date}</td>
      <td style="text-align:right"><button class="btn-sm btn-sm-danger" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
    </tr>`;
  }
  return html + '</tbody></table>';
}

// ── seen filter ────────────────────────────────────────────────────────────────

function renderSeenTable(entries, bucket) {
  if (!entries.length) return '<div class="db-empty">No seen entries.</div>';
  let html = `<table class="db-table" id="db-content-table">
    <thead><tr><th>Title</th><th>Task</th><th>Seen</th><th></th></tr></thead><tbody>`;
  for (const e of entries) {
    const rec = e.value || {};
    const date = rec.seen_at ? new Date(rec.seen_at).toLocaleDateString() : '—';
    html += `<tr>
      <td style="word-break:break-word">${esc(rec.title || e.key)}</td>
      <td style="color:var(--muted);white-space:nowrap">${esc(rec.task || '—')}</td>
      <td style="color:var(--muted);white-space:nowrap">${date}</td>
      <td style="text-align:right"><button class="btn-sm btn-sm-danger" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
    </tr>`;
  }
  return html + '</tbody></table>';
}

// ── caches ─────────────────────────────────────────────────────────────────────

// Cache entries are stored as {"v": <inner>, "e": "<expires-at>"} by
// internal/cache. Surface the inner value and the TTL so the user can tell
// whether a row is stale, poisoned, or live before deciding to delete.
function renderCacheTable(entries, bucket) {
  if (!entries.length) return '<div class="db-empty">No cached entries.</div>';
  let html = `<table class="db-table db-cache-table" id="db-content-table">
    <thead><tr><th>Key</th><th>Value</th><th>Expires</th><th></th></tr></thead><tbody>`;
  for (const e of entries) {
    const v = e.value || {};
    const inner = v.v !== undefined ? v.v : v;
    const expiresAt = v.e || null;
    html += `<tr>
      <td class="db-cache-key">${esc(e.key)}</td>
      <td class="db-cache-value">${esc(cacheValuePreview(inner))}</td>
      <td class="db-cache-expires" title="${esc(expiresAt || '')}">${esc(cacheExpiryLabel(expiresAt))}</td>
      <td style="text-align:right"><button class="btn-sm btn-sm-danger" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
    </tr>`;
  }
  return html + '</tbody></table>';
}

// cacheValuePreview returns a one-line preview suitable for a table cell. Shows
// shape and size cues so the user can spot empty/negative entries (a common
// reason to clear a single cache row rather than the whole bucket).
function cacheValuePreview(v) {
  if (v === null || v === undefined) return '∅';
  if (Array.isArray(v)) return `[${v.length} item${v.length === 1 ? '' : 's'}]`;
  if (typeof v === 'object') {
    const keys = Object.keys(v);
    if (!keys.length) return '{}';
    return `{${keys.slice(0, 4).join(', ')}${keys.length > 4 ? ', …' : ''}}`;
  }
  const s = String(v);
  return s.length > 120 ? s.slice(0, 117) + '…' : s;
}

// cacheExpiryLabel returns a short relative label like "in 3d 4h", "in 12m", or
// "expired 2h ago". Returns "—" if the timestamp can't be parsed.
function cacheExpiryLabel(iso) {
  if (!iso) return '—';
  const t = Date.parse(iso);
  if (isNaN(t)) return '—';
  const deltaMs = t - Date.now();
  const absMs = Math.abs(deltaMs);
  const m = Math.floor(absMs / 60000);
  const h = Math.floor(m / 60);
  const d = Math.floor(h / 24);
  let label;
  if (d > 0) label = `${d}d ${h % 24}h`;
  else if (h > 0) label = `${h}h ${m % 60}m`;
  else if (m > 0) label = `${m}m`;
  else label = '<1m';
  return deltaMs >= 0 ? 'in ' + label : 'expired ' + label + ' ago';
}

// ── shared helpers ─────────────────────────────────────────────────────────────

function toggleEps(id, row) {
  const el = document.getElementById(id);
  if (!el) return;
  el.classList.toggle('open');
  const chv = document.getElementById('chv-' + id);
  if (chv) chv.textContent = el.classList.contains('open') ? '▾' : '▸';
}

// dbFilter is defined above alongside the other pagination helpers.

let _dbErrorTimer = null;

function dbShowError(msg) {
  const main = document.getElementById('db-main-content');
  let banner = document.getElementById('db-error-banner');
  if (!banner) {
    banner = document.createElement('div');
    banner.id = 'db-error-banner';
    banner.className = 'db-error-banner';
    main.prepend(banner);
  }
  clearTimeout(_dbErrorTimer);
  banner.textContent = '✗ ' + msg;
  banner.style.display = 'block';
  _dbErrorTimer = setTimeout(() => { banner.style.display = 'none'; }, 6000);
}

async function dbClearBucket(bucket, label) {
  if (!confirm(`Clear all entries in "${label}"? They will be re-processed next run.`)) return;
  const r = await fetch('/api/db/buckets/' + encodeURIComponent(bucket), {method: 'DELETE'});
  if (r.ok) { await selectDBBucket(bucket); await loadDBSidebar(); }
  else dbShowError(await r.text());
}

async function loadDBSidebar() {
  const r = await fetch('/api/db/buckets');
  if (!r.ok) return;
  const { buckets } = await r.json();
  for (const item of dbNavItems) {
    const b = buckets.find(b => b.name === item.bucket);
    item.count = b?.count ?? 0;
  }
  renderDBSidebar();
}

async function dbDeleteShow(showName) {
  if (!confirm(`Delete all episodes of "${showName}"? They will be re-downloaded next run.`)) return;
  const r = await fetch('/api/db/buckets/series');
  if (!r.ok) { dbShowError('Could not load series list: ' + r.status); return; }
  const data = await r.json();
  const keys = (data.entries || []).filter(e => e.key.startsWith(showName + '|')).map(e => e.key);
  const results = await Promise.all(keys.map(key => fetch('/api/db/entries/series', {
    method: 'DELETE', headers: {'Content-Type':'application/json'}, body: JSON.stringify({key}),
  })));
  const failed = results.filter(res => !res.ok).length;
  if (failed) dbShowError(`${failed} of ${keys.length} deletion(s) failed`);
  await selectDBBucket('series');
  await loadDBSidebar();
}

async function dbDeleteEntry(bucket, key) {
  const r = await fetch('/api/db/entries/' + encodeURIComponent(bucket), {
    method: 'DELETE', headers: {'Content-Type':'application/json'}, body: JSON.stringify({key}),
  });
  if (r.ok) { await selectDBBucket(bucket); await loadDBSidebar(); }
  else dbShowError(await r.text());
}

