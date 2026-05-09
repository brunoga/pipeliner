# require

Rejects entries that are missing one or more required metadata fields.

A field is considered missing if its value is `nil`, an empty string, zero
integer, zero `time.Time`, `false`, or an empty slice.

## Config

```yaml
require:
  fields: series_name          # single field
  # or
  fields:
    - series_name
    - series_season
    - quality
```

## Example

```yaml
tasks:
  tv:
    - rss:
        url: "https://example.com/rss"
    - metainfo_series:
    - metainfo_quality:
    - require:
        fields:
          - series_name
          - quality
    - series:
        static:
          - "Breaking Bad"
```
