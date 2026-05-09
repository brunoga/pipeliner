# email (output)

Sends an email for each batch of accepted entries via SMTP. Subject and body are Go templates rendered against the entry batch.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `smtp_host` | string | yes | — | SMTP server hostname |
| `smtp_port` | int | no | `25` | SMTP port |
| `from` | string | yes | — | Sender address |
| `to` | string or list | yes | — | Recipient address(es) |
| `username` | string | no | — | SMTP auth username |
| `password` | string | no | — | SMTP auth password |
| `subject` | string | no | `pipeliner: {{len .Entries}} new item(s)` | Subject template |
| `body_template` | string | no | `{{range .Entries}}- {{.Title}}\n  {{.URL}}\n{{end}}` | Body template |

## Template context

`.Entries` is a slice of accepted entries. Each entry has `.Title`, `.URL`, and all field map values.

## Example

```yaml
tasks:
  news:
    - rss:
        url: "https://feeds.example.com/tech"
    - regexp:
        accept: "(?i)golang"
    - email:
        smtp_host: smtp.gmail.com
        smtp_port: 587
        from: alerts@example.com
        to: me@example.com
        username: alerts@example.com
        password: app-password
        subject: "{{len .Entries}} new Go articles"
```
