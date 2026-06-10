# report_empty

Emits a synthetic marker entry when its upstream produced **no** entries; emits nothing otherwise.

Use it to opt into "alert when the upstream returned nothing" patterns — e.g. notify when a tracker timed out, when a search returned zero results, or when every entry got filtered out.

Per-entry plugins (`condition`, `route`, every sink) only run their body when they have at least one entry to act on, so there's no way for a downstream sink to react to an empty batch on its own. `report_empty` bridges that gap by turning *the absence of entries* into a single entry the rest of the pipeline can handle normally.

## How it behaves

| Upstream | Output |
|----------|--------|
| 0 entries | 1 marker entry (Accepted, `Title=message`, `empty_marker=true`) |
| 1+ entries | 0 entries (everything dropped) |

It is a **marker-only emitter** — when there are real entries flowing, it produces nothing. Chain it on a fan-out branch from the upstream you want to monitor; the main path of your pipeline should read from that upstream directly, not from `report_empty`'s output.

## Canonical pattern

```python
src   = input("jackett", api_url=env("JACKETT_URL"), api_key=env("JACKETT_KEY"),
              movie="My Movie")

# Main path — reads from src directly.
seen  = process("seen",         upstream=src)
output("transmission",          upstream=seen, host="localhost")

# Alert path — separate branch off src. Marker fires only when src
# returned 0 entries.
alert = process("report_empty", upstream=src,
                message="Jackett returned no results for My Movie")
output("notify", via="email",   upstream=alert,
       to="me@example.com", subject="Pipeline alert", body="{{.Title}}")

pipeline("my-movie", schedule="1h")
```

Fan-out semantics give you the opt-in for free: if you don't add the `report_empty` node, no marker is ever generated and the notify sink never fires.

## Marker entry shape

| Field | Value |
|-------|-------|
| `Title` | The configured `message` (default `"(no entries)"`) |
| `URL` | Synthetic: `pipeliner://empty/<task_name>` |
| `State` | `Accepted` — opted-in sinks pick it up without a filter in between |
| `empty_marker` | `true` (boolean) — use in downstream expressions to distinguish marker from real entries when you mix flows |
| `marker` flag | Set on the entry. The executor strips markers from any plugin that didn't declare `Descriptor.AcceptsMarkers`, so misrouted pipelines (`report_empty` → `metainfo_tmdb` → `notify`) don't accidentally enrich, download, or otherwise act on the placeholder |

## Which plugins receive the marker?

The marker is delivered only to plugins that explicitly opted in via `Descriptor.AcceptsMarkers`. As of this commit:

| Plugin | Receives markers? |
|--------|-------------------|
| `notify`, `print` | Yes — they're the intended destinations |
| `report_empty` itself | Yes — chaining two of them sees the upstream marker and emits nothing (no double-fire) |
| Everything else (`metainfo_*`, `transmission`, `qbittorrent`, `deluge`, `exec`, `download`, `seen`, every filter except `condition`/`route`) | No — markers bypass them and merge back into the downstream slice unchanged |

The bypass is transparent: a marker that hits a non-opt-in plugin keeps flowing — it just doesn't enter that plugin's `Process` / `Consume`. So if you accidentally place `report_empty → metainfo_tmdb → notify`, the marker silently routes around `metainfo_tmdb` and lands at `notify` as expected.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `message` | no | `"(no entries)"` | Title to set on the marker entry. Customize per-pipeline so notifications carry useful context |

## Composing with other plugins

### Empty after filtering

Place `report_empty` *after* a filter chain to alert when nothing survived filtering — not just when the source itself was empty:

```python
src      = input("rss", url="...")
meta     = process("metainfo_tmdb", upstream=src, api_key=env("TMDB_KEY"))
filtered = process("condition",     upstream=meta, accept="video_rating >= 7.5")

alert = process("report_empty", upstream=filtered,
                message="Nothing met the rating bar this run")
output("notify", via="email", upstream=alert, ...)
```

Because `report_empty`'s default `InputStates` is `StatesAcceptedUndecided`, "empty" means "no entries in those states reached me" — which is exactly what you want after a filter that may Reject entries.

### Branching on the marker downstream

If you ever route the marker through more processing, you can detect it explicitly:

```python
process("condition", upstream=alert, reject="empty_marker == true")
```

In the canonical pattern you don't need this — the marker is the *only* thing flowing through the alert branch.

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| May produce | `empty_marker` |
| Requires | — |
