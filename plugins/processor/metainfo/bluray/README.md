# metainfo_bluray

Enriches movie entries with metadata parsed from [Blu-ray.com](https://www.blu-ray.com/): release date, studio, codec, aspect ratio, and — most usefully — `bluray_3d_release`, a boolean that tells you whether a movie actually has a real 3D Blu-ray release in the catalog. This is the answer to "is the 3D label on this torrent real or a fake/upscale?"

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `country` | enum | no | `us` | Blu-ray.com locale (`us`, `uk`, `ca`, `au`, `de`, `fr`, `kr`, `jp`, `it`, `es`, `all`, etc.) |
| `cache_ttl` | duration | no | `168h` | How long to cache release-detail pages |
| `cache_index_ttl` | duration | no | `720h` | How long to cache the title-to-ID index |
| `cache_negative_ttl` | duration | no | `168h` | How long to remember "we searched, found nothing" |
| `request_interval` | duration | no | `1s` | Minimum gap between HTTP requests |
| `user_agent` | string | no | Chrome 131 UA | Custom `User-Agent` |
| `details` | bool | no | `false` | Fetch the release page (codec, audio, ratings) even when the embedded 3D catalog answers |
| `offline` | bool | no | `false` | Never contact Blu-ray.com; answer from the embedded 3D catalog only |

## Fields set on entry

Fields are written only when the lookup succeeds, from the embedded catalog or Blu-ray.com. The one exception is `bluray_3d_release=false`, set when the embedded catalog is sure no 3D Blu-ray exists. When the plugin cannot tell (a newer film offline, or a failed lookup), the entry is left untouched (no errors).

| Field | Type | Description |
|-------|------|-------------|
| `enriched` | bool | `true` — Blu-ray.com successfully resolved this entry |
| `bluray_id` | string | Numeric release ID, e.g. `26954` |
| `bluray_parent_id` | string | Master theatrical movie ID, e.g. `128351` |
| `bluray_url` | string | Canonical detail-page URL |
| `bluray_format` | string | `BD`, `UHD`, `BD3D`, or `DVD` |
| `bluray_studio` | string | Distributing studio, e.g. `20th Century Fox` |
| `bluray_country` | string | Release locale, e.g. `United States` |
| `bluray_year` | int | Production year |
| `bluray_release_date` | string | `YYYY-MM-DD` |
| `bluray_runtime_minutes` | int | Runtime in minutes |
| `bluray_rated` | string | Content rating, e.g. `Rated PG-13` |
| `bluray_codec` | string | Video codec, e.g. `MPEG-4 MVC` (the 3D codec), `HEVC`, `AVC` |
| `bluray_resolution` | string | e.g. `1080p`, `2160p` |
| `bluray_aspect_ratio` | string | e.g. `1.78:1` |
| `bluray_hdr` | string | HDR specification, e.g. `HDR10, Dolby Vision` |
| `bluray_edition` | string | Edition tag from the page title, e.g. `Limited 3D Edition` |
| `bluray_audio` | string_list | Audio track descriptions |
| `bluray_subtitles` | string_list | Subtitle languages |
| `bluray_playback` | string | Region playback compatibility, e.g. `2K Blu-ray: Region A (locked)` |
| `bluray_cover_url` | string | Cover image URL |
| `bluray_rating` | float | Movie score out of 10 |
| `bluray_review_rating` | float | Official review overall score out of 5 |
| `bluray_user_rating` | float | User overall score out of 5 |
| `bluray_native_3d` | bool | True when theatrical technical details state `Native 3D` |
| `bluray_converted_3d` | bool | True when theatrical technical details state `Converted 3D` |
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

**Embedded catalog first.** The plugin carries a snapshot of every 3D Blu-ray release Blu-ray.com lists worldwide (14,163 releases), treated as complete for films released before 2026. For those films it answers with no request at all: a listed film gets `bluray_3d_release=true` and the catalog release's ID, URL, studio, country, year, date and edition; an unlisted one gets `bluray_3d_release=false`. An entry that already carries a `bluray_id` the catalog holds is answered for that release. Titles match ignoring punctuation, a leading "The", an "Episode N -" prefix and the "4K + 3D" / "w/ ..." format suffixes, and number words read as digits ("The Fantastic 4" for "The Fantastic Four").

Everything else (newer films, films with no known year, and every film when `details=True`) goes through the live lookup below, unless `offline=True`, which leaves them unset:

1. **By ID** — if the entry already carries `bluray_id`, fetch the detail page directly.
2. **From the local index** — look up the normalised title (with format tokens like " 3D" / " 4K" stripped) in the SQLite-backed `cache_bluray_index`. The `bluray_releases` source plugin populates this from monthly calendar passes, so common-case titles never trigger a search.
3. **Negative cache** — if a recent search returned zero hits for this title, skip.
4. **Live search + embedded 3D catalog** — fall back to `/search/` on Blu-ray.com and merge any matches from the embedded worldwide 3D Blu-ray catalog (14,163 releases). For current/future titles (`year >= 2026` or unknown year) not yet in the embedded snapshot, the plugin also queries Blu-ray.com's theatrical master release list (`/products/menu_ajax.php`) to discover newly announced 3D editions. Successful results are written through to `cache_bluray_index`; empty results are written to `cache_bluray_search_neg`. Subsequent runs hit the cache.

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
- Failures (network errors, 404s, unparseable pages) are logged at WARN and the entry is passed through unchanged. The pipeline does not fail.
