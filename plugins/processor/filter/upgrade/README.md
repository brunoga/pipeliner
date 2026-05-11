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

The entry key is `title:series_episode_id` when series metadata is present
(set by `metainfo_series` or the `series` filter); otherwise the raw title is
used. Run a series metainfo plugin before this filter to ensure stable keys.

## Example

```python
src  = input("rss", url="https://example.com/rss")
q    = process("metainfo_quality", from_=src)
up   = process("upgrade", from_=q, target="1080p")
output("transmission", from_=up, host="localhost")
pipeline("upgrade-quality")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | `video_quality` (set by `metainfo_quality`) |
