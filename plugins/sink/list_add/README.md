# list_add

Stores accepted entries in a named persistent list. The list can then be read by the [`list_match`](../../processor/filter/list_match/README.md) filter plugin in the same or a different task.

This pairs with `list_match` to replicate FlexGet's `movie_list` / `list_add` / `list_match` cross-task coordination pattern.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `list` | string | yes | — | Name of the list to add entries to |

## Example

```python
# Populate a list from an RSS feed (trakt_list is a from plugin, not a standalone input)
task("sync-list", [
    plugin("rss", url="https://example.com/rss/movies"),
    plugin("seen", local=True),   # don't mark as globally seen
    plugin("accept_all"),
    plugin("list_add", list="movie_watchlist"),
], schedule="1h")
```

## Notes

- Only accepted entries are stored. Pair with `accept_all` when the task's purpose is purely list population with no other filtering.
- If an entry title already exists in the list its URL is updated silently.
- The list is stored in `pipeliner.db` in the same directory as the config file, shared with `seen`, `series`, `movies`, and other stateful plugins.

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
