# series-backfill.star
#
# Backfill via gap detection: the series tracker knows what you have, TheTVDB
# knows what exists — the diff is your backlog.
#
# series_tracker emits one entry per tracked show. series_lifecycle classifies
# each one; a condition gate keeps only "dormant" shows (Ended/Cancelled with
# aired episodes missing — the ideal backfill targets: the episode list will
# never grow again). series_gaps then diffs each show's aired episodes against
# the tracker and emits one search-query entry per missing episode
# ("My Show S02E05"), capped at max_per_run per run with a persisted cursor
# that resumes (and wraps around) on the next run. discover searches Jackett
# for each query; the results flow through the usual episode chain and into
# Transmission, with a Pushover note for every grab.
#
# pack_threshold: when more than that fraction of a season's aired episodes is
# missing, series_gaps emits a single season-pack query ("My Show S02")
# instead of per-episode queries. Season-pack releases don't parse as single
# episodes, so the strict require/series chain below would drop their search
# results — this config pins pack_threshold=1.0 (per-episode only). Lower it
# (e.g. 0.5) and relax the chain if you want pack grabs; see the "Backfill"
# section of the user guide.
#
# Requirements: TVDB_API_KEY, JACKETT_URL, JACKETT_API_KEY, PUSHOVER_USER,
# PUSHOVER_TOKEN.

tvdb_api_key   = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
jackett_url    = env("JACKETT_URL", default="http://localhost:9117")
jackett_key    = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")
pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")
trans_host     = "localhost"
tv_path        = "/media/tv"

shows = input("series_tracker")
lc    = process("series_lifecycle", upstream=shows, api_key=tvdb_api_key)

# Keep only dormant shows (ended, gaps remain). The catch-all reject matters:
# accept alone would leak active/complete shows through as Undecided.
gate  = process("condition", upstream=lc, rules=[
    {"accept": 'series_lifecycle == "dormant"'},
    {"reject": "true"},
])

# One query entry per missing aired episode, 30 per run, resuming next run.
gaps  = process("series_gaps", upstream=gate, api_key=tvdb_api_key,
                pack_threshold=1.0, max_per_run=30)

found = process("discover", upstream=gaps, interval="12h",
                search=[{"name": "jackett",
                         "url": jackett_url,
                         "api_key": jackett_key,
                         "indexers": ["all"]}])

fresh = process("seen",          upstream=found)
meta  = process("metainfo_file", upstream=fresh)
req   = process("require",       upstream=meta,
                fields=["title", "series_episode_id", "series_season",
                        "series_episode", "_quality"])
qual  = process("quality",       upstream=req, spec="720p+")

# tracking="backfill" accepts any episode not yet downloaded, including ones
# older than the newest tracked episode — exactly what a backfill grabs.
flt   = process("series",        upstream=qual,
                tracking="backfill",
                list=[{"name": "series_tracker"}])

fmt   = process("pathfmt", upstream=flt, field="download_path",
                path=tv_path + "/{title}/Season {series_season:02d}")
dl    = output("transmission", upstream=fmt, host=trans_host,
               path="{download_path}")

# Chained after transmission: fires only for confirmed grabs.
output("notify", upstream=dl, via="pushover",
       config={"user": pushover_user, "token": pushover_token},
       title="Backfill grabbed",
       body="{{range .Entries}}Backfilled: {{.Title}}\n{{end}}")

pipeline("series-backfill", schedule="12h")
