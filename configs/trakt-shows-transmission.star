# trakt-shows-transmission.star
#
# Downloads TV episodes for shows on the Trakt watchlist and trending list
# via Transmission. Demonstrates two pipelines sharing helper functions.
#
# Run `pipeliner auth trakt` once to store your OAuth token.

trakt_client_id     = env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID")
trakt_client_secret = env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET")
tvdb_key            = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
tv_path             = "/media/tv"

def enriched_output(upstream):
    """Enrich with TVDB metadata, format path, and send to Transmission."""
    tvdb = process("metainfo_tvdb", upstream=upstream, api_key=tvdb_key)
    fmt  = process("pathfmt", upstream=tvdb,
                   path=tv_path + "/{title}/Season {series_season:02d}",
                   field="download_path")
    output("transmission", upstream=fmt, host="localhost", port=9091,
           path="{download_path}")

# ── Pipeline 1: watchlist (strict tracking, 720p+) ───────────────────────────

_series_required = ["title", "series_episode_id", "series_season",
                    "series_episode", "_quality"]

src1    = input("rss", url="https://example.com/rss/shows")
seen1   = process("seen",          upstream=src1)
meta1   = process("metainfo_file", upstream=seen1)
req1    = process("require",       upstream=meta1, fields=_series_required)
q1      = process("quality",       upstream=req1, spec="720p+")
series1 = process("series",        upstream=q1,
                   tracking="strict", ttl="2h",
                   list=[{"name": "trakt_list",
                          "client_id":     trakt_client_id,
                          "client_secret": trakt_client_secret,
                          "type": "shows", "list": "watchlist"}])
enriched_output(series1)

pipeline("tv-watchlist", schedule="1h")

# ── Pipeline 2: trending (backfill, 1080p+) ───────────────────────────────────

src2    = input("rss", url="https://example.com/rss/shows")
seen2   = process("seen",          upstream=src2)
meta2   = process("metainfo_file", upstream=seen2)
req2    = process("require",       upstream=meta2, fields=_series_required)
q2      = process("quality",       upstream=req2, spec="1080p+")
series2 = process("series",        upstream=q2,
                   tracking="backfill", ttl="6h",
                   list=[{"name": "trakt_list",
                          "client_id": trakt_client_id,
                          "type":      "shows",
                          "list":      "trending",
                          "limit":     50}])
enriched_output(series2)

pipeline("tv-trending", schedule="6h")
