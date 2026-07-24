# Pipeliner Feature Roadmap

This document formalizes the next wave of features. Each item is designed as a
first-class DAG citizen: new capabilities arrive as source/processor/sink
plugins (or engine/UI features that serve them), dry-run must always preview
side effects, and the validator must know about any fields a plugin produces
or requires. Items are ordered into milestones at the end.

Conventions used below: *Exists* lists what the codebase already provides,
*Gaps* what must be built, *Config sketch* the intended user-facing surface.

---

## 1. List management (remote-list mutation)

**Motivation.** Pipelines currently *read* lists (`trakt_list`,
`tvdb_favorites`, `list_add`/`list_match` local lists) but can only *write*
downloads. Making list mutation a sink turns list hygiene into ordinary
pipelines: prune ended series from TheTVDB favorites, mirror a filtered Trakt
list, auto-add discovered premieres to a watchlist.

**Config sketch.**

```python
# Follow only still-running favorites: TheTVDB's API cannot remove favorites
# (see Gaps below), so the supported pattern filters them into a local list
# that the series filter consumes instead of the raw favorites.
favs   = input("tvdb_favorites")
alive  = process("condition", upstream=favs,
                 reject="series_status == 'Ended' or series_status == 'Cancelled'")
keep   = output("list_add", upstream=alive, list="active_favorites")
note   = output("notify", upstream=keep, via="pushover",
                body="Still following: {{.Title}}")
pipeline("sync-active-favorites", schedule="168h")

# Trakt lists DO support removal — full remote hygiene:
watch  = input("trakt_list", list="watchlist", type="shows")
done   = process("condition", upstream=watch,
                 accept="series_status == 'Ended' or series_status == 'Cancelled'")
prune  = output("trakt_list", upstream=done, list="watchlist", action="remove")
pipeline("prune-trakt-watchlist", schedule="168h")
```

**Exists.** `tvdb_favorites` source with rich `video_*`/`series_*` fields;
`internal/tvdb` client (search, series-by-id, extended, favorites GET);
`trakt_list` source; Trakt OAuth device flow in the web UI; `list_add` sink +
`list_match` processor for local lists.

**Gaps.**
- `series_status` (Continuing/Ended/Cancelled) is not surfaced today — add it
  to the `tvdb_favorites` source and `metainfo_tvdb` enrichment (the extended
  series response carries status), register the field constant + metadata.
- `tvdb_favorites` **sink**, `action="add"` only, `Requires: tvdb_id`.
  ✅ Resolved (2026-07-23, v4 swagger): `/user/favorites` supports GET and
  POST only — **TheTVDB's API cannot remove favorites**. `action="remove"`
  is rejected at validation with an error explaining the limitation and
  pointing at the supported pattern: filter on `series_status` into a local
  list (`list_add`) or a Trakt list (which does support removal), and use
  that list as the `series.list` source instead of raw favorites.
- `trakt_list` **sink**: add/remove items on a user list or watchlist
  (`POST /users/{id}/lists/{list}/items[/remove]`), using the existing device
  token. `Requires` one of `tmdb_id`/`tvdb_id`/`imdb_id`/`trakt_id`.
- Refresh the stale `list_add` README (still shows removed `task()` syntax).

**Deliverables.** Field constant + both enrichment paths; two sinks with
dry-run previews ("would remove X"); sample config `configs/prune-ended-favorites.star`;
user-guide section; README refresh.

## 2. Follow lifecycle for tracked series

**Motivation.** The `series` tracker follows shows forever. When a show is
Ended and its final episode is downloaded, searching for it is wasted work —
and knowing a series is complete is genuinely useful information.

**Design.** A `series_lifecycle` processor: source = the series tracker
itself (one entry per tracked show), enrich with TVDB status + episode list,
and classify: `complete` (ended, last episode downloaded), `dormant` (ended,
gaps remain — feeds backfill, see §4), `active`. Sinks decide what to do:
notify, remove from `series.static`-equivalent local lists, or mark the
tracker entry inactive (new tracker flag honored by the `series` filter).

**Exists.** Series tracker with per-episode records; TVDB episode lists.

**Gaps.** Tracker-as-source plugin, `inactive` flag + `series` filter
support, the classifier, docs.
✅ Shipped (2026-07): `series_tracker` source, `series_lifecycle` classifier
(TVDB status + aired-episode diff, `include_specials` opt-in, lookup failures
classify as `active`), `series_tracker_update` sink writing a per-show
inactive flag (parallel `series_inactive` bucket — episode records untouched)
that the `series` filter rejects on early. Sample config
`configs/series-lifecycle.star`; user-guide section "Series lifecycle".

## 3. Download-loop closure: session janitor and failed-grab recovery

**Motivation.** Pipeliner adds torrents and then never looks back. Dead
torrents leave permanent holes (the release is marked seen, no retry); seeded
torrents accumulate forever.

