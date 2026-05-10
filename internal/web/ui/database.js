// ── database tab ─────────────────────────────────────────────────────────────

function toTitleCase(s) {
  return s.replace(/\b\w/g, c => c.toUpperCase());
}

let dbLoaded = false;
let dbActiveBucket = null;
let dbNavItems = []; // [{bucket, label, count}]
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
    dbNavItems.push({bucket, label, count: b?.count ?? 0});
  }
  // Any per-task local seen buckets (seen:task-name).
  for (const b of buckets) {
    if (b.name.startsWith('seen:')) {
      dbNavItems.push({bucket: b.name, label: 'Seen: ' + b.name.slice(5), count: b.count});
    }
  }
  dbLoaded = true;
  renderDBSidebar();
  if (dbNavItems.length && !dbActiveBucket) selectDBBucket(dbNavItems[0].bucket);
}

function renderDBSidebar() {
  let html = '<div class="db-sidebar-section">Trackers</div>';
  for (const item of dbNavItems) {
    const active = dbActiveBucket === item.bucket ? ' active' : '';
    html += `<button class="db-nav-btn${active}" onclick="selectDBBucket(${esc(JSON.stringify(item.bucket))})">
      <span>${esc(item.label)}</span>
      <span class="db-nav-count">${item.count}</span>
    </button>`;
  }
  document.getElementById('db-sidebar').innerHTML = html;
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

async function fetchDBPage(name) {
  const main = document.getElementById('db-main-content');
  main.innerHTML = '<div class="db-loading">Loading…</div>';
  const r = await fetch(dbPageURL(name));
  if (!r.ok) { main.innerHTML = '<div class="db-empty">Error loading data.</div>'; return; }
  const data = await r.json();
  renderDBContent(name, data);
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

async function dbFilter(val) {
  dbFilterQuery = (val || '').toLowerCase().trim();
  dbCurrentCursor = '';
  dbCursorStack = [];
  if (dbActiveBucket) await fetchDBPage(dbActiveBucket);
}

function renderDBContent(name, data) {
  const item = dbNavItems.find(i => i.bucket === name) || {label: name};
  const main = document.getElementById('db-main-content');

  const toolbar = `<div class="db-toolbar" style="margin-bottom:12px">
    <span class="db-title">${esc(item.label)}</span>
    <div style="display:flex;gap:8px;align-items:center">
      <input type="text" class="db-search" id="db-filter-input" placeholder="filter…"
        value="${esc(dbFilterQuery)}" oninput="dbFilter(this.value)">
      <button class="btn-danger" onclick="dbClearBucket(${esc(JSON.stringify(name))},${esc(JSON.stringify(item.label))})">Clear all</button>
    </div>
  </div>`;

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
  else content = renderSeenTable(data.entries || [], name);

  main.innerHTML = toolbar + pager + `<div class="db-scroll">${content}</div>`;
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
        <td style="text-align:right"><button class="btn-sm" onclick="event.stopPropagation();dbDeleteShow(${esc(JSON.stringify(show.name))})">Delete all</button></td>
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
        <td style="text-align:right"><button class="btn-sm" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(key))})">×</button></td>
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
    html += `<tr>
      <td>${esc(toTitleCase(rec.title || e.key))}</td>
      <td style="color:var(--muted)">${rec.year || '—'}</td>
      <td class="ep-quality">${esc(rec.quality?.string || '—')}</td>
      <td style="color:var(--muted)">${date}</td>
      <td style="text-align:right"><button class="btn-sm" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
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
      <td style="text-align:right"><button class="btn-sm" onclick="dbDeleteEntry(${esc(JSON.stringify(bucket))},${esc(JSON.stringify(e.key))})">×</button></td>
    </tr>`;
  }
  return html + '</tbody></table>';
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

async function dbClearBucket(bucket, label) {
  if (!confirm(`Clear all entries in "${label}"? They will be re-processed next run.`)) return;
  const r = await fetch('/api/db/buckets/' + encodeURIComponent(bucket), {method: 'DELETE'});
  if (r.ok) { await selectDBBucket(bucket); await loadDBSidebar(); }
  else alert('Error: ' + await r.text());
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
  const data = await r.json();
  const keys = (data.entries || []).filter(e => e.key.startsWith(showName + '|')).map(e => e.key);
  await Promise.all(keys.map(key => fetch('/api/db/entries/series', {
    method: 'DELETE', headers: {'Content-Type':'application/json'}, body: JSON.stringify({key}),
  })));
  await selectDBBucket('series');
  await loadDBSidebar();
}

async function dbDeleteEntry(bucket, key) {
  const r = await fetch('/api/db/entries/' + encodeURIComponent(bucket), {
    method: 'DELETE', headers: {'Content-Type':'application/json'}, body: JSON.stringify({key}),
  });
  if (r.ok) { await selectDBBucket(bucket); await loadDBSidebar(); }
  else alert('Error: ' + await r.text());
}

