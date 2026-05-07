# movies

Accepts movies from a configured title list. Parses the movie title, year, quality, and 3D format from the entry title, matches with fuzzy matching, and enforces an optional quality floor. A re-download of an already-seen movie is accepted when the new copy is strictly better quality, or when it is a PROPER/REPACK that is not a quality downgrade.

**Multiple quality variants** of the same movie (from different sources or input feeds) are all accepted so the task engine's automatic deduplication can pick the best copy. The download history is updated in the Learn phase — only the winning copy is recorded.

**3D and non-3D versions are tracked independently.** If both a 3D and a non-3D copy of the same movie match, both are downloaded — they do not compete with each other.

The movie list can be provided statically via `static`, dynamically via `from` (a list of input plugins whose entry titles are used as movie titles), or both. Dynamic results are cached for the configured `ttl` so external APIs are not called on every pipeline run.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `static` | string or list | conditional | — | Static movie titles to accept |
| `from` | list | conditional | — | Input plugin configs whose entry titles supplement the movie list |
| `ttl` | string | no | `1h` | How long to cache the dynamic list fetched via `from` |
| `quality` | string | no | — | Minimum quality floor (e.g. `720p+`, `1080p webrip+`). See [`quality`](../quality/README.md) for syntax. |

At least one of `static` or `from` is required.

### `from` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config:

```yaml
from:
  - name: trakt_list
    client_id: YOUR_CLIENT_ID
    access_token: YOUR_ACCESS_TOKEN
    type: movies
    list: watchlist
```

## Fields set on each entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Matched canonical movie title |
| `video_year` | int | Parsed release year |
| `video_quality` | string | Human-readable quality string, e.g. `1080p BluRay H.265` (absent if unparseable) |
| `video_resolution` | string | Resolution tag, e.g. `1080p` (absent if unparseable) |
| `video_source` | string | Source tag, e.g. `BluRay` (absent if unparseable) |
| `video_is_3d` | bool | `true` when any 3D format marker is detected |

## 3D quality

3D format is a ranked quality dimension. When two 3D copies of the same movie are compared, the 3D format rank takes precedence over resolution, source, and all other dimensions; those become tie-breakers.

| Rank | Format | Detected markers |
|------|--------|-----------------|
| Lowest | Half | `3D`, `HSBS`, `H-SBS`, `HALF-SBS`, `HOU`, `H-OU`, `HALF-OU` |
| Middle | Full | `SBS`, `FSBS`, `F-SBS`, `FULL-SBS`, `OU`, `FOU`, `F-OU`, `FULL-OU` |
| Highest | BD | `BD3D` |

Plain `3D` without a subtype is treated as Half (most common encoding for generic 3D releases).

The 3D format is included in the `video_quality` string (e.g. `BD3D 1080p BluRay H.265`).

3D and non-3D versions of the same movie are tracked independently — both are downloaded if both match.

Filtering out 3D releases entirely:

```yaml
condition:
  reject: 'video_is_3d == true'
```

## Debug logging

Run with `--log-level debug --log-plugin movies` to see:
- Which titles are loaded from `from` sources (cache hit or live fetch)
- Why individual entries are skipped (title not parseable, no match in list)

## Example — static list

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/rss/movies"
    movies:
      quality: 1080p+
      static:
        - Inception
        - Interstellar
        - "The Dark Knight"
    deluge:
      host: localhost
```

## Example — dynamic list from Trakt watchlist

```yaml
tasks:
  movies-watchlist:
    rss:
      url: "https://example.com/rss/movies"
    movies:
      quality: 1080p+
      ttl: 4h
      from:
        - name: trakt_list
          client_id: YOUR_TRAKT_CLIENT_ID
          access_token: YOUR_TRAKT_ACCESS_TOKEN
          type: movies
          list: watchlist
    condition:
      reject: 'video_is_3d == true'    # exclude all 3D releases
    deluge:
      host: localhost
```

To accept only BD3D quality or better among 3D releases (and still download non-3D copies independently), use the `video_quality` field which includes the 3D format string:

```yaml
condition:
  rules:
    # reject 3D releases that are not BD3D
    - reject: 'video_is_3d == true and not contains(video_quality, "BD3D")'
```

## Notes

- Download history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
- `quality:` specifies a **minimum floor** — use `720p+` or `1080p+` syntax. A bare `720p` means exactly 720p.
