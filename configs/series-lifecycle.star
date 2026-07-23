# series-lifecycle.star
#
# Follow lifecycle for tracked series: stop searching for shows that are over.
#
# The series_tracker source emits one entry per show the series tracker knows
# about (the same store the series/premiere filters write download records
# to — shared across all pipelines). series_lifecycle enriches each show with
# TheTVDB status + episode list and classifies it:
#
#   complete - status Ended/Cancelled AND every aired episode is tracked
#   dormant  - Ended/Cancelled but aired episodes are missing (backfill hint)
#   active   - still running, upcoming, or lookup failed (keep following)
#
# Complete shows are deactivated in the tracker (the series filter then
# rejects their episodes early with "series: <show> inactive (complete)"),
# and a notify confirms it. Dormant shows only notify — they are backfill
# candidates, not done. To undo a deactivation, run a pipeline with
# series_tracker_update(action="reactivate").
#
# Requirements: TVDB_API_KEY, PUSHOVER_USER, PUSHOVER_TOKEN.

tvdb_api_key   = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")

shows = input("series_tracker")
lc    = process("series_lifecycle", upstream=shows, api_key=tvdb_api_key)

routes = route(lc,
    complete = 'series_lifecycle == "complete"',
    dormant  = 'series_lifecycle == "dormant"',
    active   = 'series_lifecycle == "active"')

# ── Complete: deactivate, then notify only after the tracker write succeeded ─
deact = output("series_tracker_update", upstream=routes.complete,
               action="deactivate")
output("notify", upstream=deact, via="pushover",
       config={"user": pushover_user, "token": pushover_token},
       title="Series complete",
       body="{{range .Entries}}Series complete: {{.Title}}\n{{end}}")

# ── Dormant: ended with gaps — surface as backfill candidates ────────────────
output("notify", upstream=routes.dormant, via="pushover",
       config={"user": pushover_user, "token": pushover_token},
       title="Backfill candidates",
       body="{{range .Entries}}Backfill candidate: {{.Title}} (missing {{index .Fields \"series_missing_episode_count\"}} of {{index .Fields \"series_aired_episode_count\"}} aired episodes)\n{{end}}")

# ── Active: nothing to do — log for visibility ───────────────────────────────
output("print", upstream=routes.active)

pipeline("series-lifecycle", schedule="168h")
