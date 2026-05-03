# metainfo_quality

Parses video quality tags from the entry title and annotates the entry with structured quality fields. Takes no config.

## Fields set on entry

| Field | Example | Description |
|-------|---------|-------------|
| `quality` | `1080p.BluRay.x265` | Full quality string |
| `resolution` | `1080p` | Resolution tag |
| `source` | `BluRay` | Source tag |
| `codec` | `x265` | Codec tag |
| `audio` | `DTS` | Audio tag (if present) |
| `color_range` | `HDR` | Color range tag (if present) |

## Example

```yaml
tasks:
  my-task:
    rss:
      url: "https://example.com/feed"
    metainfo_quality:
    condition:
      accept: '{{eq .resolution "1080p"}}'
```
