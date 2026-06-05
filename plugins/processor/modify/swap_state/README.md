# swap_state

Swaps entries between two states (e.g. `accepted` ↔ `rejected`). It is one of the very few plugins that intentionally operates on rejected and failed entries — its purpose is to flip them so downstream nodes can act on them.

Most processors skip rejected and failed entries via `if e.IsRejected() || e.IsFailed() { continue }`. `swap_state` deliberately does not — that's the point.

## Config

| Key | Required | Description |
|---|---|---|
| `swap` | yes | Two distinct state names from `{accepted, rejected, failed, undecided}`. Entries in either state are flipped to the other; entries in any third state are left untouched. |

## What is preserved

- **`AcceptReason` / `RejectReason` / `FailReason`** survive the swap as audit history. A rescued failed entry still carries its original `FailReason` for templates and logs.
- **`Consumed` flag** is orthogonal to state and is left alone.
- **`Fields`** are not touched.

## What is recorded

Every swap populates `Entry.LastStateChange`:

```go
type StateChange struct {
    From   State    // state before the swap
    To     State    // new state
    Plugin string   // "swap_state"
    Reason string   // e.g. "swap [accepted, failed]"
    At     time.Time
}
```

Notify templates can render both the original reason and the swap:

```
{{.FailReason}}                        →  deluge: connection refused
{{.LastStateChange.Reason}}            →  swap [accepted, failed]
```

## DAG role

| Property | Value |
|---|---|
| Role | `processor` |
| Produces | — |
| Requires | — |

## Examples

### Cleanup pipeline — delete dedup's losers, leave winners alone

`dedup` keeps the best-quality copy and rejects the rest. To actually delete the rejected duplicates from disk while keeping the winners safe:

```python
src    = input("filesystem", path="/media/movies", recursive=True)
meta   = process("metainfo_file", upstream=src)
best   = process("dedup",         upstream=meta)
flip   = process("swap_state",    upstream=best, swap=["accepted", "rejected"])
output("exec", upstream=flip, command="rm -f {file_location}")
pipeline("dedup-cleanup")
```

After the swap, the dedup *losers* (originally rejected) are Accepted and reach `exec`. The dedup *winners* (originally accepted) are Rejected and are filtered out by `exec`'s `FilterAccepted`, so they are never touched. The bidirectional flip is what makes a single sink correctly delete only the duplicates.

### Retry pipeline — route failed entries through a fallback sink

```python
src      = input("rss", url="https://feeds.example.com/movies")
meta     = process("metainfo_file", upstream=src)
primary  = output("deluge",        upstream=meta, host="primary.lan", password=env("DELUGE_PASS"))
rescued  = process("swap_state",   upstream=primary, swap=["accepted", "failed"])
output("deluge", upstream=rescued, host="fallback.lan", password=env("DELUGE_PASS"))
pipeline("downloads-with-failover")
```

Entries that the primary deluge daemon failed to enqueue (network error, daemon down, malformed URL) are flipped to Accepted and forwarded to the fallback. Entries the primary accepted are flipped to Failed, so the fallback does not duplicate-enqueue them.

### Re-feed rejected entries through a different filter

```python
strict   = process("condition",  upstream=meta, reject='video_rating < 8.0')
relaxed  = process("swap_state", upstream=strict, swap=["rejected", "undecided"])
fallback = process("condition",  upstream=relaxed, reject='video_rating < 6.5')
```

Entries that the strict gate rejected are returned to Undecided so the relaxed gate gets a second chance at them.

## Notes

- The two states in `swap=` must be distinct. `swap=["accepted", "accepted"]` is a config error.
- An entry in a state not named in `swap=` is left completely untouched — no state change, no `LastStateChange` record.
- The DAG validator's `Reachable` / `Certain` field analysis does not yet model what `swap_state` does to the entry population downstream. After a swap, the new "accepted" entries may have skipped enrichment processors that operated only on the prior-accepted population. Treat downstream field availability as best-effort until that follow-up lands.
