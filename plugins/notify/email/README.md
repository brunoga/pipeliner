# email (notifier)

Sends a single email with the run title as subject and body via SMTP. Used via the [`notify` output plugin](../../output/notify/README.md).

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `smtp_host` | string | yes | — | SMTP server hostname |
| `port` | int | no | `25` | SMTP port |
| `from` | string | yes | — | Sender address |
| `to` | string or list | yes | — | Recipient address(es) |
| `username` | string | no | — | SMTP auth username |
| `password` | string | no | — | SMTP auth password |

## Example

```yaml
tasks:
  tv:
    # ... filters and output ...
    notify:
      via: email
      smtp_host: smtp.example.com
      from: pipeliner@example.com
      to: me@example.com
      username: pipeliner@example.com
      password: secret
      title: "{{len .Entries}} new episodes"
```
