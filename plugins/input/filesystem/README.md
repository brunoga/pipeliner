# filesystem

Walks a local directory and emits one entry per file. Entry URLs use the `file://` scheme.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `path` | string | yes | — | Directory to scan |
| `recursive` | bool | no | false | Recurse into subdirectories |
| `mask` | string | no | — | Glob pattern to filter filenames (e.g. `*.torrent`) |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `location` | Absolute file path |
| `filename` | File name without directory |
| `extension` | File extension (without dot) |
| `size` | File size in bytes |
| `modified_time` | Last modified time (RFC3339) |

## Example

```yaml
tasks:
  watch-folder:
    filesystem:
      path: /downloads/watch
      mask: "*.torrent"
    metainfo_torrent:
    transmission:
      host: localhost
```
