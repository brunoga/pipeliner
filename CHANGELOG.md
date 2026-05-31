# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[1.1.1]: https://github.com/brunoga/pipeliner/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/brunoga/pipeliner/compare/v1.0.0...v1.1.0
