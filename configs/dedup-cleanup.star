# dedup-cleanup.star
#
# Scans a media library directory tree, identifies duplicate copies of the
# same movie or episode, and deletes the lower-quality copies — keeping the
# best copy of each title on disk.
#
# Pipeline shape:
#
#   filesystem → metainfo_file → accept_all → dedup(invert) → { print, delete }
#
# Why invert?
#
# `dedup` keeps the best copy of each item and rejects the rest, so a sink
# placed directly after it would act on the *winners*. `invert=True` turns it
# around: the best copy of each item is set aside (rejected) and only the
# inferior copies stay accepted for the sinks. An entry dedup cannot group
# (no media_type or title) is set aside too, so "no opinion" never reads as
# "delete it".
#
# Flipping a plain dedup with swap_state does not work: a filter drops the
# entries it rejects from its output, so swap_state never sees the losers.
#
# Why accept_all?
#
# dedup only compares accepted entries, and nothing upstream accepts a file
# the filesystem source found.
#
# # Safety
#
# Manually triggered — no `schedule=`. Preview first with
#
#     pipeliner run dedup-cleanup --dry-run
#
# which skips every sink, then read the print output of a real run as it
# happens. `delete` refuses any file outside `roots` and never removes a root.

library = "/data/media"

scan  = input("filesystem", path=library, recursive=True)
meta  = process("metainfo_file", upstream=scan)
cands = process("accept_all",    upstream=meta)
extra = process("dedup",         upstream=cands, invert=True)

# Fan-out: same upstream feeds both sinks so each gets an independent slice.
# The print runs alongside delete so the log records every deletion.
output("print",  upstream=extra, format="deleting duplicate: {file_location}")
output("delete", upstream=extra, roots=[library])

pipeline("dedup-cleanup")
