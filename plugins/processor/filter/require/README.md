# require

Rejects entries that are missing one or more required fields. A field is considered missing when its value is absent, empty string, zero, `false`, or an empty list.

A common use is to gate on the `enriched` field set by external metainfo plugins (TVDB, TMDb, Trakt), so entries that couldn't be identified are dropped early.

In addition to anything in the entry's metadata bag (`Fields`), four struct-field aliases also work: `title`, `url`, `original_url`, and `task`. They map to `Entry.Title`, `Entry.URL`, `Entry.OriginalURL`, and `Entry.Task` respectively, so `fields=["url"]` rejects entries whose `e.URL` is empty even though no plugin populates `Fields["url"]`. The Fields entry wins when both are set, which matches how the same names work in `condition` rules and notification templates.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `fields` | yes | — | Field name or list of field names that must be present and non-empty |

## Example — drop entries not identified by TVDB

```python
meta = process("metainfo_tvdb", upstream=upstream, api_key=env("TVDB_KEY"))
req  = process("require", upstream=meta, fields=["enriched"])
```

## Example — require episode metadata

```python
req = process("require", upstream=upstream, fields=["series_episode_id"])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | (the fields named in the `fields` config) |
