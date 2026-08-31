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
  // Diagnostic tools (no backing bucket) live in their own section.
  dbNavItems.push({bucket: '__match_tester__', label: '🔍 Match tester', section: 'tools'});
  dbNavItems.push({bucket: '__quality_tester__', label: '🎚 Quality tester', section: 'tools'});
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
  const tools = dbNavItems.filter(i => i.section === 'tools');
  const renderToolSection = (items, title) => {
    if (!items.length) return '';
    let html = `<div class="db-sidebar-section">${title}</div>`;
    for (const item of items) {
      const active = dbActiveBucket === item.bucket ? ' active' : '';
      html += `<button class="db-nav-btn${active}" onclick="selectDBBucket(${esc(JSON.stringify(item.bucket))})">
        <span>${esc(item.label)}</span>
      </button>`;
    }
    return html;
  };
  document.getElementById('db-sidebar').innerHTML =
    renderSection(trackers, 'Trackers') + renderSection(caches, 'Caches') +
    renderToolSection(tools, 'Tools');
}

function dbPageURL(name) {
  const p = new URLSearchParams({limit: dbPageSize});
  if (dbCurrentCursor) p.set('after', dbCurrentCursor);
  if (dbFilterQuery)   p.set('q', dbFilterQuery);
  return '/api/db/buckets/' + encodeURIComponent(name) + '?' + p;
}

async function selectDBBucket(name) {
  dbActiveBucket = name;
  if (name === '__match_tester__') {
    renderDBSidebar();
    renderMatchTester();
    return;
  }
  if (name === '__quality_tester__') {
    renderDBSidebar();
    renderQualityTester();
    return;
  }
  dbCurrentCursor = '';
  dbCursorStack = [];
  dbFilterQuery = '';
  // The fast-path render keeps the toolbar (and its filter input) alive when
  // re-selecting the current bucket, so the visible text must be reset along
  // with the state or the input would show a filter that is no longer applied.
  const inp = document.getElementById('db-filter-input');
  if (inp) inp.value = '';
  renderDBSidebar();
  await fetchDBPage(name);
}

// dbRefreshBucket re-fetches the active bucket without touching the filter or
// pagination state — used after row deletions so the user keeps their place
// (active filter, current page) instead of being bounced back to an
// unfiltered first page.
async function dbRefreshBucket(name) {
  await fetchDBPage(name);
  await loadDBSidebar();
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

  // Fast path: same bucket as last render. The toolbar (filter input + Delete
  // all) is already in the DOM and the user may be actively typing into the
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
      <button class="btn-danger" onclick="dbClearBucket(${esc(JSON.stringify(name))},${esc(JSON.stringify(item.label))})">Delete all</button>
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
      <tr class="db-show-row" role="button" tabindex="0" aria-expanded="false"
          onclick="toggleEps('${sid}',this)" onkeydown="dbShowRowKeydown(event,'${sid}',this)">
        <td colspan="2"><span class="db-chevron" id="chv-${sid}">▸</span> <strong>${esc(toTitleCase(show.name))}</strong> <span style="color:var(--muted);font-size:12px">${show.episodes.length} ep${show.episodes.length !== 1 ? 's' : ''}</span></td>
        <td></td><td></td>
        <td style="text-align:right"><button class="btn-sm btn-sm-danger" aria-label="Delete all episodes of ${esc(show.name)}" onclick="event.stopPropagation();dbDeleteShow(${esc(JSON.stringify(show.series_name ?? show.name))},${esc(JSON.stringify(show.name))})">Delete all</button></td>
      </tr>
    </tbody>
    <tbody class="db-eps" id="${sid}">`;
    for (const ep of show.episodes) {
      // The stored key comes from the API — display name and stored key
      // material can differ (the tracker normalizes/lowercases names), so
      // reconstructing "name|episode" client-side would delete nothing.
      const key = ep.key || (show.series_name ?? show.name) + '|' + ep.episode_id;
      const date = ep.downloaded_at ? new Date(ep.downloaded_at).toLocaleDateString() : '—';
      html += `<tr>
        <td style="width:16px"></td>
        <td style="color:var(--accent);font-family:monospace;font-size:12px">${esc(ep.episode_id)}</td>
        <td class="ep-quality">${esc(ep.quality || '—')}</td>
        <td style="color:var(--muted)">${date}</td>
        <td style="text-align:right"><button class="btn-sm btn-sm-danger" aria-label="Delete ${esc(show.name + ' ' + ep.episode_id)}" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(key))})">×</button></td>
      </tr>`;
    }
    html += '</tbody>';
  }
  return html + '</table>';
}

// dbShowRowKeydown makes the expandable show rows keyboard-operable: Enter
// and Space toggle the episode list, mirroring the click handler.
function dbShowRowKeydown(ev, sid, row) {
  if (ev.key !== 'Enter' && ev.key !== ' ') return;
  ev.preventDefault();
  toggleEps(sid, row);
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
      <td style="color:var(--muted)">${esc(rec.year || '—')}</td>
      <td class="ep-quality">${esc(rec.quality?.string || '—')}</td>
      <td style="color:var(--muted)">${date}</td>
      <td style="text-align:right"><button class="btn-sm btn-sm-danger" aria-label="Delete ${esc(rec.title || e.key)}" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
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
      <td style="text-align:right"><button class="btn-sm btn-sm-danger" aria-label="Delete ${esc(rec.title || e.key)}" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
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
    const title = cacheKeyTitle(inner);
    const keyHtml = title
      ? `<span class="db-cache-key-id">${esc(e.key)}</span><span class="db-cache-key-title">${esc(title)}</span>`
      : esc(e.key);
    html += `<tr>
      <td class="db-cache-key">${keyHtml}</td>
      <td class="db-cache-value">${esc(cacheValuePreview(inner))}</td>
      <td class="db-cache-expires" title="${esc(expiresAt || '')}">${esc(cacheExpiryLabel(expiresAt))}</td>
      <td style="text-align:right"><button class="btn-sm btn-sm-danger" aria-label="Delete ${esc(e.key)}" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
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
    // Wrappers that pair a name with a list (e.g. the TVDB episodes cache
    // stores {name, episodes: [...]}) read better as "[62 episodes]" than
    // the generic "{name, episodes}" — the name is already surfaced as the
    // key subtitle via cacheKeyTitle so repeating it here is noise. Only
    // applies when exactly one non-name field is present and it's an array.
    const nonNameKeys = keys.filter(k => k !== 'name' && k !== 'Name');
    if (nonNameKeys.length === 1 && Array.isArray(v[nonNameKeys[0]])) {
      const label = nonNameKeys[0];
      const n = v[label].length;
      const singular = n === 1 ? label.replace(/s$/, '') : label;
      return `[${n} ${singular}]`;
    }
    return `{${keys.slice(0, 4).join(', ')}${keys.length > 4 ? ', …' : ''}}`;
  }
  const s = String(v);
  return s.length > 120 ? s.slice(0, 117) + '…' : s;
}

