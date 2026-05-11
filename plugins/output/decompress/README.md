# decompress

Extracts RAR, ZIP, and 7z archives to a destination directory using system
tools. No CGo or external Go libraries are required.

The archive path is taken from the entry's `location` field if set, otherwise
from the entry URL when it ends with `.rar`, `.zip`, or `.7z`.

## Config

```python
plugin("decompress",
    to="/data/extracted",          # destination directory (required)
    keep_dirs=True,                # preserve internal directory structure (default: true)
    delete_archive=False,          # remove archive after successful extraction (default: false)
    tool="unrar",                  # force a specific tool: "unrar", "7z", or "unar" (default: auto)
)
```

**Tool selection:** `unrar` → `7z` → `unar` in order of preference. The first
tool found on `$PATH` is used. When `delete_archive=True`, split RAR parts
(`.r00`–`.r99`) are also removed.

## Requirements

At least one of `unrar`, `7z`, or `unar` must be installed and on `$PATH`.
The plugin fails at startup if none is found.

## Example

```python
task("scene", [
    plugin("rss", url="https://example.com/rss"),
    plugin("download", to="/downloads/staging"),
    plugin("decompress", to="/downloads/completed", delete_archive=True),
])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `file_location` (set by `download` or `filesystem`) |
