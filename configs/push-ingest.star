# push-ingest.star
#
# Push sources: autobrr (or any announcer) POSTs releases to
#   POST /api/ingest/announces?pipeline=push-grabs
#   Authorization: Bearer $PIPELINER_INGEST_TOKEN
# and the pipeline runs immediately with the pushed entries. The usual
# filter chain applies — a push is just a faster source.
#
# Requires PIPELINER_INGEST_TOKEN in the daemon environment.

src  = input("webhook", queue="announces")
meta = process("metainfo_file", upstream=src)
req  = process("require", upstream=meta,
               fields=["title", "series_episode_id", "series_season",
                       "series_episode", "_quality"])
qual = process("quality", upstream=req, spec="720p+")
flt  = process("series", upstream=qual)
out  = output("transmission", upstream=flt, host="localhost")
pipeline("push-grabs")  # no schedule: runs only when pushed (or manually)
