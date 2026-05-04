# list_match

Accepts entries whose title is present in a named persistent list; rejects everything else. The list is populated by the [`list_add`](../../output/list_add/README.md) output plugin, which can run in a separate task.

This pair of plugins replaces FlexGet's `list_add` / `list_match` / `movie_list` mechanism.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `list` | string | yes | — | Name of the list to match against |
| `remove_on_match` | bool | no | `false` | Remove the entry from the list after a successful match |

## Example — download movies from a persistent watchlist

```yaml
tasks:
  # Task 1 (priority 1): sync Trakt watchlist into a local list
  sync-watchlist:
    priority: 1
    input_trakt:
      client_id: YOUR_CLIENT_ID
      access_token: YOUR_ACCESS_TOKEN
      type: movies
      list: watchlist
    seen:
      local: true
    accept_all:
    list_add:
      list: movie_watchlist

  # Task 2 (priority 10): search and download, matching against the local list
  movies-search:
    priority: 10
    discover:
      from:
        - name: input_trakt
          client_id: YOUR_CLIENT_ID
          type: movies
          list: watchlist
      via:
        - name: search_rss
          url_template: "https://example.com/search?q={QueryEscaped}"
      interval: 24h
    seen:
    metainfo_quality:
    list_match:
      list: movie_watchlist
      remove_on_match: true   # remove from list once downloaded
    pathfmt:
      path: /media/movies
    transmission:
      host: localhost
      path: "{download_path}"

schedules:
  sync-watchlist: 1h
  movies-search: 6h
```

## Notes

- Matching is by exact entry title. The `movies` filter (with `from`) is often the better choice when the source is a single Trakt or TVDB list — `list_match` is most useful when combining multiple sources or when you want manual list management.
- `remove_on_match: true` is appropriate for one-shot download queues (download once, then stop). Leave it `false` (the default) if multiple tasks should be able to match the same list entry.
- The list is stored in `pipeliner.db` in the same directory as the config file, shared with all other stateful plugins.
