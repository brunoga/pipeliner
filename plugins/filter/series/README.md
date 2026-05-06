# series

Accepts episodes of configured TV shows. Parses the episode identifier from the entry title, matches the series name with fuzzy matching, and enforces optional quality and ordering constraints.

**Multiple quality variants** of the same episode (from different sources or input feeds) are all accepted so the task engine's automatic deduplication can pick the best copy. The download history is updated in the Learn phase — only the winning copy is recorded.

The show list can be provided statically via `shows`, dynamically via `from` (a list of input plugins whose entry titles are used as show names), or both. Dynamic results are cached for the configured `ttl` so external APIs are not called on every pipeline run.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `shows` | string or list | conditional | — | Static show names to accept |
| `from` | list | conditional | — | Input plugin configs whose entry titles supplement the show list |
| `ttl` | string | no | `1h` | How long to cache the dynamic list fetched via `from` |
| `tracking` | string | no | `strict` | Episode ordering mode: `strict`, `backfill`, or `all` |
| `quality` | string | no | — | Minimum quality spec (e.g. `720p`, `1080p bluray`) |

At least one of `shows` or `from` is required.

### Tracking modes

| Mode | Behaviour |
|------|-----------|
| `strict` | Accept only the next expected episode; reject gaps greater than one ahead of the latest downloaded |
| `backfill` | Accept any episode not yet downloaded, including older ones |
| `all` | Accept every episode regardless of prior download history |

### `from` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config:

```yaml
from:
  - trakt_list                 # name only, uses defaults
  - name: tvdb_favorites
    api_key: YOUR_KEY
    user_pin: YOUR_PIN
```

## Fields set on each entry

| Field | Description |
|-------|-------------|
| `series_name` | Matched canonical show name |
| `series_season` | Season number |
| `series_episode` | Episode number |
| `series_episode_id` | Episode identifier string (e.g. `S02E05`) |

## Example — static list

```yaml
tasks:
  tv:
    rss:
      url: "https://example.com/feed"
    seen:
    series:
      tracking: strict
      quality: 720p
      shows:
        - "Breaking Bad"
        - "Better Call Saul"
        - "The Wire"
```

## Example — dynamic list from Trakt watchlist

```yaml
tasks:
  tv-watchlist:
    rss:
      url: "https://example.com/feed"
    seen:
    series:
      tracking: strict
      quality: 720p
      ttl: 2h
      from:
        - name: trakt_list
          client_id: YOUR_TRAKT_CLIENT_ID
          access_token: YOUR_TRAKT_ACCESS_TOKEN
          type: shows
          list: watchlist
```

## Example — dynamic list from TheTVDB favorites

```yaml
tasks:
  tv-favorites:
    rss:
      url: "https://example.com/feed"
    seen:
    series:
      tracking: strict
      quality: 720p
      from:
        - name: tvdb_favorites
          api_key: YOUR_TVDB_API_KEY
          user_pin: YOUR_TVDB_USER_PIN
```

## Example — combined static and dynamic

```yaml
tasks:
  tv-combined:
    rss:
      url: "https://example.com/feed"
    series:
      shows:
        - "Severance"      # always included regardless of watchlist
      from:
        - name: trakt_list
          client_id: YOUR_CLIENT_ID
          access_token: YOUR_ACCESS_TOKEN
          type: shows
          list: watchlist
```

## Notes

- Episode history and dynamic list cache are stored in `pipeliner.db` in the same directory as the config file.
