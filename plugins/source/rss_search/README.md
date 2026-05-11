# rss_search

A from plugin that fetches entries from a parameterized RSS URL. The URL is
constructed by rendering a Go template with the search query substituted for
`{{.Query}}` or `{{.QueryEscaped}}`.

This plugin is used as a sub-plugin of [`discover`](../../processor/discover/) via
the `via` config key. It cannot be used directly as a task-level plugin.

**This plugin is a PhaseFrom sub-plugin.** Use it inside `discover.via`.

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
task("discover-tv", [
    plugin("discover", **{
        "titles": ["Breaking Bad"],
        "via": [
            {"name": "rss_search",
             "url_template": "https://jackett.example.com/api/v2.0/indexers/all/results/torznab/api?t=search&q={{.QueryEscaped}}&apikey=" + env("JACKETT_API_KEY")},
        ],
        "interval": "6h",
    }),
])
```

## DAG role

`rss_search` keeps `PhaseFrom` so it continues to work inside `discover.via`. Its `Role` is `source`, which means it can also be used as a standalone `input()` node in DAG pipelines (it will call the URL template with an empty query, returning all recent results):

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `rss_feed`, `rss_guid`, `rss_link`, `rss_enclosure_url`, `rss_enclosure_type`, `published_date`, `torrent_seeds` |
| Requires | — |
