# premiere-new-shows.star
#
# Automatically discovers and downloads the premiere episode (S01E01) of
# new series appearing in an RSS feed. Pair with your main TV pipeline —
# once a premiere is downloaded, add the show to series.static to track
# ongoing episodes.
#
# The premiere plugin accepts all spec-matching quality variants so dedup
# can pick the best copy. If the download fails the show is not marked seen
# and will be retried on the next run.
#
# reject_unmatched=False lets non-episode entries pass through silently so
# this pipeline can sit in front of a broader feed without dropping movies.

rss_url    = "https://feeds.example.com/all"
trans_host = "localhost"

src  = input("rss",            url=rss_url)
seen = process("seen",         upstream=src)
meta = process("metainfo_file", upstream=seen)   # classifies + sets series_*, _quality, etc.
prem = process("premiere",     upstream=meta,
    quality="720p+ webrip+",   # only try new shows that meet a minimum bar
    season=1, episode=1,       # default, shown explicitly for clarity
    reject_unmatched=False)    # pass movies and other non-episode entries through
best = process("dedup",        upstream=prem)
output("transmission", upstream=best, host=trans_host)

pipeline("premiere-discovery", schedule="2h")
