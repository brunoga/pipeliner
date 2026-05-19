# notify (output)

Sends a notification about the accepted entries via a configured notifier (webhook, email, Pushover, etc.). Unlike the `email` output plugin, this is batch-level — one notification for the whole run, not one per entry.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `via` | string | yes | — | Notifier type: `webhook`, `email`, or `pushover` |
| `config` | dict | yes | — | Notifier-specific config (see notifier docs below) |
| `title` | string | no | `pipeliner: {{len .Entries}} new item(s)` | Notification title template |
| `body` | string | no | `{{range .Entries}}- {{.Title}}\n{{end}}` | Notification body template |
| `on` | string | no | `only-accepted` | `all` to notify even when no entries were accepted |

Notifier-specific config is passed as a nested `config={}` dict. See [`notify/email`](email/README.md), [`notify/webhook`](webhook/README.md), and [`notify/pushover`](pushover/README.md) for available keys.

Templates have access to `.Entries` (list of accepted entries), `.Task` (pipeline name).

## Examples

```python
# Webhook (Discord, Slack, Gotify, …)
output("notify", upstream=ready,
       via="webhook",
       config={"url": env("DISCORD_WEBHOOK_URL", default="https://…")},
       title="pipeliner — {{len .Entries}} new episode(s)")

# Pushover
output("notify", upstream=ready,
       via="pushover",
       config={"token": env("PUSHOVER_TOKEN", default="YOUR_TOKEN"),
               "user":  env("PUSHOVER_USER",  default="YOUR_USER")})

# Email summary
output("notify", upstream=ready,
       via="email",
       config={"smtp_host": "smtp.example.com", "smtp_port": 587,
               "sender": "pipeliner@example.com", "to": "you@example.com",
               "username": env("SMTP_USER", default=""), "password": env("SMTP_PASS", default="")})
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
