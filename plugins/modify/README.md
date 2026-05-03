# Modify plugins

Modify plugins mutate entry fields in-place after metainfo annotation. They run on accepted entries only.

| Plugin | Description |
|--------|-------------|
| [pathfmt](pathfmt/README.md) | Render a pattern into the `download_path` field |
| [pathscrub](pathscrub/README.md) | Sanitize path components for safe filesystem use |
| [set](set/README.md) | Unconditionally set one or more entry fields |

## Pattern syntax

Both `pathfmt` and `set` render patterns against the entry's full field map using `{field}` syntax:

| Syntax | Meaning | Example |
|--------|---------|---------|
| `{field}` | Insert field value | `{series_name}` |
| `{field:format}` | Printf-formatted field | `{series_season:02d}` |

Go template syntax (`{{.field}}`) is also accepted for backward compatibility and is required for pipe expressions (`{{.date | slice 0 4}}`).

## Available fields

| Key | Value |
|-----|-------|
| `title` | Entry title |
| `url` | Entry URL |
| `task` | Task name |
| `download_path` | Previously rendered path (if `pathfmt` ran earlier) |
| `series_name`, `series_season`, … | Fields from `metainfo_series` |
| `tmdb_title`, `tmdb_genres`, … | Fields from `metainfo_tmdb` |
| `trakt_rating`, `trakt_genres`, … | Fields from `metainfo_trakt` |
| `tvdb_series_name`, `tvdb_air_date`, … | Fields from `metainfo_tvdb` |
