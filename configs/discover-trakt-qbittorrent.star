# discover-trakt-qbittorrent.star
#
# Actively searches for movies and TV episodes from the Trakt watchlist using
# Jackett as the search backend and qBittorrent as the download client.
#
# Requirements:
#   - Jackett running at jackett_url with the given api_key
#   - qBittorrent running and reachable
#   - TRAKT_CLIENT_ID and TRAKT_CLIENT_SECRET set as environment variables

trakt_client_id     = env("TRAKT_CLIENT_ID")
trakt_client_secret = env("TRAKT_CLIENT_SECRET")
jackett_url         = "https://jackett.example.com"
jackett_key         = "YOUR_JACKETT_API_KEY"
qbit_host           = "localhost"
qbit_port           = 8080
qbit_user           = "admin"
qbit_pass           = "changeme"
movies_path         = "/media/movies"
tv_path             = "/media/tv"

# ── shared helpers ───────────────────────────────────────────────────────────

def trakt_movies():
    return [{"name": "trakt_list",
             "client_id": trakt_client_id,
             "client_secret": trakt_client_secret,
             "type": "movies",
             "list": "watchlist"}]

def trakt_shows():
    return [{"name": "trakt_list",
             "client_id": trakt_client_id,
             "client_secret": trakt_client_secret,
             "type": "shows",
             "list": "watchlist"}]

def jackett_via():
    return [{"name": "rss_search",
             "url_template": jackett_url + "/api/v2.0/indexers/all/results/torznab?q={QueryEscaped}&apikey=" + jackett_key}]

def qbit(savepath, category):
    return plugin("qbittorrent",
        host=qbit_host, port=qbit_port,
        username=qbit_user, password=qbit_pass,
        savepath=savepath, category=category)

# ── tasks ─────────────────────────────────────────────────────────────────────

# Actively searches for movies from the Trakt watchlist.
task("discover-movies",
    [
        plugin("discover", {
            "ttl": "4h",
            "from": trakt_movies(),
            "via":  jackett_via(),
            "interval": "6h",
        }),
        plugin("seen"),
        plugin("metainfo_quality"),
        plugin("metainfo_magnet", resolve_timeout="30s"),
        plugin("movies", {
            "quality": "1080p",
            "ttl":     "4h",
            "from":    trakt_movies(),
        }),
        plugin("metainfo_tmdb", api_key="YOUR_TMDB_API_KEY"),
        plugin("condition", rules=[
            {"reject": 'video_source == "CAM"'},
            {"reject": 'video_rating < 6.5'},
        ]),
        plugin("pathfmt",
            path=movies_path + "/{title} ({video_year})",
            field="download_path"),
        qbit(savepath="{download_path}", category="movies"),
    ],
    schedule="6h")

# Actively searches for TV episodes from the Trakt watchlist.
task("discover-shows",
    [
        plugin("discover", {
            "ttl": "2h",
            "from": trakt_shows(),
            "via":  jackett_via(),
            "interval": "3h",
        }),
        plugin("seen"),
        plugin("metainfo_quality"),
        plugin("series", {
            "tracking": "strict",
            "quality":  "720p",
            "ttl":      "2h",
            "from":     trakt_shows(),
        }),
        plugin("pathfmt",
            path=tv_path + "/{title}/Season {series_season:02d}",
            field="download_path"),
        qbit(savepath="{download_path}", category="tv"),
    ],
    schedule="3h")
