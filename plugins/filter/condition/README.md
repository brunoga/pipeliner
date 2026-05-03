# condition

Accepts or rejects entries using Go template expressions evaluated against the entry's field map. Useful for rules that don't fit a dedicated plugin, such as score thresholds, genre exclusions, or multi-condition logic.

## Config formats

### Single rule

Both `accept` and `reject` are optional top-level keys; at least one must be present. Within a rule, `reject` is evaluated before `accept`.

```yaml
condition:
  accept: '{{gt .tmdb_vote_average 7.0}}'
  reject: '{{eq .source "CAM"}}'
```

### Multiple rules (`rules` list)

When you need more than one condition block in a task — YAML does not allow duplicate keys — put all conditions in one `condition` block using the `rules` list. Rules are evaluated in order; the first rule that fires terminates processing.

```yaml
condition:
  rules:
    - reject: '{{eq .source "CAM"}}'
    - reject: '{{lt .tmdb_vote_average 6.0}}'
    - accept: '{{gt .tmdb_vote_average 8.0}}'
```

Within each rule, `reject` is checked before `accept`.

## Config keys

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `accept` | string | conditional | Template; entry accepted when it renders truthy |
| `reject` | string | conditional | Template; entry rejected when it renders truthy |
| `rules` | list | conditional | Ordered list of `{accept, reject}` rule objects |

Use either the single-rule format (`accept`/`reject` at top level) or `rules`; they cannot be mixed.

## Template context

The data map contains `.Title`, `.URL`, `.OriginalURL`, `.Task`, and all entry fields written by filter, metainfo, and modify plugins.

A template output is truthy when it is non-empty and not equal (case-insensitive) to `"false"` or `"0"`.

## Template functions

All functions from the internal template package are available, including `daysago`, `before`, `after`, `contains`, `lower`, `upper`, `join`, `slice`, and `replace`.

## Example — single rule

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/rss/movies"
    metainfo_tmdb:
      api_key: YOUR_TMDB_KEY
    condition:
      accept: '{{gt .tmdb_vote_average 7.0}}'
      reject: '{{eq .source "CAM"}}'
    qbittorrent:
      host: localhost
```

## Example — multi-rule

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/rss/movies"
    metainfo_tmdb:
      api_key: YOUR_TMDB_KEY
    condition:
      rules:
        - reject: '{{eq .source "CAM"}}'
        - reject: '{{lt .tmdb_vote_average 6.0}}'
        - accept: '{{gt .tmdb_vote_average 8.0}}'
    qbittorrent:
      host: localhost
```
