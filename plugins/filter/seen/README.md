# seen

Rejects entries already processed in a previous run. Computes a SHA-256 fingerprint from configured entry fields and checks it against the shared SQLite store.

## When to use

**You do not need `seen` for TV series, movies, or premieres.** Those filters maintain their own trackers (`series`, `movies`, and the series tracker respectively) and handle deduplication automatically.

`seen` is useful for any pipeline that lacks a domain-specific tracker — the most common cases are:

- **RSS news or article feeds** — there is no episode or movie identifier to key off; `seen` prevents the same article being emailed or processed again when it reappears in the feed.
- **Direct downloads from HTML pages or generic feeds** — any input where entries repeat across runs and no series/movies filter is involved.
- **Torrent feeds with no show-list matching** — e.g. downloading every item from a curated feed exactly once, without tracking by show name.

**Typical example — tech news to email:**

```yaml
tasks:
  tech-news:
    - rss:
        url: "https://feeds.arstechnica.com/arstechnica/technology-lab"
    - seen:
    - regexp:
        accept: "(?i)linux|open.?source|golang"
    - email:
        smtp_host: smtp.gmail.com
        smtp_port: 587
        from: me@example.com
        to: me@example.com
        subject: "{{len .Entries}} new article(s)"

schedules:
  tech-news: 1h
```

Without `seen`, every hourly run would re-send articles that were already emailed. With `seen`, each article URL is fingerprinted and stored after the first successful delivery, so it is silently rejected on all subsequent runs.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `fields` | string or list | no | `["url"]` | Entry fields to include in the fingerprint |
| `local` | bool | no | false | Scope the seen store to this task name only |

## Notes

- Fingerprints are written to the store during the **learn** phase, which runs after all output plugins complete successfully. If an output plugin fails (e.g. email not delivered), the entry is not marked seen and will be retried next run.
- Use `local: true` when multiple tasks consume the same feed but should track seen entries independently.
- State is stored in `pipeliner.db` in the same directory as the config file.
