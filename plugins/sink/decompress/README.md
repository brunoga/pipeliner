# decompress

Extracts RAR, ZIP, and 7z archives to a destination directory. Reads the archive path from the `file_location` field (set by `download` or `filesystem`), or falls back to the entry URL when it ends in `.rar`, `.zip`, or `.7z`.

Requires `unrar`, `7z`, or `unar` on `$PATH`. The first found is used; `tool` overrides the selection. The plugin fails at startup if none is found.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `to` | yes | — | Destination directory for extracted files |
| `keep_dirs` | no | `true` | Preserve the archive's internal directory structure |
| `delete_archive` | no | `false` | Remove the archive (and split RAR parts) after successful extraction |
| `tool` | no | auto | Force a specific tool: `unrar`, `7z`, or `unar` |

## Example

```python
src = input("filesystem", path="/downloads/completed", mask="*.rar")
output("decompress", upstream=src, to="/media/extracted")
pipeline("extract", schedule="5m")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `file_location` (set by `download` or `filesystem`) |
