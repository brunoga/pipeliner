# route-tv-movies.star
#
# Splits a single RSS feed into TV and movie branches using route().
#
# route() declares the two branches mutually exclusive — the DAG validator
# applies union field semantics at any merge point so there are no spurious
# warnings, and entries that match neither port are rejected with a WARN log
# rather than silently dropped.
#
# Compare with multi-client.star which achieves a similar result using
# two independent processor chains. route() is preferred when entries must
# go to exactly one branch.

rss_url    = "https://feeds.example.com/all"
trans_host = "localhost"
qbit_host  = "localhost"
tv_path    = "/media/tv"
movie_path = "/media/movies"

src  = input("rss",            url=rss_url)
seen = process("seen",          upstream=src)
meta = process("metainfo_file", upstream=seen)   # sets media_type + series_*/movie_* + quality

# metainfo_file stamps media_type with the detected classification.
# Route on that field — cleaner than checking series_episode_id presence.
routes = route(meta,
    tv     = "media_type == 'series'",
    movies = "media_type == 'movie'")

# ── TV branch ──────────────────────────────────────────────────────────────
series   = process("series",  upstream=routes.tv,
    static=["Breaking Bad", "Severance", "The Wire"],
    tracking="strict", quality="720p+")
tv_dedup = process("dedup",   upstream=series)
tv_fmt   = process("pathfmt", upstream=tv_dedup,
    path=tv_path + "/{title}/Season {series_season:02d}",
    field="download_path")
output("transmission", upstream=tv_fmt,
    host=trans_host, path="{download_path}")

# ── Movies branch ──────────────────────────────────────────────────────────
movies     = process("movies",  upstream=routes.movies,
    static=["Dune Part Two", "Oppenheimer", "The Batman"],
    quality="1080p+")
mv_dedup   = process("dedup",   upstream=movies)
movie_fmt  = process("pathfmt", upstream=mv_dedup,
    path=movie_path + "/{title} ({video_year})",
    field="download_path")
output("qbittorrent", upstream=movie_fmt,
    host=qbit_host, savepath="{download_path}")

pipeline("route-tv-movies", schedule="30m")
