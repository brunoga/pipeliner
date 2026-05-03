# quality

Rejects entries whose parsed video quality falls outside a configured range. At least one of `min` or `max` must be set.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `min` | string | conditional | — | Minimum quality spec (e.g. `720p`, `1080p`) |
| `max` | string | conditional | — | Maximum quality spec |

Quality specs are compared across resolution, source, codec, audio, and color range dimensions. A release must meet or exceed every configured dimension to pass.

## Example

```yaml
tasks:
  hd-only:
    rss:
      url: "https://example.com/feed"
    quality:
      min: 720p
      max: 1080p
```
