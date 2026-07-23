# trakt_list_update

Adds or removes accepted entries on a Trakt.tv user list or watchlist. Pairs with the [`trakt_list`](../../source/trakt_list/README.md) source, which reads lists. Unlike TheTVDB favorites, Trakt lists support removal, so this sink covers full remote list hygiene: prune ended shows from the watchlist, mirror a filtered list, auto-add discovered premieres.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `client_id` | string | yes | — | Trakt API client ID |
| `list` | string | yes | — | `"watchlist"` or a personal list slug |
| `action` | string | no | `add` | `"add"` or `"remove"` |
| `type` | string | no | infer | `"shows"` or `"movies"`; when omitted, inferred per entry from `media_type` |
| `client_secret` | string | one of | — | OAuth client secret; tokens managed via `pipeliner.db` (run `pipeliner auth trakt`) |
| `access_token` | string | one of | — | Static OAuth2 bearer token (alternative to `client_secret`) |

List mutation is always authenticated: exactly one of `client_secret` (stored token) or `access_token` must be set.

## Behavior

- **Batched**: all entries of a run are collected into a single add/remove request — Trakt's sync endpoints take item arrays. Endpoints used: `POST /sync/watchlist[/remove]` for the watchlist, `POST /users/me/lists/{slug}/items[/remove]` for personal lists.
- **ID matching**: the request's `ids` object is built from whichever ID fields are present on the entry: `trakt_id`, `trakt_imdb_id`/`video_imdb_id`, `trakt_tmdb_id`/`tmdb_id`, `trakt_tvdb_id`/`tvdb_id`. Entries with none are marked failed.
- Entries Trakt reports as `not_found` are marked failed so chained sinks and the commit phase do not treat them as done.
- Entries whose type cannot be determined (no `type=` config and no `media_type` field) are marked failed.
- Dry-run logs a "would add/remove" preview per entry and makes no HTTP calls.

## Example

```python
# Prune ended shows from the Trakt watchlist (status values from Trakt are
# lowercase: "returning series", "ended", "canceled").
watch = input("trakt_list", client_id=trakt_client_id,
              client_secret=trakt_client_secret,
              type="shows", list="watchlist")
done  = process("condition", upstream=watch, rules=[
    {"accept": 'series_status == "ended" or series_status == "canceled"'},
    {"reject": 'true'},
])
output("trakt_list_update", upstream=done,
       client_id=trakt_client_id, client_secret=trakt_client_secret,
       list="watchlist", action="remove")
pipeline("prune-trakt-watchlist", schedule="168h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | one of `trakt_id`, `trakt_imdb_id`, `video_imdb_id`, `trakt_tmdb_id`, `tmdb_id`, `trakt_tvdb_id`, `tvdb_id` |
