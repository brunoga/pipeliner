# webhook (notifier)

POSTs a JSON payload to an HTTP endpoint. Used via the [`notify` output plugin](../../output/notify/README.md).

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Webhook endpoint URL |
| `headers` | map | no | — | Additional HTTP headers to include |

## Payload

```json
{
  "title": "rendered title string",
  "body": "rendered body string",
  "entries": [
    { "title": "...", "url": "..." }
  ]
}
```

## Example

```python
output("notify", from_=ready, via="webhook", url=env("WEBHOOK_URL"))
```
