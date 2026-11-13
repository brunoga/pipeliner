# torrent-retention.star
#
# Removes finished torrents from Deluge once they have seeded enough. Only
# the torrent is removed; the files stay where they are. The rule differs per
# tracker, which a cron job built around one rule could not express:
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
# Rules are evaluated in order and the first that fires decides. Change the
# tracker name and thresholds to suit; run it as a dry run first to see what
# it would remove.
#
# Requirements: DELUGE_PASSWORD

DELUGE_HOST = env("DELUGE_HOST", default="localhost")
DELUGE_PASSWORD = env("DELUGE_PASSWORD", default="deluge")

DAY = 86400

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

# "remove" keeps the data; "remove_with_data" would delete the files too.
output("torrent_control", upstream=due, action="remove", backend="deluge",
       host=DELUGE_HOST, password=DELUGE_PASSWORD)

pipeline("torrent-retention", schedule="0 0 * * *")
