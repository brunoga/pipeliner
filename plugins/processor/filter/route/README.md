# route

Routes entries to named ports based on ordered boolean conditions. The first matching port accepts the entry and stamps `_route_port` on it. Entries that match no port are rejected with a warning log so nothing is silently dropped.

`route()` is a **Starlark builtin**, not a plugin — use it directly in the config, not via `process()`.

## Basic usage

```python
routes = route(upstream,
    series = "series_episode_id != ''",
    movies = "series_episode_id == ''")

series_path = process("metainfo_series", upstream=routes.series)
movies_path = process("metainfo_tmdb",   upstream=routes.movies, api_key=env("TMDB_KEY"))
output("transmission", upstream=merge(series_path, movies_path), host="localhost")
```

## Automatic field inference

The DAG validator automatically infers field contracts from each port's accept expression — no explicit annotations needed:

```python
routes = route(upstream,
    torrent = "torrent_url != ''",   # presence check → torrent_url certain on this branch
    magnet  = "magnet_url != ''")    # presence check → magnet_url certain on this branch
```

### Presence checks (`field != ""`)

When a port condition uses a presence-check operator, the referenced field is promoted to **certain** (guaranteed present) on that branch. A downstream `Requires` for that field passes validation without a warning.

```python
# "torrent_url != ''" on the torrent port means every entry reaching that
# port has torrent_url set — the validator treats it as certain.
```

### Absence checks (`field == ""`)

When a port condition uses an absence-check operator, the referenced field is **removed** from the reachable field set on that branch. Any downstream `Requires` for the absent field is a **hard validation error** at config-load time.

```python
routes = route(upstream,
    torrent = "torrent_url != '' and magnet_url == ''",
    magnet  = "magnet_url != '' and torrent_url == ''")

# On the torrent branch: torrent_url is certain, magnet_url is removed.
# On the magnet branch:  magnet_url is certain, torrent_url is removed.
# A plugin accidentally Requires magnet_url on the torrent branch → load-time error.
```

## Starlark syntax

```python
# All ports use plain string conditions:
routes = route(upstream, port_a="condition_a", port_b="condition_b")
```

- **Port order matters** — conditions are evaluated in declaration order; the first match wins.
- **No match** — entry is rejected with reason `"route: no port matched"` and a `WARN` log line. Nothing is silently dropped.
- **Port handles** — attribute access on the returned object (`routes.series`, `routes.movies`). Each handle is a valid `upstream=` argument.
- **Condition syntax** — same expression language as the `condition` filter. See [`condition`](../condition/README.md) for the full reference.

## Why `route()` instead of separate condition filters?

`condition` filters achieve the same routing effect, but they apply independently — an entry rejected by port A is still evaluated by port B, and a merge downstream produces soft `~` warnings because the validator sees two branches with different field sets.

`route()` declares the branches are **mutually exclusive**: exactly one port matches each entry. This has two benefits:

1. **Cleaner validation** — at merge points the validator uses correct field semantics (no spurious warnings).
2. **Explicit intent** — unmatched entries are explicitly rejected with a clear log message.

## Validator behaviour

### Merge points

When all upstreams of a `merge()` node trace back to ports of the same `route()` call (directly or through a single-upstream chain), the DAG validator uses **intersection** of the `certain` field sets across branches. Without inferred field differences both branches inherit identical field sets from the shared upstream, so intersection and union are the same — no behaviour change.

### Within a branch

- Presence-check fields (`field != ""`) are added to both `reachable` and `certain` — downstream `Requires` passes without warning.
- Absence-check fields (`field == ""`) are removed from both `reachable` and `certain` — downstream `Requires` is a hard error.

These inferences apply automatically from the port condition. Both `AND` and `OR` combinators are supported:
- `AND`: all clauses' fields are inferred (union).
- `OR`: only fields that appear in every clause (intersection).

## Visual editor

In the **Config → Visual** editor, each route rule displays a port name and condition expression. Field inference is shown automatically in the field availability panel — no manual input required.

## Config keys

| Key | Type | Set by | Description |
|-----|------|--------|-------------|
| `rules` | list | `route()` builtin | Ordered `[{name, accept}]` records |

### Internal keys (on `route_selector` nodes)

| Key | Type | Description |
|-----|------|-------------|
| `_route_port_name` | string | Port name this selector passes |
| `_route_group` | string | Group ID linking all selectors of the same `route()` call |
| `_port_accept_expr` | string | Port's accept expression (read by DAG validator for field inference) |

> Users never set these directly; they are managed by the `route()` builtin.

## Fields produced

| Field | Type | Description |
|-------|------|-------------|
| `_route_port` | string | Name of the matched port (e.g. `"series"`, `"torrent"`) |

This field is consumed by the internal `route_selector` nodes and is also available downstream for use in templates, `condition` filters, `pathfmt` patterns, and `exec` commands.

## Unmatched entry log

```
WARN  route: no port matched  entry="Some Title 720p"  task=my-task  node=route_0  plugin=route
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `_route_port` |
| Requires | — |
