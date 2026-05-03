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

```yaml
tasks:
  tv:
    # ... filters and output ...
    notify:
      via: webhook
      url: "https://hooks.slack.com/services/T.../B.../..."
      headers:
        Authorization: "Bearer token"
      title: "{{len .Entries}} new episodes queued"
      body: "{{range .Entries}}- {{.Title}}\n{{end}}"
```
