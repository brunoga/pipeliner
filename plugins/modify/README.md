# Modify plugins

Modify plugins mutate entry fields in-place after metainfo annotation. They run on accepted entries only.

| Plugin | Description |
|--------|-------------|
| [pathfmt](pathfmt/README.md) | Render a path pattern into a named field, with automatic scrubbing |
| [set](set/README.md) | Unconditionally set one or more entry fields |

## Pattern syntax

Both `pathfmt` and `set` render patterns against the entry's full field map:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `{field}` | Insert field value | `{title}` |
| `{field:format}` | Printf-formatted field | `{series_season:02d}` |

Go template syntax (`{{.field}}`) is also accepted for backward compatibility and is required for pipe expressions (`{{.date | slice 0 4}}`).

## Available fields

| Key | Value |
|-----|-------|
| `title` | Canonical enriched title (from TVDB/TMDb/Trakt) |
| `raw_title` | Original entry title (torrent filename or feed item) |
| `url` | Entry URL |
| `task` | Task name |
| `download_path` | Previously rendered path (if `pathfmt` ran earlier) |
| `series_season`, `series_episode_id`, … | Standard series fields |
| `video_year`, `video_genres`, … | Standard video fields |
| `tvdb_id`, `tmdb_id`, … | Provider-specific IDs |
