# tvdb_favorites_add

Adds accepted entries to the authenticated user's TheTVDB favorites list. Pairs with the [`tvdb_favorites`](../../source/tvdb_favorites/README.md) source, which reads the same list.

**TheTVDB's v4 API cannot remove favorites.** Per the official swagger, `/user/favorites` supports `GET` and `POST` only, so `action="remove"` is rejected at validation time. To stop following ended shows, filter on `series_status` into a local list ([`list_add`](../list_add/README.md)) or a Trakt list ([`trakt_list_update`](../trakt_list/README.md), which does support removal) and use that list as the `series.list` source instead of the raw favorites.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `user_pin` | string | yes | — | TheTVDB user PIN (enables favorites access) |
| `action` | string | no | `add` | Only `"add"` is supported; anything else is a validation error |

## Behavior

- The current favorites are fetched once per run; entries already favorited are consumed silently (no duplicate POSTs, chained sinks are skipped for them).
- Entries without a usable `tvdb_id` are marked failed.
- If an add call fails, only the affected entry is marked failed; the rest of the batch continues.
- Dry-run logs a "would add favorite" preview per entry and makes no HTTP calls.

## Example

```python
# Auto-favorite every show that survives filtering.
src  = input("rss", url="https://example.com/rss")
meta = process("metainfo_file", upstream=src)
tvdb = process("metainfo_tvdb", upstream=meta, api_key=tvdb_api_key)
flt  = process("condition", upstream=tvdb, rules=[
    {"accept": 'video_rating >= 7.5'},
])
output("tvdb_favorites_add", upstream=flt,
       api_key=tvdb_api_key, user_pin=tvdb_user_pin)
pipeline("auto-favorite", schedule="12h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `tvdb_id` |
