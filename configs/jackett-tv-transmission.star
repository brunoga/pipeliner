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

results = process("discover", upstream=shows,
    search=[{"name":     "jackett",
             "url":      jackett_url,
             "api_key":  jackett_key,
             "indexers": ["all"],
             "categories": [5000, 5030, 5040]}],
    interval="6h")

# fields=["source_id"] keys the seen fingerprint on Jackett's GUID — the
# tracker permalink — instead of the default "url". Jackett re-encrypts its
# proxy download links on every search, so a URL-keyed fingerprint sees the
# same release as new every run; the GUID does not move.
#
# You do not strictly need this: seen also maintains a secondary index on
# every durable identifier an entry carries (info hash first, then source_id),
# so a release is recognised either way. Naming it here makes the primary key
# stable too, which is worth doing in a NEW pipeline.
#
# Do NOT add it to a pipeline that already has history: the fingerprint is a
# hash of the named fields, so changing them re-keys the store and everything
# previously seen looks new again.
seen   = process("seen",          upstream=results, fields=["source_id"])
meta   = process("metainfo_file", upstream=seen)
req    = process("require",       upstream=meta,
                  fields=["title", "series_episode_id", "series_season",
                          "series_episode", "_quality"])
q      = process("quality",       upstream=req, spec="720p+")
series = process("series",        upstream=q,
                  static=["Breaking Bad", "Better Call Saul", "The Wire"])
cond   = process("condition",     upstream=series, reject="torrent_seeds < 3")
fmt    = process("pathfmt",       upstream=cond,
                  path="/media/tv/{title}/Season {series_season:02d}",
                  field="download_path")
output("transmission", upstream=fmt,
       host="localhost", port=9091,
       path="{download_path}")

pipeline("tv-jackett", schedule="6h")
