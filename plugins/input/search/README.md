# Search plugins

Search plugins are sub-plugins used exclusively by the [`discover`](../discover/README.md) input plugin via its `via` config key. They are not used directly as task-level plugins.

| Plugin | Description |
|--------|-------------|
| [jackett](jackett/) | Search Jackett indexers via the Torznab API |
| [search_rss](rss/) | Fetch search results from a parameterized RSS/Atom URL |

## jackett

Queries a [Jackett](https://github.com/Jackett/Jackett) proxy via the Torznab API. Seeder/leecher counts, info hashes, and file sizes are returned in the search response — no separate `metainfo_torrent` or `metainfo_magnet` fetch is needed.

See the full [jackett README](jackett/README.md) for config options and field reference.

### Example

```yaml
discover:
  from: series
  via:
    - jackett:
        url: "http://localhost:9117"
        api_key: "YOUR_JACKETT_KEY"
        indexers: [all]
        categories: [5000, 5030]
  interval: 12h
```

## search_rss

Constructs a search URL by substituting the query into a URL pattern, fetches the resulting RSS/Atom feed, and returns the items as entries. Use this when you have a tracker or aggregator with a simple search RSS endpoint. For Jackett specifically, the dedicated `jackett` plugin is preferred because it parses Torznab attributes natively.

### Config

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `url_template` | string | yes | URL pattern. Use `{Query}` for the raw query or `{QueryEscaped}` for URL-encoded. Go template syntax (`{{.QueryEscaped}}`) is also accepted. |

### Example

```yaml
discover:
  from: series
  via:
    - search_rss:
        url_template: "https://some-tracker.example.com/rss?q={QueryEscaped}"
  interval: 6h
```
