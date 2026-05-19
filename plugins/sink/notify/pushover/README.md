# pushover (notifier)

Sends a push notification via the [Pushover](https://pushover.net) API. Used via the [`notify` output plugin](../README.md) with `via="pushover"`.

## Config

Pass these keys inside the `config={}` dict of the `notify` output:

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `user` | yes | — | Pushover User Key (from pushover.net) |
| `token` | yes | — | Pushover API Token (from your application) |
| `device` | no | — | Target device name; omit to send to all devices |
| `url` | no | — | URL to attach to the notification |

Get your user key and API token at [pushover.net](https://pushover.net/).

## Example

```python
src  = input("rss", url="https://example.com/rss")
seen = process("seen",   upstream=src)
flt  = process("series", upstream=seen, static=["Breaking Bad"])
fmt  = process("pathfmt", upstream=flt,
               path="/media/tv/{title}/Season {series_season:02d}",
               field="download_path")
output("transmission", upstream=fmt, host="localhost")
output("notify", upstream=fmt,
       via="pushover",
       config={"token": env("PUSHOVER_TOKEN"),
               "user":  env("PUSHOVER_USER")})
pipeline("tv", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` (via `notify`) |
| Produces | — |
| Requires | — |
