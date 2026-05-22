# list_match

Accepts entries whose title is in a named persistent list. The list is populated by the [`list_add`](../../../sink/list_add/README.md) output plugin. This pair allows two independent pipelines to coordinate: one pipeline discovers and stores titles, another searches and downloads them.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `list` | yes | — | Name of the list to match against |
| `remove_on_match` | no | `false` | Remove the entry from the list after a successful match (one-shot queues) |
| `reject_unmatched` | no | `true` | Reject entries not in the list; set `false` to leave them undecided when chaining filters |

## Example

```python
# Pipeline 1: populate the list every hour from Trakt
src = input("trakt_list", client_id=env("TRAKT_ID"),
            client_secret=env("TRAKT_SECRET"), type="movies", list="watchlist")
acc = process("accept_all", upstream=src)
output("list_add", upstream=acc, list="movie_watchlist")
pipeline("sync-watchlist", schedule="1h")

# Pipeline 2: search for movies in the list via Jackett
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"), type="movies", list="watchlist")
results   = process("discover", upstream=watchlist,
    search=[{"name": "jackett", "url": "http://localhost:9117",
             "api_key": env("JACKETT_KEY"), "categories": ["2000"]}],
    interval="24h")
seen = process("seen", upstream=results)
flt  = process("list_match", upstream=seen, list="movie_watchlist",
               remove_on_match=True)
output("qbittorrent", upstream=flt, host="localhost")
pipeline("download-movies", schedule="6h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |

## Notes

- Matching is by entry title (case-insensitive fuzzy match). For precise matching, use `movies` or `series` with a static list instead.
- `remove_on_match=true` is appropriate for one-shot queues where each title should be downloaded exactly once.
- The list is stored in `pipeliner.db` in the same directory as the config file.
