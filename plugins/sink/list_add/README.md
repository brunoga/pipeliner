# list_add

Stores accepted entries in a named persistent list. The list can then be read by the [`list_match`](../../processor/filter/list_match/README.md) filter plugin in the same or a different pipeline.

This pairs with `list_match` to replicate FlexGet's `movie_list` / `list_add` / `list_match` cross-pipeline coordination pattern. It is also the supported way to "prune" TheTVDB favorites: since TheTVDB's API cannot remove favorites, filter still-running shows into a local list and consume that list instead of the raw favorites (see [`tvdb_favorites_add`](../tvdb_favorites/README.md)).

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `list` | string | yes | — | Name of the list to add entries to |

## Example

```python
# Populate a list from an RSS feed.
src = input("rss", url="https://example.com/rss/movies")
new = process("seen", upstream=src, local=True)   # don't mark as globally seen
acc = process("accept_all", upstream=new)
output("list_add", upstream=acc, list="movie_watchlist")
pipeline("sync-list", schedule="1h")
```

```python
# Keep a local list of still-running TheTVDB favorites (TheTVDB's API cannot
# remove favorites, so prune into a local list instead).
favs  = input("tvdb_favorites", api_key=tvdb_api_key, user_pin=tvdb_user_pin)
alive = process("condition", upstream=favs, rules=[
    {"reject": 'series_status == "Ended" or series_status == "Cancelled"'},
    {"accept": 'true'},
])
output("list_add", upstream=alive, list="active_favorites")
pipeline("sync-active-favorites", schedule="168h")
```

## Notes

- Only accepted entries are stored. Pair with `accept_all` when the pipeline's purpose is purely list population with no other filtering.
- If an entry title already exists in the list its URL is updated silently.
- The list is stored in `pipeliner.db` in the same directory as the config file, shared with `seen`, `series`, `movies`, and other stateful plugins.

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
