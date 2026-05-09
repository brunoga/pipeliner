# rss

Fetches entries from an RSS 2.0 or Atom 1.0 feed. Prefers enclosure URLs (torrent feeds) and falls back to item links.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Feed URL |
| `all_entries` | bool | no | false | Accept all entries, skipping filter phase |

## Fields set on entry

### Generic fields

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | Item title |
| `description` | string | Item description or summary |
| `published_date` | string | Publication date string |

### RSS fields

| Field | Type | Description |
|-------|------|-------------|
| `rss_feed` | string | Feed URL (the configured `url`) |
| `rss_guid` | string | Item GUID |
| `rss_link` | string | Item link |
| `rss_enclosure_url` | string | Enclosure URL (if present) |
| `rss_enclosure_type` | string | Enclosure MIME type (if present) |

### Torrent fields (when torrent namespace extensions are present)

| Field | Type | Description |
|-------|------|-------------|
| `torrent_seeds` | int | Seeder count from torrent namespace extensions (nyaa, Jackett, ezrss, etc.) |

## Example

```yaml
tasks:
  my-task:
    - rss:
        url: "https://example.com/torrents/rss"
```
