# stuck_favorites

Emits one entry per **stuck favorite**: a show or movie on a `series`/`movies`
list whose candidates keep appearing in runs but never get accepted. Pipe it
into a [`notify`](../../sink/notify/README.md) sink on a schedule to be alerted
when something silently stops downloading — the failure mode where an episode
fails to grab for weeks with nothing to flag it.

It reads the resolved favorite lists from the series/movies title-list caches
(`cache_series_list`, `cache_movies_list`) and correlates them with the kept run
traces. A candidate is linked to a favorite even across a normalization gap
(within `max_distance` edits), so a matching bug still surfaces here rather than
hiding as an unrelated "not in list" rejection.

Correlation is **scoped by media kind**: a favorite from a series list is only
matched against occurrences from pipelines that filter series, and a movie
favorite only against pipelines that filter movies. So a TV favorite (e.g. one
from `tvdb_favorites`, which only feeds `series` pipelines) never picks up
rejections from an unrelated movies pipeline — which would otherwise show a
nonsensical last reason like `missing required field: video_year`.

A favorite is reported when its candidates were **blocked** (rejected or failed)
in at least `min_runs` distinct runs and **never succeeded** in any of them. The
`stuck_nearest_distance` field tells the story: `0` means the favorite matched but
every candidate was rejected downstream (quality, tracking); a positive value
means candidates only *nearly* match the favorite — the fingerprint of a
matching/normalization problem.

To keep the report signal-dense, three things are deliberately **not** counted as
stuck:

- **Undecided occurrences** — a `discover`/search pipeline emits a result per
  favorite every run and never accepts them (the actual download happens in
  another pipeline), so those entries end *undecided* rather than rejected. They
  are ignored; otherwise an entire discover list would report as stuck.
- **Already-acquired favorites** — a repeat rejected with "already downloaded" or
  "already seen" (by the `dedup`/`seen` filters) proves the pipeline grabbed it
  before, so the favorite is healthy.
- **Loose near-misses** — a candidate only associates to a favorite when it is an
  exact/glob match, a punctuation-only normalization gap, or within a quarter of
  the title length in edits. Unrelated titles that merely fall within a few edits
  (e.g. *fbi* ↔ *vigil*, *fallout* ↔ *furious*) are treated as feed noise.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `min_runs` | no | `3` | Distinct runs a favorite must be seen in (never accepted) before it is reported |
| `max_distance` | no | `6` | Max edit distance to link a candidate title to a favorite |

## Fields set on each entry

| Field | Description |
|-------|-------------|
| `stuck_favorite` | Normalized favorite name |
| `stuck_runs` | Distinct runs its candidates were seen in without acceptance |
| `stuck_nearest_distance` | `0` = favorite matched (downstream reject); `>0` = candidates only nearly match |
| `stuck_last_reason` | Most recent rejection reason |
| `stuck_example_title` | A representative candidate release title |
| `stuck_last_task` | Pipeline the last candidate was seen in |

Entries have stable `pipeliner://stuck/<favorite>` URLs: put a URL-keyed
[`seen`](../../processor/filter/seen/README.md) filter downstream to alert on
each favorite only once until it recovers.

## Example

```python
stuck  = input("stuck_favorites", min_runs=3)
notify = output("notify", upstream=stuck, via="email",
                to="me@example.com",
                subject="Pipeliner: stuck favorites",
                body="{{.Title}}\nlast reason: {{index .Fields \"stuck_last_reason\"}}")
pipeline("stuck-watchdog", schedule="24h")
```

Traces are bounded (last 20 runs per pipeline), so detection covers recent
history rather than all time.
