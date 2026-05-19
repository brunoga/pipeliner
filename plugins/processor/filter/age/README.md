# age

Filter processor that rejects entries based on the age of a date field.

## Config keys

| Key | Type | Default | Description |
|---|---|---|---|
| `field` | string | `"published_date"` | Entry field to read the date from |
| `newer_than` | string | — | Reject entries older than this duration (e.g. `"7d"`, `"2w"`, `"24h"`) |
| `older_than` | string | — | Reject entries newer than this duration (useful for "wait 30 days for quality releases") |
| `on_missing` | string | `"pass"` | What to do when the field is absent or unparseable: `"pass"` or `"reject"` |

At least one of `newer_than` or `older_than` must be set.

## Duration format

Durations accept Go's standard suffixes (`h`, `m`, `s`) plus:
- `d` — days (24h each)
- `w` — weeks (7 × 24h each)

Examples: `"7d"`, `"2w"`, `"168h"`, `"30d"`.

## Example

```python
# Keep only entries published in the last 7 days.
process("age", upstream=rss_node, newer_than="7d")

# Wait at least 30 days before accepting (quality release filter).
process("age", upstream=rss_node, older_than="30d")

# Reject entries with no date field instead of passing them.
process("age", upstream=rss_node, newer_than="7d", on_missing="reject")

# Use a custom date field.
process("age", upstream=rss_node, newer_than="14d", field="series_episode_air_date")
```
