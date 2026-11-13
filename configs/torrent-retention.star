# torrent-retention.star
#
# Two nightly pipelines that keep a download box from filling up without
# hurting share ratios.
#
# torrent-retention removes finished torrents from Deluge once they have
# seeded enough, and their files with them. The rule differs per tracker:
#
#   - a private tracker with a minimum share ratio (here 3dtorrents, which
#     disables accounts below 1.0) keeps its torrents until they reach 2.0
#     themselves, however long that takes, and at least 3 days after that;
#   - everything else goes after 7 days of seeding, or at a 2.0 ratio after a
#     day.
#
# A flat "7 days, whatever the ratio" rule is what took one account to 0.69:
# niche torrents on a private tracker often have no leechers for days, and
# removing them at a 0.0-0.3 ratio makes every download a net loss.
#
# media-cleanup then deletes media files older than 7 days that no torrent
# uses: what an extraction or a conversion produced, and what was left behind
# by torrents removed without their data. torrent_in_use keeps every file a
# torrent still seeds from -- so a 3dtorrents download outliving the 7 days
# keeps seeding -- and keeps everything when Deluge cannot be asked. delete
# never removes a library root, even an emptied one.
#
# torrent-cap keeps Deluge within CAP torrents, under its active-seeding
# limit: past it Deluge queues the extras, which then neither upload nor earn
# bonus points. The excess is removed with its data, least protected first --
# public trackers, then the healthy private one, then the ratio-strict one --
# and oldest first within each; a torrent still downloading is never removed.
#
# Run each as a dry run first to see what it would remove.
#
# Requirements: DELUGE_PASSWORD; pipeliner must see the media folders at the
# same paths Deluge does.

DELUGE_HOST = env("DELUGE_HOST", default="localhost")
DELUGE_PASSWORD = env("DELUGE_PASSWORD", default="deluge")

MEDIA = "/data/media"
LIBRARIES = [MEDIA + "/series", MEDIA + "/movies", MEDIA + "/3dmovies", MEDIA + "/3d-mvc-inbox"]

DAY = 86400
CAP = 290  # under Deluge's max_active_seeding of 300

SMTP = {
    "smtp_host": env("SMTP_HOST", default="smtp.example.com"),
    "smtp_port": 587,
    "username": env("SMTP_USER", default="you@example.com"),
    "password": env("SMTP_PASSWORD", default="YOUR_SMTP_PASSWORD"),
    "sender": env("SMTP_USER", default="you@example.com"),
    "to": env("NOTIFY_TO", default="you@example.com"),
}

# What each run removed, with the share ratio each torrent earned: on a
# ratio-strict tracker that is the number to watch.
REMOVED = """Removed {{len .Entries}} torrent(s), freeing {{filesize (sumfield "torrent_file_size" .Entries)}}; they uploaded {{filesize (sumfield "torrent_uploaded" .Entries)}} in all.
{{range .Entries}}
{{.Title}}
  why:     {{.AcceptReason}}
  ratio:   {{printf "%.2f" (index .Fields "torrent_ratio")}} ({{filesize (index .Fields "torrent_uploaded")}} up / {{filesize (index .Fields "torrent_downloaded")}} down)
  seeded:  {{duration (index .Fields "torrent_seed_time")}}
  tracker: {{with index .Fields "torrent_tracker_host"}}{{.}}{{else}}unknown{{end}}
  freed:   {{filesize (index .Fields "torrent_file_size")}} from {{index .Fields "torrent_download_dir"}}
{{end}}"""

DELETED = """Deleted {{len .Entries}} file(s) older than 7 days that no torrent uses, freeing {{filesize (sumfield "file_size" .Entries)}}.
{{range .Entries}}
{{index .Fields "file_location"}}
  {{filesize (index .Fields "file_size")}}, last modified {{ago (index .Fields "file_modified_time")}}
{{end}}"""

