# tvdb-favorites-deluge.star
#
# Downloads HD episodes for every show in the TheTVDB favorites list via Deluge.

tvdb_api_key  = "YOUR_TVDB_API_KEY"
tvdb_user_pin = "YOUR_TVDB_USER_PIN"
deluge_host   = "localhost"
deluge_pass   = "changeme"
tv_path       = "/media/tv"

def common_input():
    return [
        plugin("rss", url="https://example.com/rss/shows"),
        plugin("seen"),
    ]

task("tv-favorites",
    common_input() + [
        plugin("metainfo_quality"),
        plugin("series", {
            "tracking": "strict",
            "quality":  "720p",
            "ttl":      "2h",
            "from": [{"name":     "tvdb_favorites",
                      "api_key":  tvdb_api_key,
                      "user_pin": tvdb_user_pin}],
        }),
        plugin("metainfo_tvdb", api_key=tvdb_api_key),
        plugin("pathfmt",
            path=tv_path + "/{title}/Season {series_season:02d}",
            field="download_path"),
        plugin("deluge",
            host=deluge_host, password=deluge_pass,
            path="{download_path}"),
    ],
    schedule="1h")
