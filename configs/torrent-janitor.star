# torrent-janitor.star
#
# Close the download loop: clean up the Transmission session and recover
# from failed grabs.
#
# Pipeline 1 (seed-janitor): remove torrents that have earned their keep —
# seeding with ratio >= 2.0 or more than 3 days (259200s) of seed time.
# Entries that don't match the condition stay undecided, so the
# torrent_control sink never sees them.
#
# Pipeline 2 (failed-grab-recovery): torrent_failed accepts dead torrents
# (errored in the client, or stalled/zero-progress for longer than
# stall_timeout) and fans out to two sinks:
#
#   - mark_failed writes the original release URL (resolved through the
#     grab records the transmission/qbittorrent sinks store at add time)
#     into the shared seen_failed bucket and un-tracks the episode/movie,
#     so a *different* release of the same content can be grabbed on the
#     next run of your download pipelines — put retry_failed=True on their
#     seen filter so the exact failed release stays blocked.
#   - torrent_control remove_with_data deletes the dead torrent AND its
#     partial data from disk; a pushover notify confirms each removal.
#
# Requirements: TRANSMISSION_HOST (optional), PUSHOVER_USER, PUSHOVER_TOKEN.

trans_host     = env("TRANSMISSION_HOST", default="localhost")
pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")

# ── Pipeline 1: remove well-seeded torrents ──────────────────────────────────

sess1 = input("torrent_session", backend="transmission", host=trans_host)
done  = process("condition", upstream=sess1, rules=[
    {"accept": 'torrent_state == "seeding" and (torrent_ratio >= 2.0 or torrent_seed_time > 259200)'},
    {"reject": "true"},
])
output("torrent_control", upstream=done,
       action="remove", backend="transmission", host=trans_host)

pipeline("seed-janitor", schedule="6h")

# ── Pipeline 2: detect dead grabs, mark them failed, purge them ──────────────

sess2  = input("torrent_session", backend="transmission", host=trans_host)
failed = process("torrent_failed", upstream=sess2, stall_timeout="4h")

# Branch 1: record the failure so the release is never re-grabbed and the
# episode/movie is un-tracked (a different release can be tried).
output("mark_failed", upstream=failed)

# Branch 2: purge the dead torrent and its partial files, then notify.
purged = output("torrent_control", upstream=failed,
                action="remove_with_data", backend="transmission", host=trans_host)
output("notify", upstream=purged, via="pushover",
       config={"user": pushover_user, "token": pushover_token},
       title="Dead torrent purged",
       body="{{range .Entries}}Purged: {{.Title}} — {{.AcceptReason}}\n{{end}}")

pipeline("failed-grab-recovery", schedule="1h")
