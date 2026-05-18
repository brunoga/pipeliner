# route

Routes entries to named legs based on ordered boolean conditions. The first matching leg accepts the entry and stamps `_route_leg` on it. Entries that match no leg are rejected with a warning log so nothing is silently dropped.

The `route()` Starlark builtin creates the route node and returns a handle object whose attributes are the individual leg handles:

```python
routes = route(upstream,
    series = "series_episode_id != ''",
    movies = "series_episode_id == ''")
```

Each leg attribute is a node handle that can be used as `upstream=` for subsequent processors.

## Why use route instead of separate condition filters?

`condition` filters achieve the same routing effect, but they apply independently — an entry rejected by leg A is still processed by leg B, and a merge downstream produces soft `~` warnings because the validator sees two branches with different field sets.

`route()` declares the branches are **mutually exclusive**: exactly one leg matches each entry. The DAG validator uses union semantics at merge points (no warnings), and unmatched entries are explicitly rejected with a clear log message.

## Full example

```python
src    = input("rss", url="https://example.com/feed")
meta   = process("metainfo_quality", upstream=src)

routes = route(meta,
    series = "series_episode_id != ''",
    movies = "series_episode_id == ''")

# Series branch
meta_s  = process("metainfo_series",    upstream=routes.series)
series  = process("series", upstream=meta_s, list=[{"name": "tvdb_favorites", ...}])

# Movies branch
meta_m  = process("metainfo_tmdb",      upstream=routes.movies, api_key=env("TMDB_KEY"))
movies  = process("movies", upstream=meta_m, list=[{"name": "trakt_list", ...}])

# Merge and download
output("transmission", upstream=merge(series, movies), host="localhost")
pipeline("mixed", schedule="1h")
```

## Starlark syntax

```python
routes = route(upstream_node,
    leg_name_1 = "boolean expression 1",
    leg_name_2 = "boolean expression 2",
    ...
)
downstream = process("plugin", upstream=routes.leg_name_1, ...)
```

- **Leg order matters**: conditions are evaluated in declaration order; the first match wins.
- **No match**: the entry is rejected with reason `"route: no leg matched"` and a `WARN` log line. There is no silent loss.
- **Leg names**: any valid Starlark identifier. Accessible as attributes on the returned handle object (`routes.series`, `routes.movies`, etc.).

## Expression syntax

Same language as the `condition` filter:

| Form | Example |
|------|---------|
| Field comparison | `series_episode_id != ''` |
| Numeric | `tmdb_vote_average >= 7.0` |
| Logical | `not (source == "CAM" or source == "TS")` |
| String match | `video_genres contains "Documentary"` |

See [`condition`](../condition/README.md) for the full expression reference.

## Config keys (internal — set by the route() builtin)

| Key | Type | Description |
|-----|------|-------------|
| `rules` | list | Ordered `[{name, accept}]` pairs generated from the `route()` kwargs |

> Users never instantiate `route` directly; always use the `route()` Starlark builtin.

## Fields produced

| Field | Type | Description |
|-------|------|-------------|
| `_route_leg` | string | Name of the matched leg (e.g. `"series"`, `"movies"`) |

This field is consumed by the internal `route_selector` nodes and is also available downstream for use in templates, `condition` filters, `pathfmt` patterns, etc.

## Unmatched entry log

```
WARN  route: no leg matched  entry="Some Podcast S01E01 720p"  task=my-task  node=route_0  plugin=route
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `_route_leg` |
| Requires | — |

## Validator behaviour

When all upstreams of a `merge()` node trace back to legs of the same `route()` call (directly or through a single-upstream chain), the DAG validator uses **union** semantics for `certain` fields instead of intersection. This means field requirements satisfied on any one leg are considered satisfied at the merge point — no spurious merge-gap `~` warnings.
