# pathfmt

Renders a Go template string into the `download_path` entry field. Output plugins that accept a `path` config key use this field by default.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `path` | string | yes | — | Go template rendered against entry fields |

## Template context

All entry fields plus `.Title`, `.URL`, `.Task`, and `.OriginalURL`.

## Example

```yaml
tasks:
  tv:
    series:
      shows: ["Breaking Bad"]
      db: pipeliner.db
    metainfo_series:
    pathfmt:
      path: "/media/tv/{{.series_name}}/Season {{printf \"%02d\" .series_season}}"
    transmission:
      host: localhost
      path: "{{.download_path}}"
```

The `printf "%02d"` zero-pads the season number. Go's `text/template` functions are available: `printf`, `len`, `index`, `slice`, and the standard sprig-style helpers if registered.
