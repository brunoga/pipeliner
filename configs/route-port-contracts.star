# route-port-contracts.star
#
# Demonstrates port() field contracts with route().
#
# Some indexers return entries that carry only a torrent file URL
# (torrent_url), only a magnet link (magnet_url), or sometimes both.
# This pipeline splits entries by URL type, sending each to the client
# that handles it, using field contracts to make the validator enforce
# that each branch only uses the URL field it is guaranteed to have.
#
# Field contracts with port():
#
#   guarantees=["torrent_url"]
#     The DAG validator treats torrent_url as definitely present on entries
#     through this port, even if the upstream only MayProduce it.
#     A downstream Requires: torrent_url passes cleanly without a ~ warning.
#
#   masks=["magnet_url"]
#     The DAG validator removes magnet_url from the available field set on
#     this branch.  Any downstream node that accidentally Requires magnet_url
#     gets a hard validation error at load time — the mistake is caught before
#     any entry is processed.  At runtime the field is also stripped from
#     passing entries so the data matches the validator's model.
#
# Without port() contracts both branches would inherit both fields from the
# upstream, the validator could not distinguish them, and wiring mistakes
# (e.g. using a magnet-only client on the torrent branch) would only surface
# as runtime failures.

jackett_url  = "http://localhost:9117"
jackett_key  = env("JACKETT_API_KEY")
trans_host   = "localhost"   # handles .torrent files
qbit_host    = "localhost"   # handles magnet links

# ── Sources ────────────────────────────────────────────────────────────────

# Jackett may set torrent_url, magnet_url, or both depending on the indexer.
tv_feed  = input("jackett_search",
    url=jackett_url, api_key=jackett_key,
    query="4K HDTV",  categories=["5000"])
mv_feed  = input("jackett_search",
    url=jackett_url, api_key=jackett_key,
    query="BluRay Remux", categories=["2000"])

combined = merge(tv_feed, mv_feed)
seen     = process("seen",             upstream=combined)
quality  = process("metainfo_quality", upstream=seen)

# ── Route by URL type — with field contracts ───────────────────────────────

routes = route(quality,
    # Entries with a torrent file URL → Transmission (file-based client).
    # The validator knows torrent_url is definitely present here and that
    # magnet_url is forbidden — accidentally wiring a magnet-only client
    # to this branch would be caught at load time.
    torrent = port("torrent_url != ''",
                   guarantees=["torrent_url"],
                   masks=["magnet_url"]),

    # Entries with only a magnet link → qBittorrent (magnet-capable client).
    magnet  = port("magnet_url != ''",
                   guarantees=["magnet_url"],
                   masks=["torrent_url"]),
)

# ── Torrent branch ─────────────────────────────────────────────────────────
#
# Only entries guaranteed to have torrent_url reach here.
# The DAG validator enforces that no plugin on this branch requires
# magnet_url — which would be a hard error, not a soft warning.

filt_t = process("quality", upstream=routes.torrent,
    min="2160p", source="webrip+")
output("transmission", upstream=filt_t,
    host=trans_host, port=9091,
    username=env("TRANS_USER", default=""),
    password=env("TRANS_PASS", default=""))

# ── Magnet branch ──────────────────────────────────────────────────────────
#
# Only entries guaranteed to have magnet_url reach here.
# torrent_url is masked — it is stripped from entries and the validator
# would catch any attempt to use it downstream.

filt_m = process("quality", upstream=routes.magnet,
    min="2160p", source="webrip+")
output("qbittorrent", upstream=filt_m,
    host=qbit_host, port=8080,
    username=env("QBIT_USER", default=""),
    password=env("QBIT_PASS", default=""))

pipeline("route-port-contracts", schedule="45m")
