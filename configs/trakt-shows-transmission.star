# trakt-shows-transmission.star
#
# Downloads TV episodes for shows on the Trakt watchlist and trending list
# via Transmission.

trakt_client_id     = "YOUR_TRAKT_CLIENT_ID"
trakt_client_secret = "YOUR_TRAKT_CLIENT_SECRET"
tv_path             = "/media/tv"

def common_input():
    return [
        plugin("rss", url="https://example.com/rss/shows"),
        plugin("seen"),
    ]

def transmission():
    return plugin("transmission",
        host="localhost", port=9091,
        path="{download_path}")

# Downloads episodes for every show on the Trakt watchlist.
# series.from fetches the watchlist (cached for 2h) and uses it as the show list.
task("tv-watchlist",
    common_input() + [
        plugin("metainfo_quality"),
        plugin("series", {
            "tracking": "strict",
            "quality":  "720p",
            "ttl":      "2h",
            "from": [{"name": "trakt_list",
                      "client_id":     trakt_client_id,
                      "client_secret": trakt_client_secret,
                      "type": "shows",
                      "list": "watchlist"}],
        }),
        plugin("metainfo_trakt", client_id=trakt_client_id, type="shows"),
        plugin("metainfo_tvdb", api_key="YOUR_TVDB_API_KEY"),
        plugin("pathfmt",
            path=tv_path + "/{title}/Season {series_season:02d}",
            field="download_path"),
        transmission(),
    ],
    schedule="1h")

# A second task pulling from the Trakt trending list.
# Uses backfill tracking so it can catch up on already-started shows.
task("tv-trending",
    common_input() + [
        plugin("metainfo_quality"),
        plugin("series", {
            "tracking": "backfill",
            "quality":  "1080p",
            "ttl":      "6h",
            "from": [{"name": "trakt_list",
                      "client_id": trakt_client_id,
                      "type":      "shows",
                      "list":      "trending",
                      "limit":     50}],
        }),
        plugin("pathfmt",
            path=tv_path + "/{title}/Season {series_season:02d}",
            field="download_path"),
        transmission(),
    ],
    schedule="6h")
