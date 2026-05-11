# regexp

Accepts or rejects entries by regular expression. Rejection patterns are checked first; if any rejection pattern matches, the entry is rejected. If an accept list is configured and nothing matches, the entry is also rejected.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `accept` | string or list | conditional | — | Pattern(s); entry accepted if any matches |
| `reject` | string or list | conditional | — | Pattern(s); entry rejected if any matches |
| `from` | string or list | no | `["title"]` | Fields to match against (`title`, `url`, or any entry field name) |

At least one of `accept` or `reject` is required.

## Example

```python
src = input("rss", url="https://example.com/rss")
flt = process("regexp", from_=src,
              accept=["(?i)golang", "(?i)kubernetes"],
              reject=["(?i)sponsored"])
acc = process("accept_all", from_=flt)
output("email", from_=acc, smtp_host="smtp.example.com",
       **{"from": "me@example.com"}, to="me@example.com")
pipeline("tech-news", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |
