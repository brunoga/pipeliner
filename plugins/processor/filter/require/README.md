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
meta = process("metainfo_tvdb", upstream=upstream, api_key=env("TVDB_KEY"))
req  = process("require", upstream=meta, fields=["enriched"])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — (dynamic: whatever fields are listed in `fields` config) |
