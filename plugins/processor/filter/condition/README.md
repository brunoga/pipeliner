# condition

Accepts or rejects entries using boolean expressions evaluated against the entry's field map. Useful for rules that don't fit a dedicated plugin — score thresholds, genre exclusions, date comparisons, multi-condition logic.

## Expression syntax

Expressions use infix syntax. Identifiers resolve to entry fields; unknown fields return `""`.

```
tmdb_vote_average >= 7.0
tvdb_language != "English"
tvdb_genres contains "Documentary"
tvdb_first_air_date > daysago(365)
not (source == "CAM" or source == "TS")
```

**Operators:** `==`, `!=`, `<`, `<=`, `>`, `>=`, `contains`, `matches` (regex)  
**Logical:** `and` (`&&`), `or` (`||`), `not` (`!`)  
**Functions:** `now()`, `daysago(n)`, `weeksago(n)`, `monthsago(n)`, `date("YYYY-MM-DD")`

Go template syntax (`{{gt .field value}}`) is also accepted for backward compatibility.

## Config formats

### Single rule

```python
plugin("condition",
    reject='tvdb_language != "" and tvdb_language != "English"',
    accept="tmdb_vote_average >= 7.0",
)
```

Both `accept` and `reject` are optional; at least one must be present. Within a rule, `reject` is evaluated before `accept`.

### Multiple rules (`rules` list)

Use `rules` when you need more than one condition:

```python
plugin("condition", rules=[
    {"reject": 'tvdb_language != "" and tvdb_language != "English"'},
    {"reject": 'tvdb_genres contains "Documentary"'},
    {"reject": 'tvdb_genres contains "Reality"'},
    {"reject": 'tvdb_first_air_date != "" and tvdb_first_air_date < daysago(365)'},
    {"accept": "tmdb_vote_average >= 7.0"},
])
```

Rules are evaluated in order; the first one that fires terminates processing. `reject` takes precedence over `accept` within the same rule.

## Config keys

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `accept` | string | conditional | Expression; entry accepted when it evaluates to true |
| `reject` | string | conditional | Expression; entry rejected when it evaluates to true |
| `rules` | list | conditional | Ordered list of `{accept, reject}` rule objects |

## Reject reason

When a `reject` expression fires, the reject reason is set to the expression itself, e.g.:

```
reason="condition: tvdb_genres contains \"Documentary\""
```

## Template context

`.Title`, `.URL`, `.OriginalURL`, `.Task`, and all entry fields set by input, metainfo, filter, and modify plugins.

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
plugin("condition",
    reject="tmdb_vote_average < 6.5",
    accept="tmdb_vote_average >= 7.0",
)
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |
