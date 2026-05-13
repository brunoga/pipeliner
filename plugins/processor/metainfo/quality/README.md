# metainfo_quality

Parses video quality tags from the entry title and annotates the entry with structured quality fields. Takes no config.

## Fields set on entry

### Standard VideoInfo fields

| Field | Example | Description |
|-------|---------|-------------|
| `video_quality` | `BD3D 1080p BluRay x265` | Full human-readable quality string, including 3D format when present |
| `video_resolution` | `1080p` | Resolution tag |
| `video_source` | `BluRay` | Source tag |
| `video_is_3d` | `true` | `true` when any 3D format marker is detected in the title |

### Extended quality fields (set only when present in the title)

| Field | Example | Description |
|-------|---------|-------------|
| `codec` | `x265` | Codec tag |
| `audio` | `DTS` | Audio tag |
| `color_range` | `HDR` | Color range tag |

### Numeric comparison fields (always set)

| Field | Example | Description |
|-------|---------|-------------|
| `quality_resolution` | `1080` | Numeric resolution value for comparisons |
| `quality_source` | `3` | Numeric source rank for comparisons |

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `video_quality`, `video_resolution`, `video_source`, `video_is_3d`, `codec`, `audio`, `color_range`, `quality_resolution`, `quality_source` |
| Requires | — |

## Example

```python
src     = input("rss", url="https://example.com/feed")
quality = process("metainfo_quality", upstream=src)
output("print", upstream=quality)
pipeline("my-pipeline", schedule="1h")
```
