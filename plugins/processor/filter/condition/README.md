# condition

Accepts or rejects entries using boolean expressions evaluated against entry fields. Useful for rules that don't fit a dedicated plugin — rating thresholds, genre exclusions, date comparisons, multi-condition logic.

## Expression syntax

Expressions use infix syntax. Identifiers resolve to entry fields; unknown fields return `""`.

```
video_rating >= 7.0
video_language != "English"
video_genres contains "Documentary"
series_first_air_date > daysago(365)
not (video_source == "CAM" or video_source == "TS")
```

**Operators:** `==`, `!=`, `<`, `<=`, `>`, `>=`, `contains`, `matches` (regex)  
**Logical:** `and` (`&&`), `or` (`||`), `not` (`!`)  
**Functions:** `now()`, `daysago(n)`, `weeksago(n)`, `monthsago(n)`, `date("YYYY-MM-DD")`

Go template syntax (`{{gt .field value}}`) is also accepted for more complex expressions.

## Config keys

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `accept` | conditional | — | Expression; entry accepted when it evaluates to true |
| `reject` | conditional | — | Expression; entry rejected when it evaluates to true |
| `rules` | conditional | — | Ordered list of `{accept, reject}` rule objects |

At least one of `accept`, `reject`, or `rules` must be present. Within a rule, `reject` is evaluated before `accept`.

## Config formats

### Single rule

```python
cond = process("condition", upstream=meta,
    reject='video_language != "" and video_language != "English"',
    accept="video_rating >= 7.0")
```

### Multiple rules

Use `rules` when you need more than one condition. Rules are evaluated in order; the first that fires terminates processing.

```python
cond = process("condition", upstream=meta, rules=[
    {"reject": 'video_language != "" and video_language != "English"'},
    {"reject": 'video_genres contains "Documentary"'},
    {"reject": 'video_genres contains "Reality"'},
    {"reject": 'series_first_air_date != "" and series_first_air_date < daysago(365)'},
    {"accept": "video_rating >= 7.0"},
])
```

## Example — TV series discovery filter

```python
src  = input("rss", url="https://example.com/rss")
meta = process("metainfo_tvdb", upstream=src, api_key=env("TVDB_KEY"))
cond = process("condition", upstream=meta, rules=[
    {"reject": 'video_language != "" and video_language != "English"'},
    {"accept": "video_rating >= 7.0"},
])
output("transmission", upstream=cond, host="localhost")
pipeline("filtered", schedule="1h")
```

## Example — rating gate

```python
cond = process("condition", upstream=meta,
    reject="video_rating < 6.5",
    accept="video_rating >= 7.0")
```

## Common pitfalls

### `accept` alone does not reject the rest

`accept` only **Accepts** matching entries; it does not Reject the non-matching ones — those pass through `Undecided`, which downstream nodes forward as-is. So a configuration like

```python
# WRONG: this does NOT mean "keep only entries with a 3D release"
cond = process("condition", upstream=meta,
    accept='bluray_3d_release == "true"')
```

leaks every entry that lacks `bluray_3d_release` straight to the downstream node, producing the surprising `in=80 out=80` shape in logs.

Use **`reject`** for exclusive filters, or pair the accept with a catch-all reject:

```python
# Option A — express the exclusion directly
cond = process("condition", upstream=meta,
    reject='bluray_3d_release != "true"')

# Option B — keep the accept, add an explicit catch-all
cond = process("condition", upstream=meta, rules=[
    {"accept": 'bluray_3d_release == "true"'},
    {"reject": 'true'},
])
```

### Comparing bool fields: prefer `!= true` over `== false`

Missing fields evaluate to the empty string `""`, not to a typed zero value. So `bluray_3d_release == false` does **not** match entries where the plugin never set the field — `compareEq` ends up doing `"" == "false"` (after string coercion), which is false, so the rule never fires.

```python
# WRONG: doesn't reject entries where bluray_3d_release was never set
reject="bluray_3d_release == false"
```

Compare against the truthy value instead — that way both "set to false" and "not set at all" trigger the rule:

```python
# RIGHT: rejects unless the field is exactly true
reject="bluray_3d_release != true"
```

The same pattern applies to any boolean field whose upstream plugin only sets it on the positive case (e.g. `video_is_3d`, `enriched`, `video_proper`).

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |
