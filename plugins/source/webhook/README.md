# webhook

Emits entries pushed to the web server's ingest endpoint — the push
counterpart to polling sources. Anything that can POST JSON (autobrr, IRC
announce bridges, custom scripts) becomes a pipeline source with instant
turnaround.

## Setup

1. Set `PIPELINER_INGEST_TOKEN` in the daemon's environment. Without it the
   endpoint answers 404. Treat the token like a password.
2. Add a `webhook` source with a queue name to a pipeline.
3. POST items to `/api/ingest/<queue>` with `Authorization: Bearer <token>`.
   Add `?pipeline=<name>` to trigger that pipeline immediately after queuing.

```sh
curl -X POST "http://localhost:8080/api/ingest/announces?pipeline=push-grabs" \
  -H "Authorization: Bearer $PIPELINER_INGEST_TOKEN" \
  -d '{"title":"Show.S01E01.720p.WEB-DL","url":"https://tracker/dl/123","fields":{"indexer":"xyz"}}'
```

The body is one item or an array of items: `title` (required), `url`
(optional — a synthetic URL is generated when absent), `fields` (optional map
merged onto the entry). Queues are in-memory and bounded (1000 items); a
daemon restart drops undrained items — announcers re-announce, and the
endpoint's response reports `queued`/`dropped`/`rejected` counts honestly.

## Config

| Key | Required | Description |
|-----|----------|-------------|
| `queue` | yes | Ingest queue name — the `{queue}` path segment pushers POST to |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title` |
