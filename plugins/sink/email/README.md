# email (output)

Sends one email per pipeline run for all accepted entries via SMTP. Subject and body are templates rendered against the entry batch.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `smtp_host` | yes | — | SMTP server hostname |
| `smtp_port` | no | `25` | SMTP server port |
| `sender` | yes | — | Sender address |
| `to` | yes | — | Recipient address or list of addresses |
| `username` | no | — | SMTP auth username |
| `password` | no | — | SMTP auth password |
| `subject` | no | `pipeliner: {{len .Entries}} new item(s)` | Subject template |
| `body_template` | no | plain entry list | Body template; context has `.Entries` (each with `.Title` and `.URL`) |
| `html` | no | `false` | Send as HTML email |

## Example

```python
src  = input("rss", url="https://feeds.arstechnica.com/arstechnica/index")
seen = process("seen",      upstream=src)
flt  = process("regexp",    upstream=seen, accept=["(?i)linux|security|AI"])
acc  = process("accept_all", upstream=flt)
output("email", upstream=acc,
       smtp_host="smtp.gmail.com", smtp_port=587,
       sender=env("SMTP_USER"), to=env("SMTP_TO"),
       username=env("SMTP_USER"), password=env("SMTP_PASS"),
       subject="{{len .Entries}} new article(s)")
pipeline("tech-news", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
