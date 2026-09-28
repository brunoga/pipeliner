# settle-window.star
#
# The settle window, and the node ordering it requires.
#
# Releases arrive in waves — 1080p, then 2160p, then an HDR pass, then an Atmos
# remux, often within hours. Grabbing on sight downloads every rung of that
# ladder. `settle` holds an item's releases for a while, records every one it
# sees, and when the window closes releases the whole wave into the pipeline so
# the gates and dedup pick the winner.
#
# ── The ordering that matters ────────────────────────────────────────────────
#
# `dedup` collapses a wave to one release per item, and it chooses on quality
# tags because that is all it has. So every node that can *refuse* a release
# must run BEFORE dedup. Placed after it, the alternatives are already gone by
# the time the refusal happens, and the item is lost — not just for that run
# but for every run after it, because the same wave comes back.
#
#   movies(settle) → enrichment → nodes that can refuse → dedup → sink
#
# Two constraints fall out of that:
#
#   * `bitrate` needs `video_runtime`, which only TMDb/TVDB enrichment
#     supplies, so it cannot run before `metainfo_tmdb`. That is also why it
#     is a separate plugin from `quality`: `quality` matches on the release
#     name alone and can run on the raw feed; `bitrate` cannot.
#   * Enrichment stays BELOW the settle filter. Above it, a network lookup
#     runs on the whole feed instead of the handful of releases the filter
#     accepts — on a real feed that is a 50x difference in API calls.
#
# tests/integration/order_test.go holds this as a test: the same feed and the
# same gate, with dedup on either side, yield nothing and the runner-up.
#
# Requirements: TMDB_API_KEY, a movie feed, a download client.

tmdb_key    = env("TMDB_API_KEY", default="YOUR_TMDB_API_KEY")
movies_path = "/media/movies"

src  = input("rss", url=env("MOVIE_FEED", default="https://example.com/rss/movies"))
meta = process("metainfo_file", upstream=src)
req  = process("require", upstream=meta, fields=["title", "video_year", "_quality"])

# Cheap, name-only gate: runs on the raw feed, needs no enrichment.
q = process("quality", upstream=req, spec="1080p+")

# The settle window lives on the movies filter. Every download-worthy release
# for a title is recorded; nothing is grabbed until the window elapses, and
# then the whole wave is released at once.
movies = process("movies", upstream=q, settle="6h",
                 static=["Inception", "Interstellar", "Dune", "Oppenheimer"])

# ── Enrichment and the refusing nodes: after settle, before dedup ───────────

tmdb  = process("metainfo_tmdb", upstream=movies, api_key=tmdb_key, cache_ttl="96h")
ready = process("require", upstream=tmdb,
                fields=["enriched", "torrent_file_size", "video_runtime"])

# Refuses a film in the wrong language.
lang = process("condition", upstream=ready,
               reject='video_language != "" and video_language != "English"')

# Refuses a starved encode: implied bitrate is size / runtime, the one quality
# signal a release name cannot fake. A 2160p release at 10 Mbps is compressed
# hard however good its tags look.
rate = process("bitrate", upstream=lang, min_1080p=2, min_2160p=15)

# ── Only now: pick one release per film, from those that survived ────────────

dedup = process("dedup", upstream=rate)

fmt = process("pathfmt", upstream=dedup,
              path=movies_path + "/{title} ({video_year})", field="download_path")
output("transmission", upstream=fmt, host="localhost", path="{download_path}")

pipeline("movies", schedule="1h")
