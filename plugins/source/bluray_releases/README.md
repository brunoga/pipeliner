# bluray_releases

Scrapes the [Blu-ray.com](https://www.blu-ray.com/movies/releasedates.php) release calendar and emits one entry per release. Also implements `SearchPlugin` so it can be used as a search backend inside `discover(search=[...])` and as a title list inside `series.list=[...]` / `movies.list=[...]`.

Calendar passes warm the local title-to-ID index that `metainfo_bluray` (and the plugin's own `Search` method) read from. The shared index means repeated runs almost never hit the search endpoint.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `country` | enum | no | `us` | Blu-ray.com locale (`us`, `uk`, `ca`, `au`, `de`, `fr`, `kr`) |
| `months` | int | no | `1` | Number of months back from current month to scan |
| `from_year` / `from_month` | int | no | — | Explicit start month (overrides `months`) |
| `to_year` / `to_month` | int | no | current | Explicit end month |
| `formats` | list | no | `[BD, UHD, BD3D]` | Subset of formats to emit (aliases: `3D`=`BD3D`, `4K`=`UHD`) |
| `cache_ttl` | duration | no | `720h` | Title-index TTL (30 days) |
| `cache_detail_ttl` | duration | no | `168h` | Release-detail TTL (7 days) |
| `cache_negative_ttl` | duration | no | `168h` | Search-miss TTL (7 days) |
| `request_interval` | duration | no | `1s` | Minimum gap between HTTP requests |
| `user_agent` | string | no | generic Chrome UA | Custom `User-Agent` |

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Title as listed on the calendar (may include " 3D" / " 4K" suffix) |
| `movie_title` | string | Cleaned title with format suffix stripped |
| `media_type` | string | `"movie"` |
| `source` | string | `"bluray_releases:<format>"` |
| `bluray_id` | string | Numeric release ID |
| `bluray_url` | string | Canonical detail-page URL |
| `bluray_format` | string | `BD`, `UHD`, `BD3D`, or `DVD` |
| `bluray_studio` | string | Distributing studio (when present in calendar row) |
| `bluray_year` | int | Production year |
| `video_year` | int | Same as `bluray_year` (mirror for downstream filters) |
| `bluray_release_date` | string | `YYYY-MM-DD` |
| `bluray_edition` | string | Edition tag, e.g. `"Limited Edition"` |
| `bluray_3d_release` | bool | true on every sibling when any release for this title is BD3D |
| `bluray_is_3d_edition` | bool | true when this specific release has format=BD3D |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `source`, `media_type`, `bluray_id`, `bluray_url`, `bluray_format` |
| MayProduce | `video_year`, `movie_title`, `bluray_studio`, `bluray_year`, `bluray_release_date`, `bluray_edition`, `bluray_3d_release`, `bluray_is_3d_edition` |
| IsSearchPlugin | `true` |
| IsListPlugin | `true` |

## Examples

### Standalone — pull this month's 3D Blu-ray releases

```python
src   = input("bluray_releases", months=1, formats=["BD3D"])
output("print", upstream=src)
pipeline("new-3d-blurays", schedule="168h")
```

### As a search backend for discover()

```python
# discover() asks bluray_releases.Search() for each title in `titles`.
disc = process("discover", titles=["Inception", "Avatar"],
               search=[{"name": "bluray_releases"}])
output("print", upstream=disc)
pipeline("discover-bluray", schedule="6h")
```

### Index warmer + downstream consumer

```python
# Pipeline 1: weekly index refresh — side effect populates cache_bluray_index.
warm = input("bluray_releases", months=2)
output("print", upstream=warm)
pipeline("bluray-warmer", schedule="168h")

# Pipeline 2: enrich an existing source using the warmed index. metainfo_bluray
# will read from the cache rather than hitting /search/.
src    = input("rss",             url="https://example.com/feed")
meta   = process("metainfo_file", upstream=src)
bluray = process("metainfo_bluray", upstream=meta)
output("transmission", upstream=bluray, host="localhost")
pipeline("rss-with-bluray-meta", schedule="1h")
```

## Notes

- The calendar page embeds the full month's release list as a JavaScript data dump, which is far more stable to parse than the surrounding DOM. Parsing is therefore tolerant to the site's frequent layout tweaks.
- Blu-ray.com's `robots.txt` disallows `/search/`; this plugin uses it as a fallback inside `Search()` and relies on caching to keep request volume low. `Generate()` only ever hits `/movies/releasedates.php`, which is allowed.
- All caches are persisted to the shared `pipeliner.db` so cold starts replay warmly.