**Design.**
- `torrent_session` source: one entry per torrent in
  Transmission/qBittorrent/Deluge (reusing each sink's client code), with
  fields `torrent_ratio`, `torrent_seed_time`, `torrent_state`,
  `torrent_added_at`, `torrent_progress`, `torrent_stalled_for`.
- Janitor pipelines are then just `condition` + a new
  `torrent_control` sink (`action="remove"|"remove_with_data"|"pause"|"reannounce"`).
- Failed-grab recovery: a `torrent_failed` classifier (stalled longer than
  `stall_timeout`, or errored) whose sink chain marks the release **failed**
  in the seen tracker (new state: retryable) and emits a synthetic entry that
  `discover` can pick up on the next run to search for an alternative
  release. The seen tracker gains a `failed_url` bucket so the same bad
  release is never re-grabbed.

**Exists.** Transmission/qBittorrent/Deluge clients inside the sinks; seen
tracker; discover search machinery; commit-phase semantics for "only after
confirmation".

**Gaps.** Extract shared session clients into `internal/torrentclient`;
source + control sink; seen-tracker failed state; recovery classifier; docs +
sample config.
✅ Shipped (2026-07): `internal/torrentclient` (Transmission + qBittorrent +
Deluge, each with its own state-mapping layer — Deluge's Queued maps to
paused so queue backlogs are never mistaken for stalled downloads),
`torrent_session` source, `torrent_control` sink
(remove/remove_with_data/pause/reannounce, dry-run previews),
`torrent_failed` classifier (stall_timeout vs last-activity, so slow-but-
moving downloads are never flagged), `mark_failed` sink + shared
`seen_failed` bucket + `seen retry_failed=true`, and hash→release-URL grab
records written by the transmission/qbittorrent/deluge sinks at add time
(the resolver for `torrent://<hash>` session entries). `mark_failed` also
forgets the series/movies tracker records so an alternative release can be
grabbed; the "synthetic entry for discover" idea was dropped — un-tracking
makes the next regular discover/RSS run pick up an alternative naturally.
Sample config `configs/torrent-janitor.star`; user-guide section
"Download-loop closure".

## 4. Backfill via gap detection

**Motivation.** Adding a show today only catches new episodes. The tracker
knows what you have; TVDB knows what exists — the diff is the backlog.

**Design.** `series_gaps` processor: upstream entries are shows (from
`tvdb_favorites`, `trakt_list`, or the §2 tracker source); for each, fetch
the TVDB episode list, diff against the series tracker, and emit one entry
per missing episode (`series_season`, `series_episode`,
`series_episode_id`, `title`) — which feeds straight into `discover`
search backends. Season-pack preference: when more than `pack_threshold`
(default 50%) of a season is missing, emit a single season-pack query entry
instead of per-episode queries.

**Exists.** Tracker, TVDB episodes, discover + jackett search,
`ReplacesUpstream` descriptor semantics (this is exactly a
`ReplacesUpstream` processor, like `discover`).

**Gaps.** The processor, pack heuristics, rate limiting for big backlogs
(cap per run, resume next run), docs + sample config.
✅ Shipped (2026-07): `series_gaps` processor (`ReplacesUpstream`, TVDB
resolution + episode lists via the shared `internal/tvdb` Resolver extracted
from `series_lifecycle`, aired-only diff with `include_specials` opt-in,
inactive shows skipped unless `include_inactive=true`). Season packs: a
season whose missing fraction strictly exceeds `pack_threshold` (default
0.5) emits one pack query ("Show S02", `series_season` set, no
`series_episode_id` — the codebase has no pack tracking semantics, so pack
entries are plain search queries and pack grabs are not recorded
per-episode; the sample config pins `pack_threshold=1.0` for its strict
episode chain). `max_per_run` (default 30) caps emissions in deterministic
(show, season, episode) order with a persisted wrap-around cursor per
pipeline; dry-run never advances it. Sample config
`configs/series-backfill.star` (lifecycle dormant gate → gaps → discover →
`series tracking="backfill"` → transmission + chained notify); user-guide
section "Backfill".

## 5. Library awareness — ✅ shipped (filesystem backend PR #311; plex/jellyfin backends + library_refresh in M5b PR)

**Motivation.** The seen tracker knows what pipeliner grabbed — not what is
actually on disk. Real-library checks enable disk-truth dedup and true
quality upgrades ("grab 1080p because the file on disk is 720p").

**Design.**
- `library` filter: reject entries whose episode/movie already exists in the
  library at equal-or-better quality. Backends: filesystem glob (parse
  filenames with the existing `internal/series`/`internal/movies` parsers)
  first; Plex/Jellyfin API backends later (same plugin, `backend=` key).
- `library_refresh` chained sink: after the download sink confirms, poke
  Plex/Jellyfin to rescan the relevant path. Chained-sink semantics already
  guarantee it only fires on confirmed downloads.

**Exists.** Filename parsers, quality comparison (upgrade detection in the
series tracker), sink chaining, `pathfmt` for destination paths.

**Gaps.** Library index (scan + cache in a store bucket with mtime
invalidation), the filter, refresh sink, Plex/Jellyfin clients, docs.

## 6. Notification upgrades — ✅ digest mode + tvdb_calendar shipped; trakt_calendar deferred (same shape, add on demand); run_report deferred to M8 (needs persisted per-run tallies)

**Motivation.** Per-entry pushes get muted; a daily/weekly summary gets read.
The engine already records accept/reject reasons — surfacing them is cheap.

**Design.**
- `notify` digest mode: `digest="run"|"1d"|"7d"` batches accepted entries
  (store-buffered for time windows) into one templated message.
- `tvdb_calendar` / `trakt_calendar` sources: one entry per upcoming episode
  for followed shows — "tonight's episodes" notify pipelines, or feed
  `discover` for day-of searching.
- Skip report: a `run_report` source emitting per-run aggregates (accepted /
  rejected by reason / failed) for a weekly "what happened and why" digest.

**Exists.** Notifier registry (email/pushover/webhook), reasons on every
entry, run history in the web server.

**Gaps.** Digest buffering, calendar API calls, run-report source (needs the
engine to persist per-run reason tallies — today they only live in memory).

## 7. Webhook ingest — ✅ shipped (webhook source + POST /api/ingest/{queue} with bearer token, bounded queues, optional immediate trigger)

**Motivation.** Polling has latency; announce bots (autobrr, IRC bridges)
can push. One authenticated endpoint makes pipeliner composable with
anything that can POST JSON.

**Design.** `webhook` source plugin that registers
`POST /api/ingest/{pipeline}` (token auth via `env()`), maps JSON fields to
entry fields (configurable mapping), queues entries, and triggers the
pipeline immediately via the existing scheduler `Trigger` path. Rate-limited
and size-capped.

**Exists.** Web server + auth, scheduler trigger, `env()`.

**Gaps.** Source plugin + server route + queue handoff, docs, sample config
pairing it with autobrr.

## 8. Run inspector — ✅ shipped (executor trace capture, capped trace store, /api/traces endpoints, dashboard drill-down; run_report from §6 remains open)

**Motivation.** "Why didn't it grab X?" is the most common debugging
question. The engine knows the answer per entry, per node; the dashboard
only shows counts.

**Design.** Persist per-run entry traces (entry title/URL, per-node verdict
+ reason, final state) to a capped store bucket (last N runs, opt-out
config). Dashboard run-history rows (added in PR #300) expand into an entry
table with a per-node trace drill-down. Dry-runs always record traces —
making dry-run + inspector the standard config-debugging loop.

**Exists.** Run history UI, reasons on entries, dry-run mode.

**Gaps.** Trace capture in the executor (bounded memory), storage schema,
API, UI table + trace view, docs.

## 9. Pipeline triggers — ✅ shipped (after="parent[:accepted]" with cycle validation, runner cascade, dashboard indication, visual-editor round-trip)

**Motivation.** Multi-stage flows (gaps → search → notify; sync-list →
filter) currently rely on schedule phasing. An explicit trigger is simpler
and race-free.

**Design.** `pipeline("b", after="a")` — run B when A finishes, optionally
`after="a:accepted"` (only when A accepted ≥1 entry). Scheduler-level:
`Daemon` fires dependents on completion; cycles rejected at config
validation; UI shows the chain on the dashboard.

**Exists.** Scheduler with named-task triggers, config validation pass.

**Gaps.** Trigger wiring, validation, dashboard indication, docs.

---

## Milestones and order

| # | Milestone | Items | Rationale |
|---|-----------|-------|-----------|
| M1 | List management core | §1 | The seed use case; small surface; establishes the list-mutation sink pattern |
| M2 | Follow lifecycle | §2 | Builds directly on M1's status enrichment |
| M3 | Download-loop closure | §3 | Biggest quiet win for every existing pipeline |
| M4 | Backfill | §4 | Reuses M3's failed/seen plumbing and discover |
| M5 | Library awareness | §5 | Independent; larger surface (external APIs) |
| M6 | Notification upgrades | §6 | Independent; run-report depends on §8's tallies (do digest+calendar first) |
| M7 | Webhook ingest | §7 | Independent; small |
| M8 | Run inspector | §8 | UI-heavy; lands best after M3/M4 increase pipeline complexity |
| M9 | Pipeline triggers | §9 | Scheduler change; benefits M2/M4 flows once they exist |

Every milestone ships as one or more PRs with tests, a sample config that
passes `pipeliner check`, and a user-guide section. Dry-run previews are a
hard requirement for anything with remote side effects (M1, M3, M5).
