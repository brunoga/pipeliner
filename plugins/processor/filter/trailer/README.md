# trailer

Detects entries whose title looks like a trailer, teaser, featurette, behind-the-scenes clip or other short-form release, and either rejects them (default) or keeps only them.

Detection is a case-insensitive word-boundary match against the entry title for any of: `trailer`, `teaser`, `sneak peek`, `featurette`, `behind the scenes`, `BTS`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `mode` | enum (`reject` / `accept`) | no | `reject` | `reject` drops trailer-like entries; `accept` rejects everything that is *not* a trailer |

## Example — drop trailers from a movies feed

```python
src   = input("rss", url="https://example.com/feed")
clean = process("trailer", upstream=src)            # mode defaults to "reject"
out   = output("transmission", upstream=clean, host="localhost")
pipeline("movies", schedule="1h")
```

## Example — collect only trailers

```python
src      = input("rss", url="https://example.com/feed")
trailers = process("trailer", upstream=src, mode="accept")
output("notify", upstream=trailers, via="email",
       config={"smtp_host": "smtp.example.com",
               "sender": "me@example.com", "to": "me@example.com"})
pipeline("trailer-watch", schedule="6h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |
