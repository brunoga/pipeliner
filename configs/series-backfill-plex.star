# Backfill the shows you are part-way through, using Plex as the source of
# truth for what you already have.
#
# This is the library-truth twin of series-backfill.star. That one diffs
# TheTVDB's episode list against pipeliner's own download tracker, which has
# two consequences people trip over: an episode you ripped yourself looks
# missing, and an episode you downloaded and later deleted can never be
# fetched again, because the tracker still says it was grabbed.
#
# Here `backend="plex"` makes the Plex library the authority instead. Deleting
# an episode re-opens the gap, and anything already on the server is left
# alone no matter where it came from.
#
# `seasons="from_first_owned"` is the part that keeps this from becoming a
# whole-library download. It anchors on the earliest season you hold an
# episode of and works forward:
#
#   you have only S02E01   ->  season 1   not wanted (you skipped it)
#                              season 2   rest of it wanted
#                              season 3   wanted in full, even holding none
#   you have nothing       ->  nothing wanted (never-watched shows stay off)
#
# Swap it for seasons="all" to fill a show in completely, specials aside.

TVDB_API_KEY   = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
JACKETT_URL    = env("JACKETT_URL", default="http://localhost:9117")
JACKETT_KEY    = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")
PUSHOVER_USER  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
PUSHOVER_TOKEN = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")
TV_PATH        = "/media/tv"

# The show list, also from Plex. series_tracker would name only the shows
# pipeliner has download records for, which misses every show you ripped or
# acquired some other way; library_shows names the shows you actually have.
#
# With this the whole chain is answered by the library: which shows to
# consider, which episodes are missing, and how far back to go.
shows = input("library_shows", sections=["TV Shows"])

# No url/token: Plex account mode, so sign in once on the Tools tab and every
# owned server is read. sections keeps the scan to the TV library, since a
# movie section has no episodes to contribute.
#
# pack_threshold=1.0 disables season packs: a part-owned season wants
# individual episodes, not a pack that would re-download what you have.
gaps = process("series_gaps", upstream=shows, api_key=TVDB_API_KEY,
               backend="plex", sections=["TV Shows"],
               seasons="from_first_owned",
               library_ttl="15m", retry_cooldown="48h",
               pack_threshold=1.0, max_per_run=30)

found = process("discover", upstream=gaps, interval="12h",
                search=[{"name": "jackett",
                         "url": JACKETT_URL,
                         "api_key": JACKETT_KEY,
                         "indexers": ["all"]}])

# seen drops releases already tried, so a dead torrent is not re-grabbed.
fresh = process("seen", upstream=found)
meta  = process("metainfo_file", upstream=fresh)
req   = process("require", upstream=meta,
                fields=["title", "series_episode_id", "series_season",
                        "series_episode", "_quality"])
qual  = process("quality", upstream=req, spec="720p+")

# tracking="backfill" accepts any episode not yet downloaded, rather than
# enforcing forward order — which is the whole point of a backfill run.
flt = process("series", upstream=qual,
              tracking="backfill",
              list=[{"name": "series_tracker"}])

fmt = process("pathfmt", upstream=flt, field="download_path",
              path=TV_PATH + "/{title}/Season {series_season:02d}")

dl = output("transmission", upstream=fmt, host="localhost",
            path="{download_path}")

# Tell Plex to rescan, so the next run sees what this one grabbed and stops
# proposing it once the pending cooldown lapses.
refresh = output("library_refresh", upstream=dl, backend="plex")

output("notify", upstream=refresh, via="pushover",
       config={"user": PUSHOVER_USER, "token": PUSHOVER_TOKEN},
       title="Backfill grabbed",
       body="{{range .Entries}}Backfilled: {{.Title}}\n{{end}}")

pipeline("series-backfill-plex", schedule="12h")
