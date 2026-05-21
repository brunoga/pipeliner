# html

Scrapes all `<a href>` links from an HTML page and emits one entry per link. Optionally filters links by filename glob pattern.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url` | yes | — | Page URL to fetch |
| `mask` | no | — | Glob pattern to filter link filenames (e.g. `*.torrent`) |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `source` | Origin in the form `html:<hostname>` (e.g. `html:example.com`) |
| `title` | Link text, or the href if the link has no visible text |
| `html_page` | Source page URL |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `source`, `title`, `html_page` |
| Requires | — |

## Example

```python
src = input("html", url="https://example.com/downloads", mask="*.torrent")
output("print", upstream=src)
pipeline("my-pipeline", schedule="1h")
```
