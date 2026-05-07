# exists

Rejects entries whose target file already exists on disk. Compares normalized filenames (case-insensitive, ignoring separators and extensions) against files in the configured directory.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `path` | string | yes | — | Directory to check |
| `recursive` | bool | no | false | Check subdirectories recursively |

## Example

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/feed"
    exists:
      path: /media/movies
      recursive: true
    movies:
      static: ["Inception"]
```
