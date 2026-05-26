# rss

Fetches entries from an RSS 2.0 or Atom 1.0 feed. Use `url=` for a fixed passive source or `url_template=` as a `discover.search` backend. Both modes share identical parsing, retry, and field-extraction logic.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url` | conditional | — | Fixed feed URL (required unless `url_template` is set) |
| `url_template` | conditional | — | URL with `{Query}` or `{QueryEscaped}` placeholder for search mode |
| `timeout` | no | `30s` | HTTP request timeout |

Exactly one of `url` or `url_template` is required. Template variables: `{Query}` (raw), `{QueryEscaped}` (URL-encoded). Go template syntax `{{.Query}}` / `{{.QueryEscaped}}` also works.

## Fields set on entry

Fields marked **always** are set on every entry. All others depend on the feed content.

| Field | Always | Description |
|-------|--------|-------------|
| `source` | ✓ | Origin in the form `rss:<hostname>` (e.g. `rss:nyaa.si`) |
| `title` | ✓ | Item title |
| `rss_feed` | ✓ | The URL used to fetch this batch of entries |
| `description` | | Item description or summary |
| `published_date` | | Publication date (from `pubDate`; falls back to `dc:date`) |
| `rss_guid` | | Item GUID |
| `rss_link` | | Item `<link>` element value |
| `rss_enclosure_url` | | Enclosure URL (if present) |
| `rss_enclosure_type` | | Enclosure MIME type (if present) |
| `rss_category` | | Category from `<category>` elements or `nyaa:category`, comma-joined |
| `torrent_seeds` | | Seeder count (nyaa, Jackett, ezrss, and other torrent namespaces) |
| `torrent_leechers` | | Leecher count (nyaa namespace) |
| `torrent_info_hash` | | SHA-1 info hash, lowercase hex (nyaa namespace) |
| `torrent_grabs` | | Download count from the indexer (nyaa namespace) |

**URL priority**: enclosure > `media:content` > link > GUID.

## Example — passive source

```python
src = input("rss", url="https://nyaa.si/?page=rss&c=1_2&f=0")
seen    = process("seen", upstream=src)
meta   = process("metainfo_file", upstream=seen)
series  = process("series", upstream=meta, tracking="strict",
    list=[{"name": "trakt_list", "client_id": env("TRAKT_ID"),
           "type": "shows", "list": "watchlist"}])
output("transmission", upstream=series, host="localhost")
pipeline("nyaa-tv", schedule="1h")
```

## Example — search backend for discover

```python
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"), type="movies", list="watchlist")
results   = process("discover", upstream=watchlist,
    search=[{"name": "rss", "url_template": "https://nyaa.si/?page=rss&q={QueryEscaped}&c=1_2&f=0"}],
    interval="12h")
output("transmission", upstream=results, host="localhost")
pipeline("nyaa-movies", schedule="2h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| IsSearchPlugin | `true` |
| Produces | `source`, `title`, `rss_feed` |
| MayProduce | `description`, `published_date`, `rss_guid`, `rss_link`, `rss_enclosure_url`, `rss_enclosure_type`, `rss_category`, `torrent_seeds`, `torrent_leechers`, `torrent_info_hash`, `torrent_grabs` |
| Requires | — |

## Notes

- Transient failures (network errors, HTTP 5xx) are retried up to 3 times with backoff.
- Response bodies are limited to 10 MB.
- `dc:date` is used as a `published_date` fallback when `<pubDate>` is absent.
- Atom entries without any `<link>` elements fall back to the `<id>` IRI.
