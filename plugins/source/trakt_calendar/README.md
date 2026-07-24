# trakt_calendar

Emits one entry per upcoming episode (within `window`) from the authenticated
Trakt user's **my shows** calendar — the shows Trakt considers followed
(watching, collected, or watchlisted). The Trakt-native counterpart to
[`tvdb_calendar`](../tvdb_calendar/README.md), which is driven by the local
series tracker instead.

Requires OAuth: either `client_secret` (uses the token stored by
`pipeliner auth trakt`) or a static `access_token`.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `client_id` | yes | — | Trakt API client ID |
| `client_secret` | one of | — | OAuth secret; uses the stored token |
| `access_token` | one of | — | Static bearer token |
| `window` | no | `24h` | How far ahead to look |

## Fields set on each entry

`title` (`<Show> SxxEyy`), `media_type=series`, `series_episode_id`/`season`/
`episode`, `series_air_date` (`time.Time`), `description` (episode name),
plus `tvdb_id`/`trakt_id` when Trakt provides them.
