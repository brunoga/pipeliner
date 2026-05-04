# print

Prints each accepted entry to stdout. Useful for debugging pipelines or as a dry-run output.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `format` | string | no | `{{.Title}}\t{{.URL}}` | Output template |

## Example

```yaml
tasks:
  debug:
    rss:
      url: "https://example.com/feed"
    series:
      shows: ["Breaking Bad"]
    print:
      format: "[{{.Task}}] {{.Title}}"
```
