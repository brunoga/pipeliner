# jackett-tv-transmission.star
#
# Downloads HD TV episodes for a configured show list using Jackett as the
# search backend and Transmission as the download client.
#
# Requirements:
#   - Jackett running and reachable; set JACKETT_URL and JACKETT_API_KEY
#   - Transmission running at localhost:9091

jackett_url = env("JACKETT_URL")
jackett_key = env("JACKETT_API_KEY")

task("tv-jackett",
    [
        # Actively search Jackett for each show in the series tracker.
        plugin("discover", {
            "from": "series",
            "via": [{"name": "jackett",
                     "url":      jackett_url,
                     "api_key":  jackett_key,
                     "indexers": ["all"],
                     "categories": [5000, 5030, 5040]}],
            "interval": "6h",
        }),
        plugin("seen"),
        plugin("series",
            static=["Breaking Bad", "Better Call Saul", "The Wire"]),
        plugin("quality", min="720p"),
        plugin("condition", accept="torrent_seeds >= 3"),
        plugin("metainfo_series"),
        plugin("pathfmt",
            path="/media/tv/{title}/Season {series_season:02d}",
            field="download_path"),
        plugin("transmission",
            host="localhost", port=9091,
            path="{download_path}"),
    ],
    schedule="6h")
