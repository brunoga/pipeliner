# dag-tv-two-feeds.star
#
# Merge two RSS feeds into a single torrent pipeline.
#
# This is the canonical "demultiplexer" pattern: two sources flow into one
# processor chain. The merge node deduplicates by URL so a torrent appearing
# in both feeds is only downloaded once.
#
# Replace the feed URLs, show list, and Transmission host with your values.

feed1 = input("rss", url="https://feeds.example.com/tv/hd")
feed2 = input("rss", url="https://feeds.example.com/tv/full")

# Merge both feeds; duplicate URLs are dropped (first-seen wins).
all_entries = merge(feed1, feed2)

seen    = process("seen",            upstream=all_entries)
quality = process("metainfo_quality", upstream=seen)
series  = process("series",          upstream=quality,
    static=["Breaking Bad", "Severance", "The Bear"],
    tracking="strict",
    quality="720p+")
dedup   = process("dedup",           upstream=series)
pathfmt = process("pathfmt",         upstream=dedup,
    path="/media/tv/{title}/Season {series_season:02d}",
    field="download_path")

output("transmission", upstream=pathfmt,
    host="localhost",
    port=9091,
    path="{download_path}")

pipeline("tv-two-feeds", schedule="30m")
