# trakt-movies-qbittorrent.star
#
# Downloads movies from the Trakt watchlist and ratings list via qBittorrent.
# movies.from fetches the list from Trakt (cached for ttl) and uses it as the
# title filter so only watchlisted movies are accepted.

trakt_client_id     = "YOUR_TRAKT_CLIENT_ID"
trakt_client_secret = "YOUR_TRAKT_CLIENT_SECRET"
qbit_host           = "localhost"
qbit_port           = 8080
qbit_user           = "admin"
qbit_pass           = "changeme"
movies_path         = "/media/movies"

def common_input():
    return [
        plugin("rss", url="https://example.com/rss/movies"),
        plugin("seen"),
    ]

def qbit():
    return plugin("qbittorrent",
        host=qbit_host, port=qbit_port,
        username=qbit_user, password=qbit_pass,
        savepath="{download_path}")

# Movies on the Trakt watchlist — 720p minimum quality.
task("movies-watchlist",
    common_input() + [
        plugin("metainfo_quality"),
        plugin("movies", {
            "quality": "720p",
            "ttl":     "4h",
            "from": [{"name": "trakt_list",
                      "client_id":     trakt_client_id,
                      "client_secret": trakt_client_secret,
                      "type": "movies",
                      "list": "watchlist"}],
        }),
        plugin("metainfo_tmdb", api_key="YOUR_TMDB_API_KEY"),
        plugin("metainfo_trakt", client_id=trakt_client_id, type="movies"),
        plugin("pathfmt",
            path=movies_path + "/{title} ({video_year})",
            field="download_path"),
        qbit(),
    ],
    schedule="2h")

# High-quality picks: movies from the Trakt ratings list, filtered by score.
task("movies-top-rated",
    common_input() + [
        plugin("metainfo_quality"),
        plugin("movies", {
            "quality": "1080p",
            "ttl":     "4h",
            "from": [{"name": "trakt_list",
                      "client_id":     trakt_client_id,
                      "client_secret": trakt_client_secret,
                      "type": "movies",
                      "list": "ratings"}],
        }),
        plugin("metainfo_tmdb", api_key="YOUR_TMDB_API_KEY"),
        plugin("condition", rules=[
            {"reject": 'video_source == "CAM"'},
            {"reject": 'video_rating < 7.5'},
        ]),
        plugin("pathfmt",
            path=movies_path + "/{title} ({video_year})",
            field="download_path"),
        qbit(),
    ],
    schedule="6h")
