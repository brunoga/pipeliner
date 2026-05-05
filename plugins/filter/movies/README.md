# movies

Accepts movies from a configured title list. Parses the movie title and year from the entry title, matches with fuzzy matching, and enforces an optional quality floor. Accepts a re-download when a proper or repack with strictly better quality is available.

**Multiple quality variants** of the same movie (from different sources or input feeds) are all accepted so the task engine's automatic deduplication can pick the best copy. The download history is updated in the Learn phase — only the winning copy is recorded.

The movie list can be provided statically via `movies`, dynamically via `from` (a list of input plugins whose entry titles are used as movie titles), or both. Dynamic results are cached for the configured `ttl` so external APIs are not called on every pipeline run.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `movies` | string or list | conditional | — | Static movie titles to accept |
| `from` | list | conditional | — | Input plugin configs whose entry titles supplement the movie list |
| `ttl` | string | no | `1h` | How long to cache the dynamic list fetched via `from` |
| `quality` | string | no | — | Minimum quality spec (e.g. `720p`, `1080p bluray`) |

At least one of `movies` or `from` is required.

### `from` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config:

```yaml
from:
  - name: input_trakt
    client_id: YOUR_CLIENT_ID
    access_token: YOUR_ACCESS_TOKEN
    type: movies
    list: watchlist
```

## Fields set on each entry

| Field | Description |
|-------|-------------|
| `movie_title` | Matched canonical movie title |
| `movie_year` | Parsed release year |

## Example — static list

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/rss/movies"
    seen:
    movies:
      quality: 1080p
      movies:
        - Inception
        - Interstellar
        - "The Dark Knight"
```

## Example — dynamic list from Trakt watchlist

```yaml
tasks:
  movies-watchlist:
    rss:
      url: "https://example.com/rss/movies"
    seen:
    movies:
      quality: 1080p
      ttl: 4h
      from:
        - name: input_trakt
          client_id: YOUR_TRAKT_CLIENT_ID
          access_token: YOUR_TRAKT_ACCESS_TOKEN
          type: movies
          list: watchlist
```

## Example — combined static and dynamic

```yaml
tasks:
  movies-combined:
    rss:
      url: "https://example.com/rss/movies"
    movies:
      movies:
        - Oppenheimer       # always included regardless of watchlist
      from:
        - name: input_trakt
          client_id: YOUR_CLIENT_ID
          access_token: YOUR_ACCESS_TOKEN
          type: movies
          list: watchlist
```

## Notes

- Download history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
