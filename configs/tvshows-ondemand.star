# tvshows-ondemand.star
#
# Ask for a whole show, or one season of it, by name, and have the missing
# episodes downloaded: as season packs where most of a season is missing, as
# single episodes where only a few are. A pack shipped as plain video files
# goes straight into the series folder. One packed in RAR/ZIP/7z archives
# goes to a staging area, and a second pipeline extracts it into the series
# folder once Deluge has finished the download.
#
#   curl -X POST -H "Authorization: Bearer $PIPELINER_INGEST_TOKEN" \
#        "http://localhost:8080/api/ingest/tvshows?pipeline=tvshows-ondemand" \
#        -d '{"title": "Agatha All Along"}'        # every aired season
#        -d '{"title": "Breaking Bad S02"}'        # one season
#        -d '{"title": "The Wire S01-S03"}'        # a run of seasons
#
# Requirements: PIPELINER_INGEST_TOKEN, TVDB_API_KEY, JACKETT_URL,
# JACKETT_API_KEY, DELUGE_PASSWORD; Plex in account mode (sign in on the Tools
# tab), or add url/token to series_gaps and library. Pipeliner must see the
# staging and series folders at the same paths Deluge writes them.

TVDB_API_KEY = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
JACKETT_URL = env("JACKETT_URL", default="http://localhost:9117")
JACKETT_API_KEY = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")
DELUGE_HOST = env("DELUGE_HOST", default="localhost")
DELUGE_PASSWORD = env("DELUGE_PASSWORD", default="deluge")

SERIES = "/data/media/series"
STAGING = "/data/downloads/tv-staging"

SMTP = {
    "smtp_host": env("SMTP_HOST", default="smtp.example.com"),
    "smtp_port": 587,
    "username": env("SMTP_USER", default="you@example.com"),
    "password": env("SMTP_PASSWORD", default="YOUR_SMTP_PASSWORD"),
    "sender": env("SMTP_USER", default="you@example.com"),
    "to": env("NOTIFY_TO", default="you@example.com"),
}

GRABBED = """{{range .Entries}}{{.Title}}{{with index .Fields "series_pack_id"}} (pack {{.}}){{end}}{{if index .Fields "torrent_archive"}} -- archived, unpacked when it finishes{{end}}
{{end}}"""

UNPACKED = """{{range .Entries}}{{.Title}} -> {{index .Fields "torrent_download_dir"}}
{{end}}"""

# --- tvshows-ondemand -------------------------------------------------------

requests = input("webhook", queue="tvshows")

# What is missing, according to Plex, of what TheTVDB says has aired. A title
# that names seasons ("Breaking Bad S02") limits the scan to them. A season
# more than half missing becomes one pack query ("Show S02"), otherwise one
# query per missing episode. retry_cooldown keeps a request that was just
# grabbed from being searched again before Plex has indexed it.
gaps = process("series_gaps", upstream=requests, api_key=TVDB_API_KEY,
               backend="plex", sections=["TV Shows"], seasons="all",
               pack_threshold=0.5, max_per_run=0, retry_cooldown="48h")

# match_titles keeps only results that are the show asked for and the same
# season (for a pack query) or episode: full-text search returns plenty else.
found = process("discover", upstream=gaps, interval="5m", match_titles=True,
                search=[{"name": "jackett", "url": JACKETT_URL, "api_key": JACKETT_API_KEY,
                         "categories": ["5000"], "limit": 50, "timeout": "20s"}])

alive = process("torrent_alive", upstream=found, min_seeds=1, verify=True)
named = process("metainfo_file", upstream=alive)

# series_season, not series_episode_id: a pack has a season and no episode.
parsed = process("require", upstream=named, fields=["title", "series_season", "_quality"])
graded = process("quality", upstream=parsed, spec="720p-1080p hdtv+?")

# An episode already in Plex is refused; a pack never is (series_gaps decided
# its season is wanted from the full episode list).
held = process("library", upstream=graded, backend="plex", sections=["TV Shows"],
               ttl="15m", upgrade=False)

files = process("metainfo_torrent", upstream=held, fetch_timeout="1m")
listed = process("require", upstream=files, fields=["torrent_files"])
clean = process("content", upstream=listed, reject=["*.exe", "*.iso", "*.scr", "*.lnk"])

# Canonical show name for the folder, poster for the email.
info = process("metainfo_tvdb", upstream=clean, api_key=TVDB_API_KEY, cache_ttl="96h")

# Releases already grabbed, or blocklisted after a failed grab. Above dedup
# and limit, so neither spends its choice on one that would be refused after.
fresh = process("seen", upstream=info, local=True, retry_failed=True)

# One release per pack or episode, after everything that can refuse one.
best = process("dedup", upstream=fresh)

# At most three torrents per run, the best-seeded first. A whole show with
# more seasons than that takes more than one run; what is left over is found
# again on the next.
capped = process("limit", upstream=best, n=3, sort="torrent_seeds", order="desc")

# Where it goes. Plain video goes straight to its season folder, and a
# multi-season or complete pack to the show's folder, since it brings its own
# season folders. An archived release goes to the same path under staging,
# which tvshows-unpack maps back. One template rather than a route: a route
# placed after dedup is flagged, because a lane that refused a release would
# lose it, and this way nothing can.
where = process("pathfmt", upstream=capped, field="move_to_path",
                path="{{if .torrent_archive}}" + STAGING + "{{else}}" + SERIES + "{{end}}" +
                     "/{{.title}}{{if and .series_season (not .series_season_end)}}/Season {{printf \"%02d\" .series_season}}{{end}}")
sent = output("deluge", upstream=where, host=DELUGE_HOST, password=DELUGE_PASSWORD,
              move_completed_path="{move_to_path}")
output("notify", upstream=sent, via="email", config=SMTP,
       title="Pipeliner on-demand: {{len .Entries}} show download(s)", body=GRABBED)

pipeline("tvshows-ondemand")  # no schedule: a push triggers it

# --- tvshows-unpack ---------------------------------------------------------

# Every torrent in Deluge; keep the finished ones that were moved to staging.
# Deluge moves a torrent to its move_completed_path only once it is complete,
# so being under staging already means finished.
session = input("torrent_session", backend="deluge", host=DELUGE_HOST, password=DELUGE_PASSWORD)
done = process("condition", upstream=session, rules=[
    {"accept": "torrent_progress >= 100 and torrent_download_dir contains \"" + STAGING + "/\""},
    {"reject": "true"},
])

# Extract into the series folder that mirrors the staging one, never
# overwriting a file. A failure leaves the torrent alone and is retried next
# run; a success removes the torrent and its archives, which are no use once
# extracted.
unpacked = output("decompress", upstream=done, to=SERIES, mirror_from=STAGING)
removed = output("torrent_control", upstream=unpacked, action="remove_with_data",
                 backend="deluge", host=DELUGE_HOST, password=DELUGE_PASSWORD)
output("notify", upstream=removed, via="email", config=SMTP,
       title="Pipeliner unpacked {{len .Entries}} download(s)", body=UNPACKED)

pipeline("tvshows-unpack", schedule="10m")
