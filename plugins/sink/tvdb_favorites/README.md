# tvdb_favorites_add

Adds accepted entries to the authenticated user's TheTVDB favorites list — or, with legacy v3 credentials, removes them. Pairs with the [`tvdb_favorites`](../../source/tvdb_favorites/README.md) source, which reads the same list.

**TheTVDB's v4 API cannot remove favorites.** Per the official swagger, `/user/favorites` supports `GET` and `POST` only. Removal instead goes through TheTVDB's legacy v3 API (`DELETE /user/favorites/{id}` on `api.thetvdb.com`), which needs a separate credential pair: set `legacy_user_key` and `legacy_user_name` to the "unique ID" (userkey) and username shown in your [thetvdb.com account dashboard](https://thetvdb.com/dashboard/account/editinfo). `action="remove"` without both keys is a validation error. The v3 login is lazy — it only happens when a removal is actually issued, so a v3 outage never affects v4 calls.

If you'd rather not use the legacy API: filter on `series_status` into a local list ([`list_add`](../list_add/README.md)) or a Trakt list ([`trakt_list_update`](../trakt_list/README.md), which supports removal natively) and use that list as the `series.list` source instead of the raw favorites.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `api_key` | string | yes | — | TheTVDB v4 API key |
| `user_pin` | string | yes | — | TheTVDB user PIN (enables favorites access) |
| `action` | string | no | `add` | `"add"` or `"remove"`; `"remove"` requires the legacy credentials below |
| `legacy_user_key` | string | for `remove` | — | Legacy v3 userkey (account identifier) |
| `legacy_user_name` | string | for `remove` | — | Legacy v3 username |

## Behavior

- The current favorites are fetched once per run, making both directions idempotent: entries already favorited are consumed silently by `add` (no duplicate POSTs), and entries not in the favorites are consumed silently by `remove` (no pointless DELETEs). Chained sinks are skipped for consumed entries.
- Entries without a usable `tvdb_id` are marked failed.
- If an add or remove call fails, only the affected entry is marked failed; the rest of the batch continues.
- Dry-run logs a "would add favorite"/"would remove favorite" preview per entry and makes no HTTP calls.

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

```python
# Prune ended shows from the favorites (requires legacy v3 credentials).
favs  = input("tvdb_favorites", api_key=tvdb_api_key, user_pin=tvdb_user_pin)
ended = process("condition", upstream=favs, rules=[
    {"accept": 'series_status == "Ended" or series_status == "Cancelled"'},
    {"reject": 'true'},
])
output("tvdb_favorites_add", upstream=ended, action="remove",
       api_key=tvdb_api_key, user_pin=tvdb_user_pin,
       legacy_user_key=tvdb_legacy_user_key,
       legacy_user_name=tvdb_legacy_user_name)
pipeline("prune-favorites", schedule="168h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `tvdb_id` |
