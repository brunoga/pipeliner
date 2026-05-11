# print

Prints each accepted entry to stdout. Useful for debugging pipelines or as a dry-run output.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `format` | string | no | `{{.Title}}\t{{.URL}}` | Output template |

## Example

```python
src = input("rss", url="https://example.com/rss")
acc = process("accept_all", from_=src)
output("print", from_=acc)
pipeline("debug")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
