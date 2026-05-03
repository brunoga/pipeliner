# Search plugins

Search plugins are sub-plugins used exclusively by the [`discover`](../discover/README.md) input plugin via its `via` config key. They are not used directly as task-level plugins.

| Plugin | Description |
|--------|-------------|
| [search_rss](rss/) | Fetch search results from a parameterized RSS/Atom URL |

## search_rss

Constructs a search URL by substituting the query into a URL pattern, fetches the resulting RSS/Atom feed, and returns the items as entries.

### Config

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `url_template` | string | yes | URL pattern. Use `{Query}` for the raw query or `{QueryEscaped}` for URL-encoded. Go template syntax (`{{.QueryEscaped}}`) is also accepted. |

### Example

```yaml
discover:
  from:
    - name: input_trakt
      client_id: YOUR_CLIENT_ID
      type: movies
      list: watchlist
  via:
    - name: search_rss
      url_template: "https://jackett.example.com/api/v2.0/indexers/all/results/torznab?q={QueryEscaped}&apikey=YOUR_KEY"
  interval: 6h
  db: pipeliner.db
```
