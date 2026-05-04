# accept_all

Accepts every entry that is not already accepted or rejected. Useful as a pass-through step before `list_add` when you want to store entries from an input plugin without applying any filter logic.

## Config

No configuration options. Use a null value or omit the block entirely:

```yaml
accept_all:
```

## Example — sync a Trakt watchlist into a persistent list

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
      local: true   # don't mark these as globally seen
    accept_all:
    list_add:
      list: movie_watchlist

schedules:
  sync-watchlist: 1h
```
