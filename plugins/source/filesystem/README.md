# filesystem

Walks a local directory and emits one entry per file. Entry URLs use the `file://` scheme.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `path` | yes | — | Directory to scan |
| `recursive` | no | `false` | Recurse into subdirectories |
| `mask` | no | — | Glob pattern to filter filenames (e.g. `*.torrent`) |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `title` | File name (same as `file_name`) |
| `file_name` | File name including extension |
| `file_extension` | File extension including the leading dot (e.g. `.torrent`) |
| `file_location` | Absolute file path |
| `file_size` | File size in bytes |
| `file_modified_time` | Last-modified timestamp |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `title`, `file_name`, `file_extension`, `file_location`, `file_size`, `file_modified_time` |
| Requires | — |

## Example

```python
src   = input("filesystem", path="/downloads/watch", mask="*.torrent")
meta  = process("metainfo_torrent", upstream=src)
output("transmission", upstream=meta, host="localhost")
pipeline("watch-folder", schedule="5m")
```
