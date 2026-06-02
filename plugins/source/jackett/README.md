# jackett

Queries Jackett indexer proxies via the Torznab API. Works in two modes:

- **Passive source** — use `input("jackett", …)` to fetch recent results from indexers on each run, like an RSS feed.
- **Search backend** — use inside `discover.search` to actively search by title.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url` | yes | — | Jackett base URL (e.g. `http://localhost:9117`) |
| `api_key` | yes | — | Jackett API key (found in the Jackett web UI) |
| `indexers` | no | `["all"]` | Indexer IDs to query; Jackett aggregates results server-side |
| `categories` | no | — | Torznab category codes to filter (see table below) |
| `query` | no | `""` | Static search query for passive-source mode; empty returns recent results |
| `limit` | no | — | Maximum number of results |
| `timeout` | no | `60s` | HTTP request timeout |

## Fields set on entry

Fields marked **always** are set on every entry. All others depend on whether the indexer includes the corresponding Torznab attribute.

| Field | Always | Description |
|-------|--------|-------------|
| `source` | ✓ | Origin in the form `jackett:<indexer>` (e.g. `jackett:1337x`) |
| `torrent_link_type` | ✓ | `"torrent"` or `"magnet"` |
| `torrent_seeds` | | Seeder count |
| `torrent_leechers` | | Leecher count |
| `torrent_file_size` | | Total size in bytes |
| `torrent_info_hash` | | SHA-1 info hash (lowercase hex) |
| `torrent_grabs` | | Number of times downloaded from the indexer |
| `published_date` | | Publication date (`publishdate` attr, falls back to RSS `pubDate`) |
| `video_year` | | Release year |
| `series_season` | | Season number |
| `series_episode` | | Episode number |
| `jackett_category` | | Torznab category code |
| `jackett_imdb_id` | | Raw IMDb ID from Jackett (e.g. `tt0903747`, same pattern as `trakt_imdb_id`) |
| `jackett_tvdb_id` | | Raw TVDB ID from Jackett (use with `metainfo_tvdb` for fast by-ID lookup) |
| `jackett_tmdb_id` | | Raw TMDb ID from Jackett (same pattern as `trakt_tmdb_id`) |
| `jackett_dl_factor` | | Download volume factor: `0.0` = freeleech, `0.5` = half-leech, `1.0` = normal |
| `jackett_ul_factor` | | Upload volume factor (private trackers) |

## Common Torznab categories

| Code | Category |
|------|----------|
| 2000 | Movies |
| 2010 | Movies / HD |
| 5000 | TV |
| 5030 | TV / HD |
| 5040 | TV / SD |
| 5045 | TV / UHD |

## Example — passive source

```python
src  = input("jackett", url="http://localhost:9117",
             api_key=env("JACKETT_KEY"), categories=["5040", "5045"])
seen = process("seen",          upstream=src)
meta = process("metainfo_file", upstream=seen)   # required for premiere downstream
prem = process("premiere",      upstream=meta, quality="720p+ webrip+")
best = process("dedup",         upstream=prem)
output("transmission", upstream=best, host="localhost")
pipeline("new-shows", schedule="1h")
```

## Example — search backend for discover

```python
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"), type="movies", list="watchlist")
results   = process("discover", upstream=watchlist,
    search=[{"name": "jackett", "url": "http://localhost:9117",
             "api_key": env("JACKETT_KEY"), "categories": ["2000"]}],
    interval="12h")
output("qbittorrent", upstream=results, host="localhost")
pipeline("movie-discover", schedule="2h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| IsSearchPlugin | `true` |
| Produces | `source`, `torrent_link_type` |
| MayProduce | `torrent_seeds`, `torrent_leechers`, `torrent_file_size`, `torrent_info_hash`, `torrent_grabs`, `published_date`, `video_year`, `series_season`, `series_episode`, `jackett_category`, `jackett_imdb_id`, `jackett_tvdb_id`, `jackett_tmdb_id`, `jackett_dl_factor`, `jackett_ul_factor` |
| Requires | — |

## Notes

- All configured indexers are queried concurrently; results are merged and deduplicated. When the same info hash appears from multiple indexers, the entry with more seeds is kept.
- Transient failures (network errors, HTTP 5xx) are retried up to 3 times with backoff. A broken indexer is logged and skipped — it does not abort results from other indexers.
- Category filtering is applied server-side by Jackett.
- **Search-backend mode (inside `discover.search`)** uses typed Torznab parameters when the upstream entry carries the corresponding hint fields: `media_type` picks `t=movie` vs `t=tvsearch` (otherwise generic `t=search`), `video_year` → `year=`, `video_imdb_id` / `jackett_imdb_id` / `trakt_imdb_id` → `imdbid=` (numeric portion only — the `tt` prefix is stripped), `jackett_tmdb_id` / `trakt_tmdb_id` → `tmdbid=`, `jackett_tvdb_id` / `tvdb_id` → `tvdbid=`, `series_season` → `season=`, `series_episode` → `ep=`. In source mode the configured `query` is sent as a plain `q=` with no extra filters.
- Each indexer's Torznab `t=caps` response is fetched lazily on first use and cached for the lifetime of the pipeliner process. Mode selection (`t=movie` / `t=tvsearch` / `t=search`) and typed parameters honour the caps' `available` and `supportedParams` lists, so indexers that only support generic free-text search (e.g. 3dtorrents) get plain `t=search` queries instead of typed ones they would reject with Torznab error 201. If an indexer's caps lie or are unparseable, the plugin still falls back to `t=search` once on a 201 response.
- `torrent_info_hash` being set makes separate `metainfo_torrent` or `metainfo_magnet` fetches unnecessary for hash-based operations.
- `jackett_tvdb_id` and `jackett_tmdb_id` follow the same convention as `trakt_tvdb_id` / `trakt_tmdb_id` — downstream metainfo plugins can use them for faster by-ID lookups instead of title searches.
- `jackett_dl_factor = 0.0` means freeleech (no download credit consumed) on private trackers.
