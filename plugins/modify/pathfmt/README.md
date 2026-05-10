# pathfmt

Renders a path template into a named entry field. After rendering, each path component is automatically scrubbed — characters invalid on common filesystems (`<>:"/\|?*` and control characters) are replaced with underscores, so no separate scrubbing step is needed.

## Config

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `path` | string | yes | Template rendered against entry fields |
| `field` | string | yes | Entry field to write the result into |

## Template syntax

| Syntax | Description | Example |
|--------|-------------|---------|
| `{field}` | Field value | `{title}` |
| `{field:format}` | Printf-formatted | `{series_season:02d}` |
| `{{.field}}` | Go template | `{{.title}}` |

All entry fields are available, plus `.Title` (raw entry title), `.URL`, `.Task`, and `.OriginalURL`.

## Example

```python
plugin("pathfmt",
    path="/media/tv/{title}/Season {series_season:02d}",
    field="download_path",
)
```

A title like `"Breaking Bad: Season 1"` is scrubbed to `"Breaking Bad_ Season 1"` — the colon is invalid on Windows and replaced with an underscore.

Output plugins (`deluge`, `qbittorrent`, `transmission`, `download`) read the `download_path` field set here. To write to a different field, specify `field` accordingly.
