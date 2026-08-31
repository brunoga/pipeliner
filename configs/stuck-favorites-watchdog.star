# stuck-favorites-watchdog.star
#
# Alert when a favorite silently stops downloading.
#
# stuck_favorites reads the resolved series/movies favorite lists and the run
# inspector's traces, and emits one entry per favorite whose candidates kept
# showing up in runs but were never accepted (min_runs distinct runs). A
# URL-keyed seen filter makes each favorite alert only once until it recovers,
# and notify sends the batch. This inverts the failure mode where an episode
# fails to grab for weeks with nothing to flag it.
#
# Requires that a series/movies pipeline using a list has run at least once
# (so the favorite caches and run traces exist).

pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")

stuck  = input("stuck_favorites", min_runs=3)
once   = process("seen", upstream=stuck)  # URL-keyed by default: alert once per favorite
notify = output("notify", upstream=once, via="pushover",
                config={"user": pushover_user, "token": pushover_token},
                title="Pipeliner: {{len .Entries}} stuck favorite(s)",
                body="{{range .Entries}}• {{.Title}}{{with index .Fields \"stuck_last_reason\"}}\n  {{.}}{{end}}\n{{end}}")
pipeline("stuck-favorites-watchdog", schedule="24h")
