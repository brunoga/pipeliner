# tv-series-deluge.star
#
# Downloads HD episodes for a static show list via Deluge.

deluge_host = "localhost"
deluge_pass = "changeme"
tv_path     = "/media/tv"

def common_input():
    return [
        plugin("rss", url="https://example.com/rss/torrents"),
        plugin("seen"),
    ]

task("tv-series",
    common_input() + [
        plugin("metainfo_quality"),
        plugin("series",
            tracking="strict",
            quality="720p",
            static=["Breaking Bad", "Better Call Saul", "The Wire", "Severance"]),
        plugin("pathfmt",
            path=tv_path + "/{title}/Season {series_season:02d}",
            field="download_path"),
        plugin("deluge",
            host=deluge_host, password=deluge_pass,
            path="{download_path}"),
    ],
    schedule="1h")
