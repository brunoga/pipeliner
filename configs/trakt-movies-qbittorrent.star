# trakt-movies-qbittorrent.star
#
# Downloads movies from the Trakt watchlist and high-rated list via qBittorrent.
# Two pipelines with different quality floors and filters.
#
# Run `pipeliner auth trakt` once to store your OAuth token.

trakt_client_id     = env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID")
trakt_client_secret = env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET")
tmdb_key            = env("TMDB_API_KEY", default="YOUR_TMDB_KEY")
qbit_host           = "localhost"
qbit_port           = 8080
qbit_user           = "admin"
qbit_pass           = "changeme"
movies_path         = "/media/movies"

def qbit_output(upstream, category):
    fmt = process("pathfmt", from_=upstream,
                  path=movies_path + "/{title} ({video_year})",
                  field="download_path")
    output("qbittorrent", from_=fmt,
           host=qbit_host, port=qbit_port,
           username=qbit_user, password=qbit_pass,
           savepath="{download_path}", category=category)

# ── Pipeline 1: watchlist (720p+) ────────────────────────────────────────────

src1    = input("rss", url="https://example.com/rss/movies")
seen1   = process("seen",             from_=src1)
q1      = process("metainfo_quality", from_=seen1)
tmdb1   = process("metainfo_tmdb",   from_=q1, api_key=tmdb_key)
movies1 = process("movies",           from_=tmdb1,
                   quality="720p+", ttl="4h",
                   **{"from": [{"name": "trakt_list",
                                "client_id":     trakt_client_id,
                                "client_secret": trakt_client_secret,
                                "type": "movies", "list": "watchlist"}]})
qbit_output(movies1, category="movies")

pipeline("movies-watchlist", schedule="2h")

# ── Pipeline 2: top-rated (1080p+, rating filter) ────────────────────────────

src2    = input("rss", url="https://example.com/rss/movies")
seen2   = process("seen",             from_=src2)
q2      = process("metainfo_quality", from_=seen2)
tmdb2   = process("metainfo_tmdb",   from_=q2, api_key=tmdb_key)
movies2 = process("movies",           from_=tmdb2,
                   quality="1080p+", ttl="4h",
                   **{"from": [{"name": "trakt_list",
                                "client_id":     trakt_client_id,
                                "client_secret": trakt_client_secret,
                                "type": "movies", "list": "ratings"}]})
cond2   = process("condition", from_=movies2, rules=[
    {"reject": 'video_source == "CAM"'},
    {"reject": 'video_rating < 7.5'},
])
qbit_output(cond2, category="movies")

pipeline("movies-top-rated", schedule="6h")
