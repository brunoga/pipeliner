# webhook (notifier)

POSTs a JSON payload to an HTTP endpoint. Works with Discord, Slack, Gotify, and any webhook-accepting service. Used via the [`notify` output plugin](../README.md) with `via="webhook"`.

## Config

Pass these keys inside the `config={}` dict of the `notify` output:

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url` | yes | — | Webhook endpoint URL |
| `headers` | no | — | Additional HTTP headers as a `{name: value}` dict |

## Payload

```json
{"title": "…", "body": "…", "entries": [{"title": "…", "url": "…"}, …]}
```

The `title` and `body` values come from the `title=` and `body=` keys on the `notify` output.

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
       via="webhook",
       config={"url": env("DISCORD_WEBHOOK_URL", default="https://discord.com/api/webhooks/…")},
       title="pipeliner — {{len .Entries}} new episode(s)",
       body="{{range .Entries}}- {{.Title}}\n{{end}}")
pipeline("tv", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` (via `notify`) |
| Produces | — |
| Requires | — |
