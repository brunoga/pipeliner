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

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |
