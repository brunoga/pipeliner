# ondemand-request.star
#
# Remote automation: push a movie title from anywhere and have pipeliner
# search for it and download the best release immediately — no schedule,
# no watchlist. The request IS the list.
#
# 1. Start the daemon with an ingest token:
#        PIPELINER_INGEST_TOKEN=secret pipeliner daemon --config ondemand-request.star --web :8080 ...
# 2. Push a title (the ?pipeline= parameter triggers the run immediately):
#        curl -X POST -H "Authorization: Bearer secret" \
#             "http://localhost:8080/api/ingest/movies?pipeline=movies-ondemand" \
#             -d '{"title": "Heat 1995"}'
#    or use the ready-made Go client:
#        go run ./examples/request-movie "Heat 1995"
#
# The webhook source drains the queue; discover searches Jackett for each
# pushed title; the rest is the usual gate chain. The movies filter (accept-all
# mode — no list) still records downloads, so repeated requests dedupe and
# quality upgrades follow the normal rules.
#
# Requirements: PIPELINER_INGEST_TOKEN, JACKETT_URL, JACKETT_API_KEY

jackett_url = env("JACKETT_URL", default="http://localhost:9117")
jackett_key = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")
movies_path = "/media/movies"

requests = input("webhook", queue="movies")

# One Jackett search per pushed title. interval is the per-title re-search
# cooldown: pushing the same title twice within it reuses the first search.
# match_titles is essential here: the movies filter below is accept-all, and
# indexer full-text search returns fuzzy junk — a search for "Mystery Men"
# also returns "Wake Up Dead Man: A Knives Out Mystery".
found = process("discover", upstream=requests, interval="5m", match_titles=True,
                search=[{"name": "jackett",
                         "url": jackett_url,
                         "api_key": jackett_key,
                         "categories": ["2000"],
                         "limit": 20,
                         "timeout": "20s"}])

alive   = process("torrent_alive", upstream=found, min_seeds=2)
meta    = process("metainfo_file", upstream=alive)
req     = process("require", upstream=meta, fields=["title", "video_year", "_quality"])
quality = process("quality", upstream=req, spec="1080p+ webrip+")

# Accept-all movies filter: no list — whatever was requested qualifies —
# but downloads are still tracked, so repeats dedupe.
movies  = process("movies", upstream=quality)

# Ordering note: dedup crowns ONE winner per title, and an on-demand search
# returns the same candidates every run — so if the winner turns out to be
# unusable (e.g. a dead-swarm magnet whose metadata never resolves), the same
# dead candidate wins every request and the movie never downloads. If your
# chain gates on fetched metadata (require(fields=["torrent_files"]), content
# filtering), run metainfo_torrent / metainfo_magnet and that require BEFORE
# dedup: candidates must prove their metadata is fetchable before they may
# win. Both metainfo stages run in parallel with caches, so this costs one
# resolve_timeout worst-case. This minimal example hands URLs straight to the
# client instead, so a dead winner is added, stalls, and the torrent-janitor
# pattern cleans it up (see configs/torrent-janitor.star).
best    = process("dedup", upstream=movies)

path    = process("pathfmt", upstream=best, field="download_path",
                  path=movies_path + "/{title} ({video_year})")
seen    = process("seen", upstream=path, local=True, retry_failed=True)
output("transmission", upstream=seen, host="localhost", path="{download_path}")

pipeline("movies-ondemand")  # no schedule — runs only when a push triggers it
