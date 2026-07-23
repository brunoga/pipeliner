# series_gaps

Turns tracked shows into search queries for the episodes you are missing. The series tracker knows what you have; TheTVDB knows what exists — `series_gaps` emits the diff, one entry per missing aired episode, shaped so the output feeds straight into [`discover`](../discover/) search backends.

Upstream entries are shows (from `series_tracker`, `tvdb_favorites`, `trakt_list`, …). For each show the plugin:

1. Resolves the TVDB series — by the entry's `tvdb_id` field when present, else by name search. Lookups are TTL-cached in store buckets.
2. Fetches the episode list (also TTL-cached) and keeps only **aired** episodes: air date strictly in the past, season-0 specials excluded unless `include_specials=true`, undated episodes ignored.
3. Diffs against the series tracker's download records and emits one entry per missing episode — or one **season-pack** entry when most of a season is missing (see below).

Lookup failures skip the show (with a warning) rather than aborting the run. Shows deactivated in the tracker (`series_inactive`) are skipped unless `include_inactive=true`.

Like `discover`, this is a `ReplacesUpstream` processor: the upstream show entries are consumed as context, and the emitted gap entries have their own URLs and lifetimes.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `cache_ttl` | duration | no | `24h` | How long to cache TVDB lookups |
| `include_specials` | bool | no | `false` | Consider season-0 specials as gap candidates |
| `include_inactive` | bool | no | `false` | Also scan shows deactivated in the series tracker |
| `pack_threshold` | float 0–1 | no | `0.5` | Missing fraction above which a season emits one season-pack query instead of per-episode queries |
| `max_per_run` | int | no | `30` | Cap on emitted entries per run (`0` = unlimited) |

## Emitted entry shape

Per-episode entry:

| Field | Example |
|-------|---------|
| title | `Breaking Bad S02E05` (searchable query form) |
| URL | `pipeliner://gap/breaking%20bad/S02E05` (synthetic, stable across runs) |
| `series_name` | `breaking bad` (normalized tracker key) |
| `series_season` | `2` |
| `series_episode` | `5` |
| `series_episode_id` | `S02E05` (canonical tracker form; `EP001` for season-0 specials) |
| `media_type` | `series` |
| `tvdb_id` | `81189` |
| `source` | `series_gaps:tvdb` |

Season-pack entry: title `Breaking Bad S02`, URL `pipeliner://gap/breaking%20bad/S02`, `series_season` set, **no** `series_episode`/`series_episode_id`. The codebase has no season-pack tracking semantics: downstream the pack entry is just a plain search query, and season-pack *releases* found for it don't parse as single episodes — a strict `require(fields=["series_episode_id", …])` / `series` chain will drop them. Keep `pack_threshold=1.0` (packs disabled) with such a chain, or relax the chain on a dedicated branch if you want pack grabs. Pack grabs are also not recorded per-episode in the tracker, so the episodes they cover remain "missing" to future gap scans until tracked by other means.

## Season packs

Per season, the plugin compares `missing / aired`. When that fraction **strictly exceeds** `pack_threshold`, the whole season collapses into one pack query. Boundaries:

- `pack_threshold=0.5`, 2 of 4 missing (exactly 0.5) → per-episode entries.
- `pack_threshold=0.5`, 3 of 4 missing (0.75) → one pack entry.
- `pack_threshold=0` → any gap at all becomes a pack.
- `pack_threshold=1` → packs disabled (even a fully-missing season stays per-episode).

## Per-run cap and resume cursor

Candidates are ordered deterministically (show, season, episode) and at most `max_per_run` are emitted per run. The position of the last emitted candidate is persisted in a per-pipeline store bucket (`series_gaps:<pipeline>`); the next run resumes right after it and wraps around at the end of the list, so a large backlog drains across runs instead of re-searching the same first 30 gaps forever. When the total candidate count fits under the cap, every run emits the full list (downstream `seen`/`discover` cooldowns keep that cheap). Dry-run reads the cursor but never advances it, so a dry-run previews exactly what the next real run would emit. Each run logs `emitted N of M candidate gaps`.

## Example

```python
shows = input("series_tracker")
lc    = process("series_lifecycle", upstream=shows, api_key=env("TVDB_API_KEY"))
gate  = process("condition", upstream=lc, rules=[
    {"accept": 'series_lifecycle == "dormant"'},
    {"reject": "true"},
])
gaps  = process("series_gaps", upstream=gate, api_key=env("TVDB_API_KEY"),
                pack_threshold=1.0, max_per_run=30)
found = process("discover", upstream=gaps, interval="12h",
                search=[{"name": "jackett", "url": env("JACKETT_URL"),
                         "api_key": env("JACKETT_API_KEY"), "indexers": ["all"]}])
```

See [`configs/series-backfill.star`](../../../configs/series-backfill.star) for the full pipeline (seen → metainfo_file → require → quality → series → transmission + notify).

## Caveats

- Date-numbered shows (talk shows tracked by air date, e.g. `2023-11-15`) cannot be matched against TVDB's season/episode numbering, so all their episodes look missing. Gate on `series_lifecycle == "dormant"` and deactivate such shows, or accept the noise.
- The gap diff is tracker-truth, not disk-truth: episodes downloaded outside pipeliner count as missing until library awareness (roadmap §5) lands.
