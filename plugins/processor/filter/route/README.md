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

## Port field contracts: `port()`

The `port()` helper adds optional field contracts to each route port. This makes the DAG validator significantly more precise for branching pipelines:

```python
routes = route(upstream,
    torrent = port("torrent_url != ''",
                   guarantees=["torrent_url"],
                   masks=["magnet_url"]),
    magnet  = port("magnet_url != ''",
                   guarantees=["magnet_url"],
                   masks=["torrent_url"]),
)
```

### `guarantees`

Fields listed in `guarantees` are promoted from **MayProduce** (reachable but uncertain) to **Produces** (certain) on this branch. A downstream `Requires` for a guaranteed field passes validation without a warning.

```python
# Without guarantees: torrent_url is only MayProduce from upstream,
# so a downstream Requires produces a ~ warning.
# With guarantees: the validator knows torrent_url is always present
# on this branch — Requires passes cleanly.
```

### `masks`

Fields listed in `masks` are **explicitly removed** from the branch's available field set. Any downstream `Requires` for a masked field is a **hard validation error** at config-load time, not a soft warning.

At runtime, `route_selector` strips masked fields from passing entries so the actual entry data matches the validator's model.

```python
# On the torrent branch, masking magnet_url means:
# - A plugin that accidentally Requires magnet_url → load-time error
# - Entries passing through this port have magnet_url stripped
# This catches wiring mistakes before any entry is processed.
```

Both `guarantees` and `masks` are optional. Plain string port values remain fully backward compatible:
```python
routes = route(upstream, series="cond1", movies="cond2")  # still works
```

## Full example with field contracts

See [`configs/route-port-contracts.star`](../../../../configs/route-port-contracts.star) for a complete working example using `port()`.

## Starlark syntax

```python
# Plain string (backward compatible):
routes = route(upstream, port_a="condition_a", port_b="condition_b")

# With field contracts:
routes = route(upstream,
    port_a = port("condition_a", guarantees=["field1"], masks=["field2"]),
    port_b = port("condition_b", guarantees=["field2"], masks=["field1"]),
)
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

### Merge points (without `port()`)

When all upstreams of a `merge()` node trace back to ports of the same `route()` call (directly or through a single-upstream chain), the DAG validator uses **intersection** of the `certain` field sets across branches. Without `port()` contracts both branches inherit identical field sets from the shared upstream, so intersection and union are the same — no behaviour change from previous versions.

### Merge points (with `port()`)

When ports have different `guarantees`/`masks`, the branches carry different `certain` field sets. After the merge, only fields that are certain on **all** branches remain certain. Branch-specific fields (guaranteed on one branch, masked on another) become `MayProduce`-equivalent at the merge point — producing a warning if a downstream node `Requires` them, which is the correct signal.

### Within a branch

- `guarantees` fields are added to both `reachable` and `certain` for this branch — downstream `Requires` passes without warning.
- `masks` fields are removed from both `reachable` and `certain` — downstream `Requires` is a hard error.

## Visual editor

In the **Config → Visual** editor, each route rule displays two compact sub-row inputs beneath the port name and condition:

- **guarantees** (green label) — space-separated field names
- **masks** (amber label) — space-separated field names

Both are editable inline and are preserved through save/load cycles.

## Config keys

| Key | Type | Set by | Description |
|-----|------|--------|-------------|
| `rules` | list | `route()` builtin | Ordered `[{name, accept, guarantees?, masks?}]` records |

### Internal keys (on `route_selector` nodes)

| Key | Type | Description |
|-----|------|-------------|
| `_route_port_name` | string | Port name this selector passes |
| `_route_group` | string | Group ID linking all selectors of the same `route()` call |
| `_port_guarantees` | list | Fields guaranteed present (read by DAG validator) |
| `_port_masks` | list | Fields guaranteed absent (read by DAG validator + stripped at runtime) |

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
