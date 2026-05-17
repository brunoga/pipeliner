# rss_search

A from plugin that fetches entries from a parameterized RSS URL. The URL is
constructed by rendering a Go template with the search query substituted for
`{{.Query}}` or `{{.QueryEscaped}}`.

It is used as a sub-plugin of [`discover`](../../processor/discover/) via
the `via` config key, and can also be used as a standalone `input()` source node.

## Config

```python
# inline in discover via list:
{"name": "rss_search", "url_template": "https://example.com/search?q={{.QueryEscaped}}"}
```

| Key            | Description                                           |
|----------------|-------------------------------------------------------|
| `url_template` | Go template for the search URL (required)             |

**Template variables:**
- `{{.Query}}` — raw query string (not URL-encoded; use only for path segments)
- `{{.QueryEscaped}}` — URL-encoded query string (safe for query parameters)

## Example

```python
src = process("discover",
    titles=["Breaking Bad"],
    search=[{"name": "rss_search",
             "url_template": "https://jackett.example.com/api/v2.0/indexers/all/results/torznab/api?t=search&q={{.QueryEscaped}}&apikey=" + env("JACKETT_API_KEY")}],
    interval="6h",
)
```

## Role

`rss_search` has `Role=source`. It is used inside `discover.search` for targeted searches, and can also be used as a standalone `input()` node (it will call the URL template with an empty query, returning all recent results):

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `rss_feed`, `rss_guid`, `rss_link`, `rss_enclosure_url`, `rss_enclosure_type`, `published_date`, `torrent_seeds` |
| Requires | — |
