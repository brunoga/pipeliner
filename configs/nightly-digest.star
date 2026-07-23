# nightly-digest.star
#
# Notification upgrades, two patterns:
#
# 1. "Tonight's episodes": tvdb_calendar emits one entry per upcoming episode
#    (next 24h) of every tracked show; notify sends one message per run with
#    the per-run batch (the default body template ranges over .Entries).
#
# 2. Daily download digest: the notify sink's digest mode buffers accepted
#    entries across runs in pipeliner.db and sends ONE combined message per
#    24h window. Digest items carry title, URL, and accept reason — keep the
#    body template to those fields.

tvdb_api_key   = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")

# ── Pipeline 1: tonight's episodes ───────────────────────────────────────────
cal  = input("tvdb_calendar", api_key=tvdb_api_key, window="24h")
tell = output("notify", upstream=cal, via="pushover",
              config={"user": pushover_user, "token": pushover_token},
              title="Tonight: {{len .Entries}} episode(s)",
              body="{{range .Entries}}• {{.Title}}{{with index .Fields \"description\"}} — {{.}}{{end}}\n{{end}}")
pipeline("tonights-episodes", schedule="0 16 * * *")

# ── Pipeline 2: daily digest of grabs ────────────────────────────────────────
src  = input("rss", url="https://feeds.example.com/tv.rss")
meta = process("metainfo_file", upstream=src)
req  = process("require", upstream=meta,
               fields=["title", "series_episode_id", "series_season",
                       "series_episode", "_quality"])
flt  = process("series", upstream=req, static=["Breaking Bad"])
out  = output("transmission", upstream=flt, host="localhost")
dig  = output("notify", upstream=out, via="pushover",
              config={"user": pushover_user, "token": pushover_token},
              digest="24h",
              title="pipeliner: {{len .Entries}} grab(s) today",
              body="{{range .Entries}}• {{.Title}}\n{{end}}")
pipeline("tv-with-digest", schedule="1h")
