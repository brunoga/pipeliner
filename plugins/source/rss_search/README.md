# rss_search

Fetches entries from a parameterized RSS URL, substituting the search query into the URL template. Used as a search backend for the [`discover`](../../processor/discover/README.md) plugin, and can also be used as a standalone `input()` source node (queries with an empty string).

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `url_template` | yes | — | URL with `{Query}` or `{QueryEscaped}` placeholder |

**Template variables:**
- `{Query}` — raw query string (not URL-encoded)
- `{QueryEscaped}` — URL-encoded query string (safe for query parameters)

Go template syntax (`{{.Query}}`, `{{.QueryEscaped}}`) is also accepted.

## Example — as a discover search backend

```python
watchlist = input("trakt_list", client_id=env("TRAKT_ID"),
                  client_secret=env("TRAKT_SECRET"),
                  type="movies", list="watchlist")
results   = process("discover", upstream=watchlist,
    search=[{"name": "rss_search",
             "url_template": "https://example.com/search?q={QueryEscaped}&apikey=" + env("KEY")}],
    interval="12h")
output("qbittorrent", upstream=results, host="localhost")
pipeline("movie-discover", schedule="2h")
```

## Example — as a standalone source

```python
src = input("rss_search",
            url_template="https://nyaa.si/?page=rss&q={QueryEscaped}&c=1_2&f=0")
output("print", upstream=src)
pipeline("nyaa-browse", schedule="1h")
```

## Fields set on entry

| Field | Description |
|-------|-------------|
| `source` | Origin in the form `rss_search:<hostname>` (e.g. `rss_search:nyaa.si`) |
| `title` | Item title |
| `rss_feed` | Rendered search URL used to fetch this batch of results |
| `description` | Item description or summary (if present) |
| `published_date` | Publication date string (if present) |
| `rss_guid` | Item GUID (if present) |
| `rss_link` | Item link (if present) |
| `rss_enclosure_url` | Enclosure URL (if present) |
| `rss_enclosure_type` | Enclosure MIME type (if present) |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `source`, `title`, `rss_feed` |
| MayProduce | `description`, `published_date`, `rss_guid`, `rss_link`, `rss_enclosure_url`, `rss_enclosure_type` |
| Requires | — |
