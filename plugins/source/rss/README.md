# rss

Fetches entries from an RSS 2.0 or Atom 1.0 feed. Prefers enclosure URLs (torrent feeds) and falls back to item links.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url` | yes | — | Feed URL |
| `all_entries` | no | `false` | Accepted in config for compatibility; has no effect — all feed items are always returned as undecided entries for downstream processors to filter |

## Fields set on entry

### Generic fields

| Field | Description |
|-------|-------------|
| `source` | Origin in the form `rss:<hostname>` (e.g. `rss:nyaa.si`) |
| `title` | Item title |
| `description` | Item description or summary |
| `published_date` | Publication date string |

### RSS fields

| Field | Description |
|-------|-------------|
| `rss_feed` | Feed URL (the configured `url`) |
| `rss_guid` | Item GUID |
| `rss_link` | Item link |
| `rss_enclosure_url` | Enclosure URL (if present) |
| `rss_enclosure_type` | Enclosure MIME type (if present) |

### Torrent fields (set when torrent namespace extensions are present)

| Field | Description |
|-------|-------------|
| `torrent_seeds` | Seeder count from torrent namespace extensions (nyaa, Jackett, ezrss, etc.) |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `source`, `title`, `rss_feed` |
| MayProduce | `description`, `published_date`, `rss_guid`, `rss_link`, `rss_enclosure_url`, `rss_enclosure_type`, `torrent_seeds` |
| MayProduce | `description`, `published_date`, `torrent_seeds` |
| Requires | — |

## Example

```python
src = input("rss", url="https://example.com/torrents/rss")
output("print", upstream=src)
pipeline("my-pipeline", schedule="1h")
```
