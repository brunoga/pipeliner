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

Dry runs **peek** at the queue without consuming it, so you can push an item without `?pipeline=`, dry-run to watch it flow through the pipeline, and then trigger the real run — the item is still queued.

## A push and the pipeline's other sources

A push that names a pipeline triggers the whole pipeline, which used to mean every *other* source ran too. On one live pipeline a push of a single film also cost a Trakt fetch of 988 titles and a 2m09s search sweep — and the pushed film then competed with 44 unrelated candidates for the four slots a downstream `limit` allowed.

Give the sources that have nothing to do with pushes a `run_on`:

```python
feed = input("trakt_list", ..., run_on=["schedule", "manual"])
hook = input("webhook", queue="3d-movies")
```

A webhook source for a *different* queue already costs nothing — it drains its own, empty queue — so `run_on` is for the feed sources beside it. With more than one webhook in a pipeline, `run_on="webhook:3d-movies"` narrows a source to one queue. See the user guide for the full token list.
