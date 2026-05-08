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

## Example

```yaml
tasks:
  my-task:
    - html:
        url: "https://example.com/downloads"
        mask: "*.torrent"
```
