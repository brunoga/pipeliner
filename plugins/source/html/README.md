# html

Scrapes all `<a href>` links from an HTML page and emits one entry per link. Optionally filters links by filename glob pattern.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Page URL to fetch |
| `mask` | string | no | — | Glob pattern to filter link filenames (e.g. `*.torrent`) |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `html_page` | Source page URL |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `html_page` |
| Requires | — |

## Example

```python
src = input("html", url="https://example.com/downloads", mask="*.torrent")
output("print", upstream=src)
pipeline("my-pipeline", schedule="1h")
```
