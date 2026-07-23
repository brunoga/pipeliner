# series_tracker_update

Flips a tracked show's **inactive** flag in the series tracker. Deactivated shows are rejected early by the [`series`](../../processor/filter/series/README.md) filter with `series: <show> inactive (<reason>)` — before any quality or tracker checks — so pipelines stop spending work on shows that are over. Reactivating removes the flag.

The flag lives in a store bucket shared across **all** tasks (`series_inactive`, keyed by normalized show name), so a deactivation performed by one pipeline applies to every pipeline's `series` filter. The per-episode download records are untouched — reactivating restores the show exactly where it left off.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `action` | string | yes | — | `deactivate` or `reactivate` |
| `reason` | string | no | entry's `series_lifecycle` value, else `deactivated` | Reason recorded on deactivation; echoed in the series filter's rejection message |

## Behavior

- Requires `series_name` (the normalized tracker key, as emitted by [`series_tracker`](../../source/series_tracker/README.md)); entries without it are marked failed.
- Dry-run logs "would deactivate/reactivate" previews and makes **no** store writes.
- On success entries get an accept reason (`deactivated <show> (<reason>)`), so a chained `notify` sink fires only after the tracker write actually happened.

## Example

```python
# Deactivate complete shows and confirm via notify (chained sink: the
# notification only fires when the tracker write succeeded).
deact = output("series_tracker_update", upstream=routes.complete,
               action="deactivate")
output("notify", upstream=deact, via="pushover",
       config={"user": pushover_user, "token": pushover_token},
       body="{{range .Entries}}Series complete: {{.Title}}\n{{end}}")
```

To undo, run a pipeline with `action="reactivate"` (e.g. from a manual-trigger pipeline fed by `series_tracker` filtered to the show in question).

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `series_name` |
