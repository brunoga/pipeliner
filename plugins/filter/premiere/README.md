# premiere

Accepts premiere episodes of previously unseen series. Useful for discovering
new shows: once a premiere is downloaded, subsequent runs reject that series so
it is not re-triggered (other filters such as `series` take over episode tracking).

**Multiple quality variants** of the same premiere are all accepted so the task
engine's automatic deduplication can pick the best copy (highest resolution among
torrents with 2+ seeds, falling back to most seeds). The series is marked seen in
the Learn phase — only the winning copy is recorded.

State is persisted in `pipeliner.db` in the same directory as the config file.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `episode` | int | no | `1` | Episode number to treat as premiere |
| `season` | int | no | `1` | Season number to match; `0` means any season |
| `quality` | string | no | — | Quality spec the entry must satisfy (e.g. `720p+`, `webrip+`) |

Episode metadata is parsed directly from the entry title — `metainfo_series` is
not required. The `series_name`, `series_season`, `series_episode`, and
`series_episode_id` fields are set on the entry for use by downstream plugins.
Entries whose titles do not parse as a series episode are left undecided.

See [`quality`](../quality/README.md) for the spec syntax.

## Example

```yaml
tasks:
  discover-shows:
    rss:
      url: "https://example.com/rss"
    premiere:
      quality: 720p+ webrip+
    deluge:
      path: /downloads/tv
```
