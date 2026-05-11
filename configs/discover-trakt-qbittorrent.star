# discover-trakt-qbittorrent.star
#
# Actively searches for movies and TV shows from Trakt watchlists using
# Jackett as the search backend and qBittorrent as the download client.
#
# discover receives the Trakt list as upstream source nodes and searches
# Jackett for each title found. This makes the topology explicit in the config.
#
# Requirements: TRAKT_CLIENT_ID, TRAKT_CLIENT_SECRET, JACKETT_URL, JACKETT_API_KEY

trakt_client_id     = env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID")
trakt_client_secret = env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET")
jackett_url         = env("JACKETT_URL", default="http://localhost:9117")
jackett_key         = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")
tmdb_key            = env("TMDB_API_KEY", default="YOUR_TMDB_KEY")
qbit_host           = "localhost"
qbit_port           = 8080
qbit_user           = "admin"
qbit_pass           = "changeme"
movies_path         = "/media/movies"
tv_path             = "/media/tv"

def jackett_via():
    return [{"name": "jackett",
             "url":      jackett_url,
             "api_key":  jackett_key,
             "indexers": ["all"]}]

def qbit_output(upstream, savepath, category):
    fmt = process("pathfmt", from_=upstream, path=savepath, field="download_path")
    output("qbittorrent", from_=fmt,
           host=qbit_host, port=qbit_port,
           username=qbit_user, password=qbit_pass,
           savepath="{download_path}", category=category)

# ── Pipeline 1: discover movies from Trakt watchlist ─────────────────────────

movie_watchlist = input("trakt_list",
    client_id=trakt_client_id, client_secret=trakt_client_secret,
    type="movies", list="watchlist")
disc_movies  = process("discover", from_=movie_watchlist,
                        via=jackett_via(), interval="6h")
seen_movies  = process("seen",            from_=disc_movies)
q_movies     = process("metainfo_quality", from_=seen_movies)
tmdb_movies  = process("metainfo_tmdb",   from_=q_movies, api_key=tmdb_key)
flt_movies   = process("movies",           from_=tmdb_movies,
                        quality="1080p+",
                        **{"from": [{"name": "trakt_list",
                                     "client_id":     trakt_client_id,
                                     "client_secret": trakt_client_secret,
                                     "type": "movies", "list": "watchlist"}]})
cond_movies  = process("condition", from_=flt_movies, rules=[
    {"reject": 'video_source == "CAM"'},
    {"reject": 'video_rating < 6.5'},
])
qbit_output(cond_movies,
            savepath=movies_path + "/{title} ({video_year})",
            category="movies")

pipeline("discover-movies", schedule="6h")

# ── Pipeline 2: discover TV shows from Trakt watchlist ───────────────────────

show_watchlist = input("trakt_list",
    client_id=trakt_client_id, client_secret=trakt_client_secret,
    type="shows", list="watchlist")
disc_shows  = process("discover", from_=show_watchlist,
                       via=jackett_via(), interval="3h")
seen_shows  = process("seen",            from_=disc_shows)
q_shows     = process("metainfo_quality", from_=seen_shows)
flt_shows   = process("series",           from_=q_shows,
                        tracking="strict", quality="720p+",
                        **{"from": [{"name": "trakt_list",
                                     "client_id":     trakt_client_id,
                                     "client_secret": trakt_client_secret,
                                     "type": "shows", "list": "watchlist"}]})
qbit_output(flt_shows,
            savepath=tv_path + "/{title}/Season {series_season:02d}",
            category="tv")

pipeline("discover-shows", schedule="3h")
