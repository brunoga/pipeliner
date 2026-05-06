# accept_all

Accepts every entry that is not already accepted or rejected. Useful as a pass-through step before `list_add` when you want to store entries from an input plugin without applying any filter logic.

## Config

No configuration options. Use a null value or omit the block entirely:

```yaml
accept_all:
```

## Example — sync a Trakt watchlist into a persistent list

Note: `trakt_list` is a PhaseFrom sub-plugin and cannot be used directly as a task-level input. Use it inside `movies.from`, `series.from`, or `discover.from`.

```yaml
tasks:
  sync-watchlist:
    priority: 1
    # Use rss or another input plugin here, and filter with movies.from for Trakt list
    rss:
      url: "https://example.com/rss/movies"
    seen:
      local: true   # don't mark these as globally seen
    accept_all:
    list_add:
      list: movie_watchlist

schedules:
  sync-watchlist: 1h
```
