# library-aware-tv.star
#
# Disk-truth dedup and quality upgrades: the library filter indexes what is
# actually on disk (including content acquired outside pipeliner) and
#   - rejects releases already present at equal-or-better quality,
#   - lets strictly-better releases through as upgrades (upgrade=True is the
#     default; set upgrade=False to freeze the library at current quality).
#
# Placement: right after metainfo_file, before the series filter, so the
# tracker only ever sees releases the library does not already satisfy.

src  = input("rss", url="https://feeds.example.com/tv.rss")
meta = process("metainfo_file", upstream=src)
lib  = process("library", upstream=meta,
               paths=["/mnt/media/tv", "/mnt/media/movies"],
               ttl="15m")
req  = process("require", upstream=lib,
               fields=["title", "series_episode_id", "series_season",
                       "series_episode", "_quality"])
qual = process("quality", upstream=req, spec="720p+")
flt  = process("series", upstream=qual, static=["Breaking Bad"])
out  = output("transmission", upstream=flt, host="localhost")
pipeline("library-aware-tv", schedule="1h")
