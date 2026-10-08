# library_shows (source)

Emits one entry per TV show present in a media server's library.

This answers a different question from [`series_tracker`](../series_tracker/),
which emits the shows pipeliner holds *download records* for. `library_shows`
emits the shows you actually **have** — including every show acquired some
other way, which the tracker has never heard of.

Pair it with
[`series_gaps`](../../processor/gaps/)`(backend=…, seasons="from_first_owned")`
and the whole chain is answered by the library: which shows to consider, which
episodes are missing, and how far back to go.

Also usable as a `list=` title source.

## Config

| Key | Required | Default | Description |
|---|---|---|---|
| `backend` | no | `plex` | `plex` or `jellyfin` |
| `url` | no | — | Server base URL; omit with `token` for Plex account mode |
| `token` | no | — | Server API token; omit with `url` for Plex account mode |
| `sections` | no | — | Only read these server libraries, by name |
| `exclude_sections` | no | — | Read every library except these |

Omitting both `url` and `token` selects Plex **account mode**: sign in once on
the Tools tab and every owned server is read, with the token picked up per call
so a later sign-in needs no restart.

## Fields set

| Field | Value |
|---|---|
| `title` | the show title as the server reports it |
| `series_name` | normalized name — the key `series_gaps` looks up by |
| `series_episode_count` | episodes of the show held by the server |
| `media_type` | always `series` |
| `source` | `library_shows` |

The entry URL is the stable synthetic `pipeliner://series/<normalized-name>`,
the same shape `series_tracker` uses, so the two sources dedup against each
other when merged.

## Example

```python
shows = input("library_shows", sections=["TV Shows"])
gaps  = process("series_gaps", upstream=shows, api_key=env("TVDB_API_KEY"),
                backend="plex", sections=["TV Shows"],
                seasons="from_first_owned",
                pack_threshold=1.0, max_per_run=30)
found = process("discover", upstream=gaps, interval="12h", search=[...])
```

See [`configs/series-backfill-plex.star`](../../../configs/series-backfill-plex.star)
for the full pipeline.

## Notes

- An unreachable server is an **error**, not an empty list. Emitting nothing
  would be indistinguishable from an empty library, and a downstream backfill
  would then do nothing while the run reported success.
- Movies and items with no show name are ignored.
