# notify (output)

Sends a notification about the accepted entries via a configured notifier (webhook, email, etc.). Unlike the `email` output plugin, this is batch-level — one notification for the whole run, not one per entry.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `via` | string | yes | — | Notifier type: `webhook` or `email` |
| `title` | string | no | `pipeliner: {{len .Entries}} new item(s)` | Notification title template |
| `body` | string | no | `{{range .Entries}}- {{.Title}}\n{{end}}` | Notification body template |
| `on` | string | no | — | Set to `all` to notify even when no entries were accepted |

Additional keys are passed through to the chosen notifier. See [`notify/email`](email/README.md) and [`notify/webhook`](webhook/README.md) for their specific config keys.

## Example

```python
output("notify", upstream=ready,
       via="webhook", url=env("DISCORD_WEBHOOK"))
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
