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

```yaml
condition:
  reject: 'tvdb_language != "" and tvdb_language != "English"'
  accept: 'tmdb_vote_average >= 7.0'
```

Both `accept` and `reject` are optional; at least one must be present. Within a rule, `reject` is evaluated before `accept`.

### Multiple rules (`rules` list)

YAML does not allow duplicate keys — use `rules` when you need more than one condition:

```yaml
condition:
  rules:
    - reject: 'tvdb_language != "" and tvdb_language != "English"'
    - reject: 'tvdb_genres contains "Documentary"'
    - reject: 'tvdb_genres contains "Reality"'
    - reject: 'tvdb_first_air_date != "" and tvdb_first_air_date < daysago(365)'
    - accept: 'tmdb_vote_average >= 7.0'
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

```yaml
tasks:
  discover:
    - rss:
        url: "https://example.com/feed"
    - metainfo_tvdb:
        api_key: YOUR_KEY
    - condition:
        rules:
          - reject: 'tvdb_language != "" and tvdb_language != "English"'
          - reject: 'tvdb_genres contains "Documentary"'
          - reject: 'tvdb_genres contains "Reality"'
          - reject: 'tvdb_genres contains "Game Show"'
          - reject: 'tvdb_first_air_date != "" and tvdb_first_air_date < daysago(365)'
    - premiere:
        quality: 720p+ webrip+
```

## Example — rating gate

```yaml
condition:
  reject: 'tmdb_vote_average < 6.5'
  accept: 'tmdb_vote_average >= 7.0'
```
