# Requires / Produces Overhaul Plan

## Problem Statement

The current `Requires []string` / `Produces []string` system on `plugin.Descriptor` has
three weaknesses:

1. **OR semantics are inexpressible.** A plugin that can fall back from field A to field
   B (needing at least one) cannot declare that. It must either lie (`Requires` neither,
   making the constraint invisible) or over-constrain (`Requires` both, which is wrong).

2. **Merge gaps go undetected.** The validator uses union semantics across upstreams: if
   *any* upstream produces a field the check passes, even when entries from other upstream
   branches will never carry that field. Misconfigured merge nodes silently produce wrong
   results at runtime.

3. **Produces is all-or-nothing.** There is no distinction between a plugin that
   *always* sets a field on every passing entry vs. one that sets it only when the entry
   matches (e.g. `metainfo_quality` silently skips unparseable titles). Downstream
   validation is therefore optimistic.

---

## Design

### 1. Unified `Requires [][]string` on `Descriptor`

Replace `Requires []string` with `Requires [][]string`.

Each inner slice is a **requirement group**. The constraint is:

> For every group, at least one field in the group must be produced by the transitive
> upstreams before this node.

| Group contents | Meaning |
|---|---|
| `{"A"}` | A must be present — equivalent to old `Requires: []string{"A"}` |
| `{"A", "B"}` | At least one of A or B must be present (OR / fallback) |
| Multiple groups | Every group must independently be satisfied (groups are ANDed) |

**Helper constructors** live in `internal/plugin/requires.go` to keep descriptor
declarations readable:

```go
// RequireAll returns one single-element group per field (AND semantics).
// Equivalent to the old Requires []string behaviour.
func RequireAll(fields ...string) [][]string

// RequireAny returns a single group containing all fields (OR semantics).
// At least one must be produced by an upstream.
func RequireAny(fields ...string) [][]string
```

Usage in plugin descriptors:

```go
// Old single-field require:
Requires: RequireAll(entry.FieldVideoQuality),

// Old multi-field require (all must be present):
Requires: RequireAll(entry.FieldFileLocation, entry.FieldTorrentFiles),

// New OR group (needs tmdb_id OR movie_title):
Requires: RequireAny("trakt_tmdb_id", "movie_title"),

// Mixed: needs video_quality AND (tmdb_id OR imdb_id):
Requires: append(RequireAll(entry.FieldVideoQuality), RequireAny("tmdb_id", "imdb_id")...),
```

### 2. Split `Produces` into `Produces` and `MayProduce`

```go
// Produces lists fields this plugin sets on EVERY passing entry.
Produces []string

// MayProduce lists fields this plugin sets only on some entries
// (e.g. when parsing succeeds, when a match is found).
MayProduce []string
```

The validator treats both as satisfying a `Requires` group at the structural level (a
field is "reachable" if any upstream Produces or MayProduces it). The distinction is used
for the merge-gap warning (see §3) and for documentation/UI purposes.

Existing `Produces` declarations that are truly conditional should be migrated to
`MayProduce`. The migration can be done incrementally — leaving a field in `Produces`
when it should be `MayProduce` is no worse than the current behaviour.

### 3. Merge-gap warnings in the DAG validator

At a merge node (node with multiple upstreams), compute two sets per field:

- **covered** — fields reachable from *all* upstreams (intersection)
- **partial** — fields reachable from *some* upstreams (union − intersection)

When checking a downstream node's `Requires` groups, if the only satisfying field(s) in a
group are in `partial` (not `covered`), emit a **warning** rather than an error:

```
WARNING: node "upgrade" (plugin "upgrade"): required field "video_quality" is not
produced by all upstream paths into the merge at node "merged_src" — entries from
some paths will lack this field at runtime
```

Warnings are collected separately from errors. `config.Validate` surfaces them without
failing the load (log at `WARN` level); a strict mode flag can promote them to errors.

### 4. Debug-mode per-entry field checks in the executor

Add an optional validation pass in `internal/executor/executor.go` controlled by a
package-level flag (or passed in via `ExecutorOptions`):

```go
type ExecutorOptions struct {
    ValidateFields bool // check Requires groups on every entry before processing
}
```

When `ValidateFields` is true, before calling `Process` / `Consume` for a node the
executor checks each entry against the node's `Requires` groups. Any entry missing all
fields in a group logs a warning:

```
WARN executor: entry <url> entering node "upgrade" is missing all fields for
require group [video_quality] — plugin may silently no-op
```

Entries are still passed through; this is a diagnostic, not a gate. Activate via a
`--validate-fields` CLI flag or an environment variable (`PIPELINER_VALIDATE_FIELDS=1`).

---

## Affected Files

| File | Change |
|---|---|
| `internal/plugin/registry.go` | Replace `Requires []string` with `Requires [][]string`; add `MayProduce []string` |
| `internal/plugin/requires.go` | **New file**: `RequireAll` and `RequireAny` helpers |
| `internal/dag/validate.go` | Update field reachability check for `[][]string` groups; add merge-gap warning logic; treat `MayProduce` as reachable but mark as partial |
| `internal/executor/executor.go` | Add optional per-entry field validation pass; accept `ExecutorOptions` |
| `cmd/pipeliner/main.go` | Wire `--validate-fields` flag into `ExecutorOptions` |
| All plugins declaring `Requires:` | Migrate to `RequireAll(...)` / `RequireAny(...)` |
| All plugins declaring `Produces:` | Audit and split into `Produces` / `MayProduce` as appropriate |
| `internal/dag/dag_test.go` | Update tests for new `Requires` shape; add merge-gap warning tests |

---

## Migration Steps

1. **Add `requires.go` helpers** and update `Descriptor` struct. Keep backward
   compatibility during migration by adding a `RequiresFlat() []string` helper if
   anything else consumes `Requires` directly (UI, docs generation).

2. **Update `dag/validate.go`** for the new `[][]string` shape and add merge-gap
   warning logic.

3. **Migrate all plugin `Requires` declarations** — this is mechanical; every existing
   `Requires: []string{"x", "y"}` becomes `Requires: RequireAll("x", "y")`.

4. **Audit `Produces` declarations** across all plugins and split into `Produces` /
   `MayProduce` where appropriate. Err on the side of `MayProduce` when unsure.

5. **Add executor `ValidateFields` pass** and wire the CLI flag.

6. **Update tests** — existing field-requirement tests need the new shape; new tests
   for OR groups, merge-gap warnings, and runtime field checks.

---

## Non-goals

- Runtime rejection of entries with missing fields (the validator is advisory, not a
  gate; plugins remain defensively written).
- Breaking the linear task engine — it does not use `Requires`/`Produces` for execution,
  only the DAG validator does.
