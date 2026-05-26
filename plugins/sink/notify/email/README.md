# email (notifier)

Sends a summary email via SMTP for the entire run. Used via the [`notify` output plugin](../README.md) with `via="email"`.

## Config

Pass these keys inside the `config={}` dict of the `notify` output:

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `smtp_host` | yes | — | SMTP server hostname |
| `smtp_port` | no | `25` | SMTP server port |
| `sender` | yes | — | Sender address |
| `to` | yes | — | Recipient address or list of addresses |
| `username` | no | — | SMTP auth username |
| `password` | no | — | SMTP auth password |

## Example

```python
src  = input("rss", url="https://example.com/rss")
seen = process("seen",   upstream=src)
meta = process("metainfo_file", upstream=seen)
flt  = process("series", upstream=meta, static=["Breaking Bad"])
fmt  = process("pathfmt", upstream=flt,
               path="/media/tv/{title}/Season {series_season:02d}",
               field="download_path")
output("transmission", upstream=fmt, host="localhost")
output("notify", upstream=fmt,
       via="email",
       config={"smtp_host": "smtp.gmail.com", "smtp_port": 587,
               "sender":   env("SMTP_USER"),
               "to":       env("SMTP_TO"),
               "username": env("SMTP_USER"),
               "password": env("SMTP_PASS")})
pipeline("tv", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` (via `notify`) |
| Produces | — |
| Requires | — |
