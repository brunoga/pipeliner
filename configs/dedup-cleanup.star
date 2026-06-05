# dedup-cleanup.star
#
# Scans a media library directory tree, identifies duplicate copies of the
# same movie or episode, and deletes the lower-quality copies — keeping the
# best copy of each title on disk.
#
# Pipeline shape:
#
#   filesystem → metainfo_file → dedup → swap_state → { exec(rm), print }
#
# Why the swap_state node?
#
# `dedup` picks the best-quality copy and Rejects the rest. By default a sink
# only acts on Accepted entries, so an `exec("rm {file_location}")` placed
# directly after dedup would delete the *winners* and leave the losers on
# disk — the opposite of what we want.
#
# `swap_state(swap=["accepted", "rejected"])` flips the two populations in a
# single node:
#
#   winners (Accepted by dedup)  →  Rejected  → never reach exec
#   losers  (Rejected by dedup)  →  Accepted  → deleted by exec
#
# The bidirectional swap is what makes a single sink correctly target only
# the duplicates: with a one-way "force-accept rejected", winners would still
# be Accepted and would also get rm'd.
#
# # Safety
#
# Manually triggered — no `schedule=`. Run with:
#
#     pipeliner run dedup-cleanup
#
# and watch the print output before letting anything delete for real.
# To preview without deleting, replace the exec command with `echo` or run
# the whole pipeline under `--dry-run` to skip every sink.

library = "/data/media"

scan = input("filesystem", path=library, recursive=True)
meta = process("metainfo_file", upstream=scan)
best = process("dedup",         upstream=meta)
flip = process("swap_state",    upstream=best, swap=["accepted", "rejected"])

# Fan-out: same upstream feeds both sinks so each gets an independent slice.
# The print runs alongside exec so the log records every deletion as it happens.
output("print", upstream=flip, format="deleting duplicate: {file_location}")
output("exec",  upstream=flip, command="rm -f {file_location}")

pipeline("dedup-cleanup")
