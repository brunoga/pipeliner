# list_add

Stores accepted entries in a named persistent list. The list can then be read by the [`list_match`](../../filter/list_match/README.md) filter plugin in the same or a different task.

This pairs with `list_match` to replicate FlexGet's `movie_list` / `list_add` / `list_match` cross-task coordination pattern.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `list` | string | yes | — | Name of the list to add entries to |

## Example

```yaml
tasks:
  sync-watchlist:
    priority: 1
    input_trakt:
      client_id: YOUR_CLIENT_ID
      access_token: YOUR_ACCESS_TOKEN
      type: movies
      list: watchlist
    seen:
      local: true   # don't mark as globally seen
    accept_all:
    list_add:
      list: movie_watchlist

schedules:
  sync-watchlist: 1h
```

## Notes

- Only accepted entries are stored. Pair with `accept_all` when the task's purpose is purely list population with no other filtering.
- If an entry title already exists in the list its URL is updated silently.
- The list is stored in `pipeliner.db` in the same directory as the config file, shared with `seen`, `series`, `movies`, and other stateful plugins.
