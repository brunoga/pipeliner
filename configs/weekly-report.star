# weekly-report.star
#
# The "what happened and why" digest: run_report emits one entry per traced
# run (from the run inspector's store); seen (URL-keyed) makes sure each run
# is reported once; notify sends the weekly summary. Body templates can use
# report_* fields — counts and the top rejection reasons per run.

pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")
trakt_id       = env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID")
trakt_secret   = env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET")

# ── Weekly activity report ───────────────────────────────────────────────────
runs = input("run_report", window="168h")
once = process("seen", upstream=runs)
rep  = output("notify", upstream=once, via="pushover",
              config={"user": pushover_user, "token": pushover_token},
              title="pipeliner week: {{len .Entries}} run(s)",
              body="{{range .Entries}}• {{.Title}}{{with index .Fields \"report_top_rejects\"}}\n   top rejects: {{.}}{{end}}\n{{end}}")
pipeline("weekly-report", schedule="0 9 * * 1")

# ── Tonight's episodes, straight from Trakt ──────────────────────────────────
cal  = input("trakt_calendar", client_id=trakt_id, client_secret=trakt_secret,
             window="24h")
tell = output("notify", upstream=cal, via="pushover",
              config={"user": pushover_user, "token": pushover_token},
              title="Tonight: {{len .Entries}} episode(s)",
              body="{{range .Entries}}• {{.Title}}{{with index .Fields \"description\"}} — {{.}}{{end}}\n{{end}}")
pipeline("trakt-tonight", schedule="0 16 * * *")
