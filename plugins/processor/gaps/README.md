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

- A show the media server files under a different title from TheTVDB's is matched by the id where the server publishes one; without an id it is matched by name, and a mismatch makes it contribute nothing (`from_first_owned`) or look entirely missing (`all`).
- Date-numbered shows (talk shows tracked by air date, e.g. `2023-11-15`) cannot be matched against TVDB's season/episode numbering, so all their episodes look missing. Gate on `series_lifecycle == "dormant"` and deactivate such shows, or accept the noise.
- Without `backend`, the gap diff is tracker-truth rather than disk-truth: episodes acquired outside pipeliner count as missing, and an episode you delete never comes back. Set `backend="plex"` (or `jellyfin"`) to diff against the library instead.

## Matching the library to TheTVDB

A show is looked up in the library by the **TheTVDB id** the server publishes, falling back to the normalized title when the server exposes none.

The id matters because a title is not stable identity. TheTVDB renames series, and a media server may disambiguate a remake with a year the provider does not use — a library holding `Brothers (2026)` against a provider calling it `Brothers` normalizes to `brothers 2026` and `brothers`, which do not match. The failure is silent and costly in both directions: with `seasons="from_first_owned"` the show finds no season floor and contributes nothing, and with `seasons="all"` every episode you own looks missing and the whole run is proposed for download.

Plex supplies ids from each library's show listing (`?type=2&includeGuids=1`) and Jellyfin from its series listing (`Fields=ProviderIds`) — one extra request per library, against shows rather than episodes. A server exposing no ids degrades to title matching exactly as before.

## Library-truth mode

With `backend` set, "already have it" means the media server says so, not that
pipeliner once grabbed it. That matters in both directions: episodes you
acquired outside pipeliner stop being proposed, and an episode you **delete**
becomes a gap again — which a tracker record would have blocked forever.

The shared series tracker is deliberately not consulted in this mode. A record
there says pipeliner grabbed the episode at some point, which is not the same
claim as "it is on disk now".

What the tracker was doing usefully — not re-asking for something already in
flight — is covered by a per-task pending set instead. An episode asked for
within `retry_cooldown` is skipped, so the hours between a grab and the server
indexing it don't spend a slot of `max_per_run` or an indexer query on every
run. Unlike a tracker record it expires, so a download that failed, or a file
later removed, comes back.

```python
shows = input("series_tracker")
gaps  = process("series_gaps", upstream=shows, api_key=env("TVDB_API_KEY"),
                backend="plex", sections=["TV Shows"],
                seasons="from_first_owned",
                pack_threshold=1.0, max_per_run=30)
```

Omit `url`/`token` for Plex **account mode**: sign in once on the Tools tab and
every owned server is read, with the token picked up per index build so a
later sign-in needs no restart.

### Which seasons are in scope

| `seasons` | Meaning |
|---|---|
| `all` (default) | Every aired season. Fills a show in completely. |
| `from_first_owned` | Seasons from the earliest one you hold an episode of onwards — **including later seasons you hold nothing of**. |

`from_first_owned` is "finish what I started". Holding only S02E01 puts the rest
of season 2 in scope, puts season 3 in scope in full, and leaves season 1
alone — you evidently did not want it. A show the library has nothing of has no
floor, so none of it is proposed; that is what stops the option backfilling
shows you have never watched.

It needs `backend`, and `pipeliner check` says so rather than waiting for the
first run: the floor is a fact about what is on disk, and answering it from the
tracker would be wrong for exactly the shows the option exists for.
