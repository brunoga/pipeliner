# require

Rejects entries that are missing one or more required metadata fields.

A field is considered missing if its value is `nil`, an empty string, zero
integer, zero `time.Time`, `false`, or an empty slice.

## Config

```python
plugin("require", fields="series_name")          # single field
# or
plugin("require", fields=["series_name", "series_season", "quality"])
```

## Example

```python
task("tv", [
    plugin("rss", url="https://example.com/rss"),
    plugin("metainfo_series"),
    plugin("metainfo_quality"),
    plugin("require", fields=["series_name", "quality"]),
    plugin("series", static=["Breaking Bad"]),
])
```
