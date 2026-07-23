# library_refresh

Asks a Plex or Jellyfin server to rescan its libraries. Chain it after a
download sink: chained sinks only receive entries the upstream sink
confirmed, so the rescan fires exactly when new content actually landed —
and at most once per run, however many entries arrived. A failed rescan is
logged but never fails the downloads (the server will pick the files up on
its own schedule).

## Config

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `backend` | string | yes | `plex` or `jellyfin` |
| `url` | string | yes | Media server base URL, e.g. `http://localhost:32400` |
| `token` | string | yes | Media server API token |

## Example

```python
out     = output("transmission", upstream=flt, host="localhost")
rescan  = output("library_refresh", upstream=out,
                 backend="plex", url="http://localhost:32400",
                 token=env("PLEX_TOKEN", default="YOUR_TOKEN"))
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` (chainable after another sink) |
| Produces | — |
| Requires | — |
