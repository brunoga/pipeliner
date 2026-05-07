# metainfo_quality

Parses video quality tags from the entry title and annotates the entry with structured quality fields. Takes no config.

## Fields set on entry

### Standard VideoInfo fields

| Field | Example | Description |
|-------|---------|-------------|
| `video_quality` | `1080p BluRay x265` | Full human-readable quality string |
| `video_resolution` | `1080p` | Resolution tag |
| `video_source` | `BluRay` | Source tag |

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

## Example

```yaml
tasks:
  my-task:
    rss:
      url: "https://example.com/feed"
    metainfo_quality:
    condition:
      accept: '{{eq .video_resolution "1080p"}}'
```
