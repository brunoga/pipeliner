# tvdb_calendar

Emits one entry per upcoming episode (airing within `window`) for every show
in the series tracker, using TheTVDB air dates. Shows deactivated by
`series_tracker_update` are skipped unless `include_inactive=True`.

Feed it into `notify` for a "tonight's episodes" message, or into `discover`
for day-of searching.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `api_key` | yes | — | TheTVDB v4 API key |
| `window` | no | `24h` | How far ahead to look |
| `ttl` | no | `6h` | TVDB lookup cache TTL |
| `include_inactive` | no | false | Include deactivated shows |

## Fields set on each entry

| Field | Description |
|-------|-------------|
| `title` | `<Show> SxxEyy` |
| `media_type` | `series` |
| `series_name` | Normalized tracker name |
| `series_episode_id` / `series_season` / `series_episode` | Episode identifiers |
| `series_air_date` | Air date (`time.Time`) |
| `description` | Episode name, when TheTVDB has one |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | title, media_type, series_name, series_episode_id, series_season, series_episode, series_air_date |
