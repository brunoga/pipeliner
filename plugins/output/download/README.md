# download

Downloads each accepted entry's URL to a local file. Uses an atomic write (temp file then rename) to avoid partial files.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `path` | string | yes | — | Local directory to download into |
| `filename` | string | no | `{{.url_basename}}` | Filename template |

## Template context

In addition to all entry fields, the `filename` template has access to `url_basename` (the last path segment of the URL) and `timestamp` (current Unix timestamp).

## Fields set on entry

`download_path` — absolute path of the downloaded file.

## Error handling

If the download fails (network error, non-200 response, disk error), the entry is marked failed and will **not** be recorded by the learn phase. It will be retried on the next run.

## Example

```python
task("articles", [
    plugin("rss", url="https://example.com/feed"),
    plugin("regexp", accept="(?i)golang"),
    plugin("download",
        path="/home/user/articles",
        filename="{{.timestamp}}-{{.url_basename}}",
    ),
])
```
