# quality

Rejects entries whose parsed quality does not match a configured spec.
Replaces the per-plugin `quality=` config knob that used to live on `series`,
`movies`, and `premiere`: place a `quality` node upstream of those filters
instead.

## Config

| Key | Required | Default | Description |
|---|---|---|---|
| `spec` | yes | — | Quality spec (see Spec syntax below) |
| `on_missing` | no | `pass` | `pass` ignores entries with no `_quality`; `reject` drops them |

## Spec syntax

| Spec | Meaning |
|---|---|
| `720p` | exact resolution |
| `720p+` | 720p or better |
| `720p-1080p` | inclusive range |
| `web` | source-only filter (any resolution) |
| `720p+ web` | combined dimensions |

See `internal/quality.ParseSpec` for the full grammar.

## DAG role

| Property | Value |
|---|---|
| Role | `processor` |
| Produces | — |
| Requires | `_quality` (typed struct set by `metainfo_file`) |

## Example

```python
src  = input("rss", url="https://feed.example.com/all")
meta = process("metainfo_file", upstream=src)
hd   = process("quality",       upstream=meta, spec="720p+")
ser  = process("series",        upstream=hd, static=["Breaking Bad"])
output("transmission", upstream=ser, host="localhost")
pipeline("tv")
```

Equivalent to the legacy `process("series", quality="720p+", …)` form, but the
quality decision is now visible as its own node and can be reused for movie or
mixed pipelines.
