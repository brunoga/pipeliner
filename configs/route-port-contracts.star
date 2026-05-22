# route-port-contracts.star
#
# Demonstrates automatic field inference from route port accept expressions.
#
# Some indexers return entries that carry only a torrent file URL
# (torrent_url), only a magnet link (magnet_url), or sometimes both.
# This pipeline splits entries by URL type, sending each to the client
# that handles it. The DAG validator automatically infers field contracts
# from the port conditions:
#
#   "torrent_url != ''"
#     Presence check → torrent_url is promoted to certain on this branch.
#     A downstream Requires: torrent_url passes cleanly without a ~ warning.
#
#   "magnet_url == ''"
#     Absence check → magnet_url is removed from the reachable field set on
#     this branch. Any downstream node that accidentally Requires magnet_url
#     gets a hard validation error at load time — the mistake is caught before
#     any entry is processed.
#
# No explicit port() contracts needed — the conditions say it all.

jackett_url  = "http://localhost:9117"
jackett_key  = env("JACKETT_API_KEY")
trans_host   = "localhost"   # handles .torrent files
qbit_host    = "localhost"   # handles magnet links

# ── Sources ────────────────────────────────────────────────────────────────

# Jackett may set torrent_url, magnet_url, or both depending on the indexer.
tv_feed  = input("jackett",
    url=jackett_url, api_key=jackett_key,
    query="4K HDTV",  categories=["5000"])
mv_feed  = input("jackett",
    url=jackett_url, api_key=jackett_key,
    query="BluRay Remux", categories=["2000"])

combined = merge(tv_feed, mv_feed)
seen     = process("seen",             upstream=combined)
quality  = process("metainfo_quality", upstream=seen)

# ── Route by URL type — field contracts are automatic ─────────────────────
#
# The validator automatically infers:
#   torrent branch: torrent_url promoted to certain (presence check)
#   magnet branch:  magnet_url  promoted to certain (presence check)

routes = route(quality,
    torrent = "torrent_url != ''",
    magnet  = "magnet_url != ''",
)

# ── Torrent branch ─────────────────────────────────────────────────────────
#
# Only entries guaranteed to have torrent_url reach here.

filt_t = process("quality", upstream=routes.torrent,
    min="2160p", source="webrip+")
output("transmission", upstream=filt_t,
    host=trans_host, port=9091,
    username=env("TRANS_USER", default=""),
    password=env("TRANS_PASS", default=""))

# ── Magnet branch ──────────────────────────────────────────────────────────
#
# Only entries guaranteed to have magnet_url reach here.

filt_m = process("quality", upstream=routes.magnet,
    min="2160p", source="webrip+")
output("qbittorrent", upstream=filt_m,
    host=qbit_host, port=8080,
    username=env("QBIT_USER", default=""),
    password=env("QBIT_PASS", default=""))

pipeline("route-port-contracts", schedule="45m")
