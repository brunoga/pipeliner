# seen

Rejects entries already processed in a previous run. Computes a SHA-256 fingerprint from configured entry fields and checks it against the shared SQLite store.

## When to use

**You do not need `seen` for TV series, movies, or premieres.** Those filters maintain their own trackers (`series`, `movies`, and the series tracker respectively) and handle deduplication automatically.

`seen` is useful for any pipeline that lacks a domain-specific tracker — the most common cases are:

- **RSS news or article feeds** — there is no episode or movie identifier to key off; `seen` prevents the same article being emailed or processed again when it reappears in the feed.
- **Direct downloads from HTML pages or generic feeds** — any input where entries repeat across runs and no series/movies filter is involved.
- **Torrent feeds with no show-list matching** — e.g. downloading every item from a curated feed exactly once, without tracking by show name.

**Typical example — tech news to email:**

```python
src  = input("rss", url="https://example.com/rss")
seen = process("seen", from_=src)
acc  = process("regexp", from_=seen, accept=[".+"])
output("transmission", from_=acc, host="localhost")
pipeline("news", schedule="1h")
```

Without `seen`, every hourly run would re-send articles that were already emailed. With `seen`, each article URL is fingerprinted and stored after the first successful delivery, so it is silently rejected on all subsequent runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `fields` | string or list | no | `["url"]` | Entry fields to include in the fingerprint |
| `local` | bool | no | false | Scope the seen store to this task name only |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |

In DAG pipelines, `seen` records new entries in its `Process()` call immediately rather than waiting for a separate learn phase. Entries that fail downstream sinks will already be marked seen; use `seen` at the beginning of the pipeline to skip duplicates on the next run.

## Notes

- Fingerprints are written to the store immediately in `Process()` so they are persisted even if downstream sinks fail.
- Use `local=True` when multiple tasks consume the same feed but should track seen entries independently.
- State is stored in `pipeliner.db` in the same directory as the config file.
