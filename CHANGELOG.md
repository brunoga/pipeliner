# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.2.1] - 2026-06-01

### Fixed

- **Deluge sink rejects empty / scheme-less URLs locally** ([#201](https://github.com/brunoga/pipeliner/pull/201)). Entries arriving at the `deluge` sink with `e.URL == ""` (or a URL lacking `http://` / `https://` / `magnet:`) surfaced as a cryptic Twisted traceback from `core.add_torrent_url` — `twisted.web.error.SchemeNotSupported: Unsupported scheme: b''` — buried inside the deluge RPC error. The sink now fails fast with `entry has empty URL` or `unsupported URL scheme: "..."` before any RPC is attempted, so the actionable error lands on the entry's `FailReason` instead of in a Python stack trace.
- **`rss` and `jackett` no longer propagate non-URL feed identifiers** ([#201](https://github.com/brunoga/pipeliner/pull/201)). Both parsers fell back to the RSS `<guid>` (or Atom `<id>`) as `e.URL` when no link / enclosure was present. But RSS GUIDs with `isPermaLink="false"` are opaque identifiers (hashes, internal IDs), and Atom `<id>` is an IRI commonly of the form `urn:uuid:…` or `tag:host,date:…` — none of which are fetchable. The GUID / `<id>` fallback is now only accepted when the value actually starts with `http://`, `https://`, or `magnet:`; otherwise the item is dropped, matching the existing behaviour for items with no link at all. This is the root cause for the Deluge failures above.
- **Drag in the visual editor follows attached search / list sub-nodes** ([#202](https://github.com/brunoga/pipeliner/pull/202)). When a parent node owned a search / list sub-node (e.g. `discover` + `rss_search`, `series` + `tvdb_favorites`) or route ports, dragging the parent left the sub-nodes frozen in place — both for plain single-node drags and for multi-selections where the sub-node was visibly highlighted as part of the group. Stale sub-node positions then persisted into the `# pipeliner:pos` comment on save. Both drag paths now translate each attached sub-node by the parent's *post-clamp* effective delta, so they stay anchored to the parent even when the group hits a pipeline region boundary.

## [1.2.0] - 2026-06-01

### Added

- **Persistent rotating log file** ([#199](https://github.com/brunoga/pipeliner/pull/199)). Both `pipeliner run` and `pipeliner daemon` now tee their slog output through a size-based rotating writer in `internal/logfile`, persisting to `pipeliner.log` next to `config.star` and `pipeliner.db`. Defaults are 10 MB × 5 archives (~50 MB disk cap), uncolored so `cat` / `less` / `grep` stay readable. The file is reopened with `O_APPEND` on restart so the prior session's tail survives, and rotation happens on whole-write boundaries — a single slog record never straddles two files.
- **Dashboard log scrollback** ([#199](https://github.com/brunoga/pipeliner/pull/199)). The Live Log on the dashboard now lazy-loads older entries from `pipeliner.log` when the user scrolls within 40 px of the top, prepending them in chronological order while pinning `scrollTop` so the view doesn't jump. Pagination is server-side via `GET /api/logs/history?offset=N&limit=M`: offsets count lines from EOF and the endpoint walks `.1..N` archives in oldest-to-newest order, so a single request can span the current file and any number of archives. A subtle "── start of recorded history ──" separator marks the end of recorded history.
- **Brand-mark SVG favicon** ([#198](https://github.com/brunoga/pipeliner/pull/198)). Replaces the generic `▶` glyph (which read as "play/media") with a small directed-graph mark that mirrors the visual editor's role palette: green source → blue processor → orange sink, joined by two short edges into a directed-arrow shape. The same SVG is served as the browser-tab favicon and used in the dashboard header, the login page, and the user-guide sidebar so the tab and the app read as one product. Served from the unauthenticated route so the login page renders the icon too.

### Fixed

- **`exitFunctionEditor` no longer drifts pipelines down on save** ([#197](https://github.com/brunoga/pipeliner/pull/197)). After editing a function, clicking *Save* inside the function editor, then *Save & Reload* on the config, every pipeline shifted down by ~one region-height — and *Save & Reload* persisted the drifted layout. `openFunctionEditor` stashes the live `ve.graphs` by reference (absolute y, `_regionY` set), and `exitFunctionEditor` called `initLayout()` directly on that restored reference; `initLayout`'s stored-position branch then added `_regionY` *again*. The conversion is now factored into a shared `initLayoutFromAbsolute()` helper used by both `exitFunctionEditor` and `undo()` (the latter already did the right thing manually), so the next call site that needs to restore absolute-coord graphs can't silently reintroduce the drift.
- **Marquee-select highlights owned `list` / `search` sub-nodes** ([#196](https://github.com/brunoga/pipeliner/pull/196)). Ctrl+Click on a parent that owns a list sub-node (e.g. `series` + `tvdb_favorites`) or a search sub-node (e.g. `discover` + `rss_search`) already highlighted both. Marquee-drag was leaving the sub-node un-highlighted until a full re-render fired, even though the parent ended up in the selection. `marqueeSelect` now mirrors the Ctrl+Click path: after adding a main node it walks `searchNodeIds` / `listNodeIds` and toggles the `multi-selected` class on each. Sub-node IDs themselves stay out of `ve.selectedNodeIds` — they ride along with their parent both visually and during "Extract to Function", matching the existing model.
- **Marquee-select activates the pipeline under the drag** ([#195](https://github.com/brunoga/pipeliner/pull/195)). Rubber-banding inside a pipeline that wasn't the active one silently selected nothing — the commit step only scanned `ve.graphs[ve.activeGraph]`. The marquee now picks the graph at the rectangle's centre via `findGraphAtPosition`, activates it through `setActiveGraph`, and only then iterates that graph's nodes. Marquees that don't intersect any pipeline region (the gap between two pipelines, or below all of them) are now a no-op rather than accidentally selecting nodes from whichever pipeline happened to be active.

## [1.1.1] - 2026-05-31

### Fixed

- **Title matching no longer tolerates single-character edits** ([#193](https://github.com/brunoga/pipeliner/pull/193)). `match.Fuzzy` previously accepted any pair within Levenshtein distance 1, which let "Masters of the Universe (2026)" match the unrelated movie "Master of the Universe (2026)" — and similar single-letter title flips elsewhere — causing the wrong content to download silently. Matching is now exact-or-glob only; `Normalize()` still handles realistic variation (case, dots, underscores, hyphens). Watchlist sources provide canonical titles and user-typed lists are short enough to proofread, so a no-match is observable while a wrong-match was not.

## [1.1.0] - 2026-05-31

### Added

- **Touch and pointer-input support in the visual editor** ([#183](https://github.com/brunoga/pipeliner/pull/183)). Two-finger pinch zoom and pan, palette-to-canvas drag via `PointerEvents`, larger touch targets on coarse-pointer devices. The editor is now usable on tablets, foldables, and touch monitors.
- **Field deprecation framework** ([#185](https://github.com/brunoga/pipeliner/pull/185)). `entry.FieldMeta` now carries `Deprecated`, `ReplacedBy`, and `DeprecationNote` flags. The DAG validator emits an advisory warning whenever a deprecated field is referenced statically — in plugin `Requires`, `condition` rules, `route()` port expressions, `require(fields=…)`, or any `FieldTypePattern` config such as `pathfmt path=…`. Warnings flow through `pipeliner check` and the visual editor's inline validation. The condition/route field picker segregates deprecated names into a dedicated optgroup and renders chips with strikethrough.
- **`media_type` is now set by every plugin that knows it** ([#188](https://github.com/brunoga/pipeliner/pull/188), [#189](https://github.com/brunoga/pipeliner/pull/189)). The `series`, `movies`, and `premiere` filters stamp `media_type` on every processed entry (`Produces`); `metainfo_tmdb`, `metainfo_trakt`, and `metainfo_tvdb` set it on successful enrichment (`MayProduce`); `trakt_list` and `tvdb_favorites` set it on every emitted entry (`Produces`). The payoff lands on `dedup`: a realistic `metainfo_file → … → series/movies → dedup` pipeline no longer flashes a `media_type` warning.
- **`series` and `movies` filters now accept an optional title list** ([#190](https://github.com/brunoga/pipeliner/pull/190)). With neither `static` nor `list` set, the filter operates in **accept-all mode**: every classified entry passes the upstream `Requires` and the quality / tracker checks, with no title matching. The tracker still dedups and detects upgrades by series name + episode ID (or movie title), so this is the right configuration for "download every quality-matching episode/movie I find" pipelines.
- **Visual-editor layout diagnostic** ([#191](https://github.com/brunoga/pipeliner/pull/191)). Setting `localStorage.veDebug = '1'` in the browser console enables verbose `console.log` output at every layout-mutating call site (`initLayout`, `pushUndo`, `undo`, `tightenPipeline`, node drag end, `textToVisualSync`), with compact per-graph y / `_regionY` snapshots. No-op when the flag is unset.

### Changed

- **`dedup` now requires `media_type` to be reachable upstream** ([#188](https://github.com/brunoga/pipeliner/pull/188)). The classifier signal is the trigger for both series and movie modes (formerly: `series_episode_id` alone for series, the now-deprecated `movie_title` for movies). For realistic pipelines this is invisible — `metainfo_file`, `metainfo_tmdb`, `metainfo_tvdb`, and the new classifier-filter `Produces` all set `media_type`. Pipelines that ran `dedup` without any metainfo upstream now get a clear validator error pointing at `media_type` instead of silently doing nothing useful.
- **All source plugins now populate `Fields["title"]`** with their raw entry name ([#184](https://github.com/brunoga/pipeliner/pull/184)). Previously `jackett`, `trakt_list`, `tvdb_favorites`, and `html` only set the `e.Title` struct field, so a bare `input → print` pipeline rendered an empty title. The tier model is documented in the user guide: sources fill `title` with raw → `metainfo_file` cleans it → external providers replace it with the canonical name.

### Deprecated

- **`movie_title` is deprecated** in favor of `title` ([#187](https://github.com/brunoga/pipeliner/pull/187)). Every plugin that wrote `movie_title` also wrote the same value to `title`, so the field is redundant. The validator now warns on any static reference to `movie_title`. **Writes are intentionally preserved for now** so existing notify templates referencing `{{index .Fields "movie_title"}}` keep rendering — removal is a future change.

### Fixed

- **`metainfo_torrent` and `metainfo_magnet` no longer clobber a canonical title** ([#184](https://github.com/brunoga/pipeliner/pull/184)). The `.torrent` display name (essentially the raw release filename) was overwriting the cleaned title set by `metainfo_file` or the canonical title set by `metainfo_tmdb` / `metainfo_tvdb` / `metainfo_trakt`. A movie notify template referencing `{{index .Fields "title"}}` ended up rendering the full release string instead of the expected `"Superman"`. Both plugins now only populate `title` when no title has been set yet — they don't have anything better than the raw source title, so they never clobber upstream cleaning or enrichment.
- **Visual-editor undo no longer shifts pipeline nodes down** ([#191](https://github.com/brunoga/pipeliner/pull/191)). The undo path was calling `initLayout` on a snapshot whose nodes held absolute y, but `initLayout`'s stored-position branch treats stored y as relative and adds `_regionY`. Each undo shifted nodes down by `_regionY` pixels and the shift compounded across multiple undos and later pipelines. The fix subtracts `_regionY` from the restored snapshot before letting `initLayout` add it back.

### Build

- Bump `modernc.org/sqlite` to the latest patch in the `all-go-deps` group ([#186](https://github.com/brunoga/pipeliner/pull/186)).

[1.2.1]: https://github.com/brunoga/pipeliner/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/brunoga/pipeliner/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/brunoga/pipeliner/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/brunoga/pipeliner/compare/v1.0.0...v1.1.0