# --- torrent-retention ------------------------------------------------------

session = input("torrent_session", backend="deluge", host=DELUGE_HOST, password=DELUGE_PASSWORD)

due = process("condition", upstream=session, rules=[
    # Only torrents that have finished and are seeding. Each reason says, in
    # the report, why a torrent was removed.
    {"reject": 'torrent_state != "seeding"', "reason": "not seeding"},
    # The ratio-strict private tracker: seed to 2.0, then at least 3 days.
    {"accept": 'torrent_tracker_host contains "3dtorrents" and torrent_ratio >= 2.0 and torrent_seed_time >= %d' % (3 * DAY),
     "reason": "3dtorrents: reached a 2.0 ratio and seeded at least 3 days"},
    {"reject": 'torrent_tracker_host contains "3dtorrents"', "reason": "3dtorrents: seeds until a 2.0 ratio"},
    # Everyone else.
    {"accept": "torrent_seed_time >= %d" % (7 * DAY), "reason": "seeded 7 days"},
    {"accept": "torrent_ratio >= 2.0 and torrent_seed_time >= %d" % DAY, "reason": "reached a 2.0 ratio after a day of seeding"},
    {"reject": "true", "reason": "still seeding"},
])

# Once a torrent stops seeding its files go too; "remove" would keep them.
removed = output("torrent_control", upstream=due, action="remove_with_data", backend="deluge",
                 host=DELUGE_HOST, password=DELUGE_PASSWORD)
output("notify", upstream=removed, via="email", config=SMTP,
       title="Retention: {{len .Entries}} torrent(s) removed", body=REMOVED)

pipeline("torrent-retention", schedule="0 0 * * *",
         description="Removes finished torrents from the client once they have seeded long enough or reached their ratio")

# --- media-cleanup ----------------------------------------------------------

files = input("filesystem", path=MEDIA, recursive=True)

old = process("condition", upstream=files, rules=[
    {"accept": "file_modified_time < daysago(7)"},
    {"reject": "true"},
])

free = process("torrent_in_use", upstream=old, backend="deluge", host=DELUGE_HOST,
               password=DELUGE_PASSWORD)

deleted = output("delete", upstream=free, roots=LIBRARIES)
output("notify", upstream=deleted, via="email", config=SMTP,
       title="Media cleanup: {{len .Entries}} file(s) deleted", body=DELETED)

# After torrent-retention, so a torrent it removes tonight is gone first.
pipeline("media-cleanup", schedule="30 0 * * *",
         description="Deletes downloaded files that no torrent in the client still uses")

# --- torrent-cap ------------------------------------------------------------

capped = input("torrent_session", backend="deluge", host=DELUGE_HOST, password=DELUGE_PASSWORD)

# Higher keeps: 9 still downloading, 2 the ratio-strict tracker, 1 the other
# private one, 0 everything else; then the newest first within each.
ranked = process("set", upstream=capped, keep_rank='{{$h := default "" .torrent_tracker_host}}' +
    '{{if ne .torrent_state "seeding"}}9{{else if contains "3dtorrents" $h}}2' +
    '{{else if contains "torrenting" $h}}1{{else}}0{{end}}-{{.torrent_added_at.Unix}}')
candidates = process("accept_all", upstream=ranked)

# invert: the top CAP are kept and everything below them goes on.
excess = process("limit", upstream=candidates, n=CAP, sort="keep_rank", order="desc", invert=True)

trimmed = output("torrent_control", upstream=excess, action="remove_with_data", backend="deluge",
                 host=DELUGE_HOST, password=DELUGE_PASSWORD)
output("notify", upstream=trimmed, via="email", config=SMTP,
       title="Torrent cap: {{len .Entries}} torrent(s) removed", body=REMOVED)

pipeline("torrent-cap", schedule="45 * * * *",
         description="Keeps the client under a torrent count by removing the oldest, least valuable torrents first")
