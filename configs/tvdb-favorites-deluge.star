# tvdb-favorites-deluge.star
#
# Downloads HD episodes for every show in the TheTVDB favorites list via Deluge.
# tvdb_favorites is used both as a standalone source (for the show list) and
# to drive metainfo enrichment.

tvdb_api_key  = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
tvdb_user_pin = env("TVDB_USER_PIN", default="YOUR_TVDB_PIN")
deluge_host   = "localhost"
deluge_pass   = "changeme"
tv_path       = "/media/tv"

src     = input("rss", url="https://example.com/rss/shows")
seen    = process("seen",             from_=src)
q       = process("metainfo_quality", from_=seen)
series  = process("series",           from_=q,
                   tracking="strict", quality="720p+", ttl="2h",
                   **{"from": [{"name":     "tvdb_favorites",
                                "api_key":  tvdb_api_key,
                                "user_pin": tvdb_user_pin}]})
tvdb    = process("metainfo_tvdb",    from_=series, api_key=tvdb_api_key)
fmt     = process("pathfmt",          from_=tvdb,
                   path=tv_path + "/{title}/Season {series_season:02d}",
                   field="download_path")
output("deluge", from_=fmt,
       host=deluge_host, password=deluge_pass,
       move_completed_path="{download_path}")

pipeline("tv-favorites", schedule="1h")
