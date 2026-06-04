# metainfo_bluray

Enriches movie entries with metadata scraped from [Blu-ray.com](https://www.blu-ray.com/): release date, studio, codec, aspect ratio, and — most usefully — `bluray_3d_release`, a boolean that tells you whether a movie actually has a real 3D Blu-ray release in the catalog. This is the answer to "is the 3D label on this torrent real or a fake/upscale?"

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `country` | enum | no | `us` | Blu-ray.com locale (`us`, `uk`, `ca`, `au`, `de`, `fr`, `kr`) |
| `cache_ttl` | duration | no | `168h` | How long to cache release-detail pages |
| `cache_index_ttl` | duration | no | `720h` | How long to cache the title-to-ID index |
| `cache_negative_ttl` | duration | no | `168h` | How long to remember "we searched, found nothing" |
| `request_interval` | duration | no | `1s` | Minimum gap between HTTP requests |
| `user_agent` | string | no | generic Chrome UA | Custom `User-Agent` |

## Fields set on entry

All fields are written only when the Blu-ray.com lookup succeeds. The entry is otherwise left untouched (no errors).

| Field | Type | Description |
|-------|------|-------------|
| `enriched` | bool | `true` — Blu-ray.com successfully resolved this entry |
| `bluray_id` | string | Numeric release ID, e.g. `26954` |
| `bluray_url` | string | Canonical detail-page URL |
| `bluray_format` | string | `BD`, `UHD`, `BD3D`, or `DVD` |
| `bluray_studio` | string | Distributing studio, e.g. `20th Century Fox` |
| `bluray_country` | string | Release locale, e.g. `United States` |
| `bluray_year` | int | Production year |
| `bluray_release_date` | string | `YYYY-MM-DD` |
| `bluray_runtime_minutes` | int | Runtime in minutes |
| `bluray_codec` | string | Video codec, e.g. `MPEG-4 MVC` (the 3D codec), `HEVC`, `AVC` |
| `bluray_resolution` | string | e.g. `1080p`, `2160p` |
| `bluray_aspect_ratio` | string | e.g. `1.78:1` |
| `bluray_edition` | string | Edition tag from the page title, e.g. `Limited 3D Edition` |
| `bluray_3d_release` | bool | **The movie has a real 3D Blu-ray release in the catalog.** |
| `bluray_is_3d_edition` | bool | This specific release IS the 3D edition (format=BD3D or codec=MPEG-4 MVC) |

`bluray_3d_release` and `video_is_3d` mean different things:

- `video_is_3d` (set by `metainfo_file`) — the release **file name** parses as 3D (e.g. `Avatar.3D.1080p.BluRay.x264`).
- `bluray_3d_release` (set here) — Blu-ray.com has a real 3D release for this movie. False here when `video_is_3d` is true is a strong signal the rip is fake/upscaled.

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| MayProduce | all `bluray_*` fields, plus `enriched` |
| Requires | — |

## Lookup strategy

The plugin resolves entries in this order:

1. **By ID** — if the entry already carries `bluray_id`, fetch the detail page directly.
2. **From the local index** — look up the normalised title (with format tokens like " 3D" / " 4K" stripped) in the SQLite-backed `cache_bluray_index`. The `bluray_releases` source plugin populates this from monthly calendar scrapes, so common-case titles never trigger a search.
3. **Negative cache** — if a recent search returned zero hits for this title, skip.
4. **Live search** — fall back to `/search/` on Blu-ray.com. Successful results are written through to `cache_bluray_index`; empty results are written to `cache_bluray_search_neg`. Subsequent runs hit the cache.

Both caches use the SQLite store at `pipeliner.db` and persist across restarts.

## Example

```python
src    = input("rss",             url="https://example.com/feed")
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)
req    = process("require",       upstream=meta, fields=["title", "_quality"])
bluray = process("metainfo_bluray", upstream=req)

# Drop anything that claims to be 3D but isn't a real 3D release.
real_only = process("condition", upstream=bluray,
    reject="video_is_3d == true and bluray_3d_release != true")

output("transmission", upstream=real_only, host="localhost")
pipeline("real-3d-only", schedule="1h")
```

## Notes

- The plugin is most effective when paired with `bluray_releases` on a weekly schedule, which warms `cache_bluray_index` so detail lookups skip the search step.
- Blu-ray.com's `robots.txt` disallows `/search/`; this plugin uses it anyway and relies on aggressive caching to keep request volume low.
- Failures (network errors, 404s, unparseable pages) are logged at WARN and the entry is passed through unchanged. The pipeline does not fail.
