# discover

Actively searches multiple sources for entries matching a list of titles. Unlike RSS-based inputs that passively receive all items, `discover` iterates a title list, dispatches a search query per title to each configured search plugin, and returns the merged, deduplicated results.

A per-title cooldown (`interval`) prevents redundant searches on successive runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `titles` | list | conditional | — | Static list of title strings to search for |
| `from` | list | conditional | — | Input plugin configs whose entry titles supplement the title list |
| `via` | list | yes | — | Search plugins to query |
| `interval` | string | no | `24h` | Minimum time between searches per title |
| `db` | string | no | `pipeliner.db` | SQLite state file for interval tracking |

At least one of `titles` or `from` must produce titles. The combined title list is deduplicated case-insensitively before searching.

### `from` entries

Each entry is a plugin name string or an object with a `name` key plus plugin-specific config. The entry titles returned by those plugins are added to the search queue:

```yaml
from:
  - name: input_trakt
    client_id: YOUR_CLIENT_ID
    access_token: YOUR_ACCESS_TOKEN
    type: movies
    list: watchlist
```

### `via` entries

Each entry references a registered [search plugin](../search/). Either a plugin name string or an object:

```yaml
via:
  - search_rss    # name only, uses defaults
  - name: search_rss
    url_template: "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"
```

## Example — static title list

```yaml
tasks:
  movies-discover:
    discover:
      titles:
        - "Dune Part Two"
        - "Oppenheimer"
      via:
        - name: search_rss
          url_template: "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"
      interval: 12h
      db: pipeliner.db
    metainfo_quality:
    quality:
      min: 1080p
    seen:
      db: pipeliner.db
    qbittorrent:
      host: localhost
```

## Example — dynamic title list from Trakt watchlist

```yaml
tasks:
  discover-watchlist:
    discover:
      from:
        - name: input_trakt
          client_id: YOUR_CLIENT_ID
          access_token: YOUR_ACCESS_TOKEN
          type: movies
          list: watchlist
      via:
        - name: search_rss
          url_template: "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"
      interval: 6h
      db: pipeliner.db
    metainfo_quality:
    quality:
      min: 1080p
    seen:
      db: pipeliner.db
    qbittorrent:
      host: localhost
```

## Example — combined static and dynamic

```yaml
tasks:
  discover-combined:
    discover:
      titles:
        - "Severance"       # always searched regardless of watchlist
      from:
        - name: input_trakt
          client_id: YOUR_CLIENT_ID
          access_token: YOUR_ACCESS_TOKEN
          type: shows
          list: watchlist
      via:
        - name: search_rss
          url_template: "https://jackett.example.com/api?q={{.QueryEscaped}}&apikey=abc"
      interval: 12h
      db: pipeliner.db
    series:
      db: pipeliner.db
      tracking: strict
    qbittorrent:
      host: localhost
```
