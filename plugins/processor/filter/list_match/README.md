# list_match

Accepts entries whose title is present in a named persistent list; rejects everything else. The list is populated by the [`list_add`](../../../sink/list_add/README.md) output plugin, which can run in a separate task.

This pair of plugins replaces FlexGet's `list_add` / `list_match` / `movie_list` mechanism.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `list` | string | yes | — | Name of the list to match against |
| `remove_on_match` | bool | no | `false` | Remove the entry from the list after a successful match |
| `reject_unmatched` | bool | no | `true` | Reject entries that do not match the list; set `false` to leave them undecided when chaining filters |

## Example — download movies from a persistent watchlist

```python
# Task 1 (priority 1): use movies.from to sync Trakt watchlist into a local list
task("sync-watchlist", [
    plugin("rss", url="https://example.com/rss/movies"),
    plugin("movies", **{"from": [
        {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
         "access_token": "YOUR_ACCESS_TOKEN", "type": "movies", "list": "watchlist"},
    ]}),
    plugin("accept_all"),
    plugin("list_add", list="movie_watchlist"),
], schedule="1h")

# Task 2 (priority 10): search and download, matching against the local list
task("movies-search", [
    plugin("discover", **{"from": [
        {"name": "trakt_list", "client_id": "YOUR_CLIENT_ID",
         "type": "movies", "list": "watchlist"},
    ], "via": [
        {"name": "rss_search",
         "url_template": "https://example.com/search?q={QueryEscaped}"},
    ], "interval": "24h"}),
    plugin("seen"),
    plugin("metainfo_quality"),
    plugin("list_match", list="movie_watchlist", remove_on_match=True),
    plugin("pathfmt", path="/media/movies", field="download_path"),
    plugin("transmission", host="localhost", path="{download_path}"),
], schedule="6h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |

## Notes

- Matching is by exact entry title. The `movies` filter (with `from`) is often the better choice when the source is a single Trakt or TVDB list — `list_match` is most useful when combining multiple sources or when you want manual list management.
- `remove_on_match=True` is appropriate for one-shot download queues (download once, then stop). Leave it `False` (the default) if multiple tasks should be able to match the same list entry.
- The list is stored in `pipeliner.db` in the same directory as the config file, shared with all other stateful plugins.