// cacheKeyTitle returns a human-friendly label derived from the inner cached
// value, to show alongside opaque ID keys (TMDb/TVDB/Blu-ray detail caches
// all key on numeric IDs but carry a title or name on the value). Tries the
// common JSON shapes: TMDb uses lowercase {title, release_date}, TVDB uses
// {name, year, firstAired}, Blu-ray's untagged Go struct serialises as
// {Title, Year}. Returns '' when nothing usable is found, in which case the
// key renders as-is.
function cacheKeyTitle(v) {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return '';
  const t = v.title || v.Title || v.name || v.Name || '';
  if (typeof t !== 'string' || !t) return '';
  const y = v.year || v.Year ||
    (typeof v.release_date === 'string' ? v.release_date.slice(0, 4) : '') ||
    (typeof v.firstAired === 'string' ? v.firstAired.slice(0, 4) : '');
  const yStr = y == null ? '' : String(y).trim();
  return /^\d{4}$/.test(yStr) ? `${t} (${yStr})` : t;
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
  const open = el.classList.contains('open');
  const chv = document.getElementById('chv-' + id);
  if (chv) chv.textContent = open ? '▾' : '▸';
  if (row && row.setAttribute) row.setAttribute('aria-expanded', String(open));
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
  if (!confirm(`Delete all entries in "${label}"? They will be re-processed next run.`)) return;
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

// dbDeleteShowRequest builds the fetch arguments for a whole-show deletion.
// The normalized series name (key material, not the display name) travels in
// the JSON body so names containing slashes or pipes need no URL encoding.
// Pure — extracted so unit tests can pin the endpoint and payload.
function dbDeleteShowRequest(seriesName) {
  return {
    url: '/api/db/series/show',
    options: {
      method: 'DELETE', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({series_name: seriesName}),
    },
  };
}

// dbDeleteEntryRequest builds the fetch arguments for a single-row deletion.
// Pure — extracted so unit tests can pin the endpoint and payload.
function dbDeleteEntryRequest(bucket, key) {
  return {
    url: '/api/db/entries/' + encodeURIComponent(bucket),
    options: {
      method: 'DELETE', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({key}),
    },
  };
}

// dbDeleteShow deletes every episode of a show server-side in one request.
// seriesName is the normalized stored name; label is the display name shown
// in the confirmation. (Deleting client-side by paging through keys would
// only ever see the current page — shows beyond page 1 would survive.)
async function dbDeleteShow(seriesName, label) {
  if (!confirm(`Delete all episodes of "${label}"? They will be re-downloaded next run.`)) return;
  const {url, options} = dbDeleteShowRequest(seriesName);
  const r = await fetch(url, options);
  if (r.ok) await dbRefreshBucket('series');
  else dbShowError(await r.text());
}

async function dbDeleteEntry(bucket, key) {
  if (!confirm('Delete this entry? It may be re-downloaded next run.')) return;
  const {url, options} = dbDeleteEntryRequest(bucket, key);
  const r = await fetch(url, options);
  if (r.ok) await dbRefreshBucket(bucket);
  else dbShowError(await r.text());
}


// ── match tester ─────────────────────────────────────────────────────────────
// A diagnostic panel that answers "why doesn't title X match my list?" without
// a log dive, mirroring the `pipeliner match` CLI. It POSTs to /api/match/test
// and renders the verdict plus a nearest-first candidate table.

// listCacheBuckets returns the cache buckets that hold resolved title lists
// (cache_*_list), so the tester can offer them as candidate sources.
function listCacheBuckets() {
  return dbNavItems
    .filter(i => i.section === 'caches' && /_list$/.test(i.bucket))
    .map(i => ({bucket: i.bucket, label: i.label}));
}

function renderMatchTester() {
  const main = document.getElementById('db-main-content');
  const lists = listCacheBuckets();
  const listOpts = lists.map(l =>
    `<option value="${esc(l.bucket)}">${esc(l.label)}</option>`).join('');
  main.innerHTML = `
    <div class="match-tester">
      <h2>Match tester</h2>
      <p class="match-hint">Check whether a release title matches a show/movie list — and if not, see the nearest near-misses. This is exactly the normalization the <code>series</code> and <code>movies</code> filters apply.</p>
      <div class="match-form">
        <label>Title
          <input id="match-input" type="text" placeholder="Star Trek Strange New Worlds" />
        </label>
        <label>Year (optional)
          <input id="match-year" type="number" min="0" placeholder="0" />
        </label>
        <label>Compare against
          <select id="match-source">
            <option value="">Titles I type below</option>
            ${listOpts}
          </select>
        </label>
        <label id="match-candidates-wrap">Candidate titles (one per line)
          <textarea id="match-candidates" rows="4" placeholder="Silo&#10;Star Trek: Strange New Worlds&#10;The Ark"></textarea>
        </label>
        <button class="btn" onclick="runMatchTest()">Test match</button>
      </div>
      <div id="match-results"></div>
    </div>`;
  const src = document.getElementById('match-source');
  const wrap = document.getElementById('match-candidates-wrap');
  const sync = () => { wrap.style.display = src.value === '' ? '' : 'none'; };
  src.addEventListener('change', sync);
  sync();
  document.getElementById('match-input').focus();
}

async function runMatchTest() {
  const input = document.getElementById('match-input').value.trim();
  const results = document.getElementById('match-results');
  if (!input) { results.innerHTML = '<div class="db-empty">Enter a title to test.</div>'; return; }
  const year = parseInt(document.getElementById('match-year').value, 10) || 0;
  const bucket = document.getElementById('match-source').value;
  const body = {input, year};
  if (bucket) body.bucket = bucket;
  else body.candidates = document.getElementById('match-candidates').value
    .split('\n').map(s => s.trim()).filter(Boolean);
  results.innerHTML = '<div class="db-loading">Testing…</div>';
  try {
    const r = await fetch('/api/match/test', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
    });
    if (!r.ok) { results.innerHTML = `<div class="db-empty">Error: ${esc(await r.text())}</div>`; return; }
    results.innerHTML = matchTesterResultHTML(await r.json());
  } catch (e) {
    results.innerHTML = `<div class="db-empty">Error: ${esc(e.message)}</div>`;
  }
}

