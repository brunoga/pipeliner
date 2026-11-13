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

## Limitation: it only sees what upstream returned

Filters drop the entries they reject from their output, so `swap_state` never sees an entry rejected by the node before it. Only entries an upstream node returned can be flipped. In particular, `dedup` → `swap_state` does **not** hand `dedup`'s losers to the next node. Verified: of two copies of one film, `dedup` rejected the 720p one and dropped it, `swap_state` flipped the 1080p winner to rejected, and nothing reached the sink. To act on dedup's inferior copies, use [`dedup`](../../filter/dedup/) with `invert=True`, as `configs/dedup-cleanup.star` does; to keep the best n and act on the rest, [`limit`](../../filter/limit/) with `invert=True`.

## Examples

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
- The DAG validator tracks field certainty per entry state. `swap_state` is modelled as exchanging the two named state buckets, so a downstream node whose `Requires` were only satisfied by upstream narrowing (a `require` filter, or a `condition` that rejected entries missing the field) will get a *may not be present* warning when those rejected entries are revived. This is intentional — the revived entries don't have the narrowed field, so the downstream guarantee no longer holds. Two patterns avoid the warning: put the swap immediately before a sink that doesn't care about specific fields, or add a downstream `require` / `condition` to re-establish the contract on the revived population.
