# tv-series-deluge.star
#
# Downloads HD episodes for a static show list via Deluge.
#
# Replace feed URL, show list, Deluge host/password, and tv_path.

deluge_host = "localhost"
deluge_pass = "changeme"
tv_path     = "/media/tv"

src    = input("rss", url="https://example.com/rss/torrents")
seen   = process("seen",             upstream=src)
q      = process("metainfo_quality", upstream=seen)
series = process("series",           upstream=q,
                  tracking="strict", quality="720p",
                  static=["Breaking Bad", "Better Call Saul",
                           "The Wire", "Severance"])
fmt    = process("pathfmt", upstream=series,
                  path=tv_path + "/{title}/Season {series_season:02d}",
                  field="download_path")
output("deluge", upstream=fmt,
       host=deluge_host, password=deluge_pass,
       move_completed_path="{download_path}")

pipeline("tv-series", schedule="1h")
