# rss

Fetches entries from an RSS 2.0 or Atom 1.0 feed. Prefers enclosure URLs (torrent feeds) and falls back to item links.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `url` | string | yes | — | Feed URL |
| `all_entries` | bool | no | false | Accept all entries, skipping filter phase |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `rss_feed` | Feed title |
| `rss_link` | Item link |
| `rss_description` | Item description |
| `rss_pubdate` | Publication date string |
| `rss_guid` | Item GUID |
| `rss_enclosure_url` | Enclosure URL (if present) |
| `rss_enclosure_type` | Enclosure MIME type (if present) |

## Example

```yaml
tasks:
  my-task:
    rss:
      url: "https://example.com/torrents/rss"
```