// matchTesterResultHTML builds the results markup from a ProbeResult. Pure
// (no DOM/fetch) so it is unit-testable.
function matchTesterResultHTML(res) {
  const verdict = res.matched
    ? `<span class="match-yes">MATCH</span> → <code>${esc(res.matched_by)}</code>`
    : `<span class="match-no">NO MATCH</span>`;
  let html = `<div class="match-verdict">${verdict}</div>
    <div class="match-norm">normalized input: <code>${esc(res.input_norm)}</code></div>`;
  const cands = res.candidates || [];
  if (!cands.length) {
    html += '<div class="db-empty">No candidates to compare against.</div>';
    return html;
  }
  const MAX = 15;
  let shownNonMatch = 0, hidden = 0;
  let rows = '';
  for (const c of cands) {
    if (!c.matched) {
      if (shownNonMatch >= MAX) { hidden++; continue; }
      shownNonMatch++;
    }
    let note = '';
    if (c.punctuation_only) note = 'punctuation-only diff';
    else if (!c.matched && c.title_matched) note = 'year mismatch';
    rows += `<tr class="${c.matched ? 'match-row-yes' : ''}">
      <td>${c.matched ? '✓' : ''}</td>
      <td>${c.distance}</td>
      <td>${c.year || ''}</td>
      <td>${esc(note)}</td>
      <td><code>${esc(c.norm)}</code></td>
    </tr>`;
  }
  html += `<table class="match-table">
    <thead><tr><th></th><th>dist</th><th>year</th><th>note</th><th>candidate</th></tr></thead>
    <tbody>${rows}</tbody></table>`;
  if (hidden > 0) html += `<div class="match-norm">${hidden} more non-matching candidate(s) hidden; nearest shown first.</div>`;
  return html;
}

