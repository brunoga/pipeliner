# jackett-tv-transmission.star
#
# Downloads HD TV episodes for a configured show list using Jackett as the
# active search backend and Transmission as the download client.
#
# Requirements: JACKETT_URL and JACKETT_API_KEY environment variables set.

jackett_url = env("JACKETT_URL", default="http://localhost:9117")
jackett_key = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")

# Use jackett as a source that actively searches for each show title.
# Upstream: a trakt_list source providing show titles → discover searches each.
shows = input("trakt_list",
    client_id=env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID"),
    client_secret=env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET"),
    type="shows", list="watchlist")

results = process("discover", from_=shows,
    via=[{"name":     "jackett",
          "url":      jackett_url,
          "api_key":  jackett_key,
          "indexers": ["all"],
          "categories": [5000, 5030, 5040]}],
    interval="6h")

seen   = process("seen",             from_=results)
q      = process("metainfo_quality", from_=seen)
series = process("series",           from_=q,
                  static=["Breaking Bad", "Better Call Saul", "The Wire"])
flt    = process("quality",          from_=series, min="720p")
cond   = process("condition",        from_=flt, accept="torrent_seeds >= 3")
meta   = process("metainfo_series",  from_=cond)
fmt    = process("pathfmt",          from_=meta,
                  path="/media/tv/{title}/Season {series_season:02d}",
                  field="download_path")
output("transmission", from_=fmt,
       host="localhost", port=9091,
       path="{download_path}")

pipeline("tv-jackett", schedule="6h")
