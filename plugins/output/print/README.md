# print

Prints each accepted entry to stdout. Useful for debugging pipelines or as a dry-run output.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `format` | string | no | `{{.Title}}\t{{.URL}}` | Output template |

## Example

```python
task("debug", [
    plugin("rss", url="https://example.com/feed"),
    plugin("series", static=["Breaking Bad"]),
    plugin("print", format="[{{.Task}}] {{.Title}}"),
])
```