// ── quality tester ───────────────────────────────────────────────────────────
// The quality-side companion to the match tester: parse a release title into a
// quality and (optionally) report per-dimension whether it satisfies a spec.
// Mirrors the `pipeliner quality` CLI. POSTs to /api/quality/test.

function renderQualityTester() {
  const main = document.getElementById('db-main-content');
  main.innerHTML = `
    <div class="match-tester">
      <h2>Quality tester</h2>
      <p class="match-hint">Parse a release title into its quality, and check it against a spec — the same parsing and matching the <code>quality</code> filter uses. When a download is unexpectedly filtered on quality, this shows which dimension failed.</p>
      <div class="match-form">
        <label>Release title
          <input id="quality-title" type="text" placeholder="Show S01E01 720p WEB-DL x265" />
        </label>
        <label>Spec (optional)
          <input id="quality-spec" type="text" placeholder="720p-1080p webrip+" />
        </label>
        <button class="btn" onclick="runQualityTest()">Test quality</button>
      </div>
      <div id="quality-results"></div>
    </div>`;
  document.getElementById('quality-title').focus();
}

async function runQualityTest() {
  const title = document.getElementById('quality-title').value.trim();
  const results = document.getElementById('quality-results');
  if (!title) { results.innerHTML = '<div class="db-empty">Enter a release title.</div>'; return; }
  const spec = document.getElementById('quality-spec').value.trim();
  results.innerHTML = '<div class="db-loading">Testing…</div>';
  try {
    const r = await fetch('/api/quality/test', {
      method: 'POST', headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({title, spec}),
    });
    if (!r.ok) { results.innerHTML = `<div class="db-empty">Error: ${esc(await r.text())}</div>`; return; }
    results.innerHTML = qualityTesterResultHTML(await r.json(), spec);
  } catch (e) {
    results.innerHTML = `<div class="db-empty">Error: ${esc(e.message)}</div>`;
  }
}

// qualityTesterResultHTML builds the results markup from a SpecResult. Pure
// (no DOM/fetch) so it is unit-testable. hasSpec controls whether the
// per-dimension verdict table is shown (an empty spec just reports quality).
function qualityTesterResultHTML(res, spec) {
  let html = `<div class="match-norm">detected quality: <code>${esc(res.quality || 'unknown')}</code></div>`;
  if (!spec) return html;
  const verdict = res.matched
    ? `<span class="match-yes">MATCH</span>`
    : `<span class="match-no">NO MATCH</span>`;
  html = `<div class="match-verdict">${verdict} <span class="match-norm">against <code>${esc(res.spec)}</code></span></div>` + html;
  const constrained = (res.dimensions || []).filter(d => d.constrained);
  if (!constrained.length) {
    html += '<div class="match-norm">Spec places no constraints on any dimension.</div>';
    return html;
  }
  let rows = '';
  for (const d of constrained) {
    const mark = d.passed ? '✓' : '✗';
    let note = '';
    if (d.bypassed) note = 'bypassed (optional, value unknown)';
    else if (!d.passed) note = 'does not satisfy constraint';
    rows += `<tr class="${d.passed ? '' : 'match-row-no'}">
      <td>${mark}</td>
      <td>${esc(d.name)}</td>
      <td><code>${esc(d.constraint)}</code></td>
      <td><code>${esc(d.value)}</code></td>
      <td>${esc(note)}</td>
    </tr>`;
  }
  html += `<table class="match-table">
    <thead><tr><th></th><th>dimension</th><th>constraint</th><th>value</th><th>note</th></tr></thead>
    <tbody>${rows}</tbody></table>`;
  return html;
}
