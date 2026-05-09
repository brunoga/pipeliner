# filesystem

Walks a local directory and emits one entry per file. Entry URLs use the `file://` scheme.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `path` | string | yes | — | Directory to scan |
| `recursive` | bool | no | false | Recurse into subdirectories |
| `mask` | string | no | — | Glob pattern to filter filenames (e.g. `*.torrent`) |

## Fields set on entry

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | File name (same as `file_name`) |
| `file_name` | string | File name including extension |
| `file_extension` | string | File extension including the leading dot (e.g. `.torrent`) |
| `file_location` | string | Absolute file path |
| `file_size` | int64 | File size in bytes |
| `file_modified_time` | time.Time | Last modified time |

## Example

```yaml
tasks:
  watch-folder:
    - filesystem:
        path: /downloads/watch
        mask: "*.torrent"
    - metainfo_torrent:
    - transmission:
        host: localhost
```
