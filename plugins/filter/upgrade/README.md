# upgrade

Accepts entries only when they offer a quality improvement over the previously
downloaded version of the same title. Once the configured target quality is
reached, further downloads are rejected.

State is persisted in `pipeliner.db` in the same directory as the config file.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `target` | string | yes | — | Quality ceiling — stop accepting once reached (e.g. `1080p`, `2160p bluray`) |
| `on_lower` | string | no | `reject` | What to do when the incoming quality is not better: `reject` or `accept` |

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
    deluge:
      path: /downloads/tv
```
