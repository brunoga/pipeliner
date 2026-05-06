# movies

Accepts movies from a configured title list. Parses the movie title, year, quality, and 3D format from the entry title, matches with fuzzy matching, and enforces an optional quality floor. Accepts a re-download when a proper or repack with strictly better quality is available.

**Multiple quality variants** of the same movie (from different sources or input feeds) are all accepted so the task engine's automatic deduplication can pick the best copy. The download history is updated in the Learn phase — only the winning copy is recorded.

The movie list can be provided statically via `movies`, dynamically via `from` (a list of input plugins whose entry titles are used as movie titles), or both. Dynamic results are cached for the configured `ttl` so external APIs are not called on every pipeline run.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `movies` | string or list | conditional | — | Static movie titles to accept |
| `from` | list | conditional | — | Input plugin configs whose entry titles supplement the movie list |
| `ttl` | string | no | `1h` | How long to cache the dynamic list fetched via `from` |
| `quality` | string | no | — | Minimum quality floor (e.g. `720p+`, `1080p webrip+`). See [`quality`](../quality/README.md) for syntax. |

At least one of `movies` or `from` is required.

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
| `movie_title` | string | Matched canonical movie title |
| `movie_year` | int | Parsed release year |
| `movie_quality` | string | Human-readable quality string, e.g. `1080p BluRay H.265` (absent if unparseable) |
| `movie_3d` | bool | `true` when a 3D format marker is detected in the title (`3D`, `SBS`, `HOU`, `BD3D`, etc.) |

## 3D detection

The following markers in the release title set `movie_3d=true`:

`3D`, `SBS`, `HSBS`, `H-SBS`, `HALF-SBS`, `FSBS`, `F-SBS`, `FULL-SBS`, `OU`, `HOU`, `H-OU`, `HALF-OU`, `FOU`, `F-OU`, `FULL-OU`, `BD3D`

Filtering out 3D releases via `condition`:

```yaml
condition:
  reject: 'movie_3d == true'
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
      movies:
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
      reject: 'movie_3d == true'
    deluge:
      host: localhost
```

## Notes

- Download history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
- `quality:` specifies a **minimum floor** — use `720p+` or `1080p+` syntax. A bare `720p` means exactly 720p.
