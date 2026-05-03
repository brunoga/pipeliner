# upgrade

Accepts entries only when they offer a quality improvement over the previously
downloaded version of the same title. Once the configured target quality is
reached, further downloads are rejected.

State is persisted in SQLite.

## Config

```yaml
upgrade:
  target: 1080p     # quality ceiling — stop accepting once this is reached (required)
  on_lower: reject  # what to do when a lower quality is offered: "reject" or "accept" (default: reject)
  db: upgrade.db    # SQLite database path (default: pipeliner.db)
```

The entry key is `series_name:series_id` when series metadata is present
(set by `metainfo_series` or the `series` filter); otherwise the raw title is
used. Run series metainfo before this filter to ensure stable keys.

## Example

```yaml
tasks:
  tv-upgrade:
    rss:
      url: "https://example.com/rss"
    metainfo_series:
    metainfo_quality:
    upgrade:
      target: 1080p
      on_lower: reject
      db: upgrade.db
    deluge:
      path: /downloads/tv
```
