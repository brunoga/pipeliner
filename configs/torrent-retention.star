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
# Run each as a dry run first to see what it would remove.
#
# Requirements: DELUGE_PASSWORD; pipeliner must see the media folders at the
# same paths Deluge does.

DELUGE_HOST = env("DELUGE_HOST", default="localhost")
DELUGE_PASSWORD = env("DELUGE_PASSWORD", default="deluge")

MEDIA = "/data/media"
LIBRARIES = [MEDIA + "/series", MEDIA + "/movies", MEDIA + "/3dmovies", MEDIA + "/3d-mvc-inbox"]

DAY = 86400

# --- torrent-retention ------------------------------------------------------

session = input("torrent_session", backend="deluge", host=DELUGE_HOST, password=DELUGE_PASSWORD)

due = process("condition", upstream=session, rules=[
    # Only torrents that have finished and are seeding.
    {"reject": 'torrent_state != "seeding"'},
    # The ratio-strict private tracker: seed to 2.0, then at least 3 days.
    {"accept": 'torrent_tracker_host contains "3dtorrents" and torrent_ratio >= 2.0 and torrent_seed_time >= %d' % (3 * DAY)},
    {"reject": 'torrent_tracker_host contains "3dtorrents"'},
    # Everyone else.
    {"accept": "torrent_seed_time >= %d" % (7 * DAY)},
    {"accept": "torrent_ratio >= 2.0 and torrent_seed_time >= %d" % DAY},
    {"reject": "true"},
])

# Once a torrent stops seeding its files go too; "remove" would keep them.
output("torrent_control", upstream=due, action="remove_with_data", backend="deluge",
       host=DELUGE_HOST, password=DELUGE_PASSWORD)

pipeline("torrent-retention", schedule="0 0 * * *")

# --- media-cleanup ----------------------------------------------------------

files = input("filesystem", path=MEDIA, recursive=True)

old = process("condition", upstream=files, rules=[
    {"accept": "file_modified_time < daysago(7)"},
    {"reject": "true"},
])

free = process("torrent_in_use", upstream=old, backend="deluge", host=DELUGE_HOST,
               password=DELUGE_PASSWORD)

output("delete", upstream=free, roots=LIBRARIES)

# After torrent-retention, so a torrent it removes tonight is gone first.
pipeline("media-cleanup", schedule="30 0 * * *")
