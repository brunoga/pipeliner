# accept_all

Accepts every entry that is not already accepted or rejected. Useful as a pass-through step before `list_add` when you want to store entries from an input plugin without applying any filter logic.

## Config

No configuration options. Call with just the plugin name:

```python
plugin("accept_all")
```

## Example — sync a Trakt watchlist into a persistent list

Note: `trakt_list` is a PhaseFrom sub-plugin and cannot be used directly as a task-level input. Use it inside `movies.from`, `series.from`, or `discover.from`.

```python
task("sync-watchlist", [
    # Use rss or another input plugin here, and filter with movies.from for Trakt list
    plugin("rss", url="https://example.com/rss/movies"),
    plugin("seen", local=True),
    plugin("accept_all"),
    plugin("list_add", list="movie_watchlist"),
], schedule="1h")
```
