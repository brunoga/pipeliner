# 3d-mvc-harvest.star
#
# Downloads Blu-ray 3D source material — MVC, including whole disc images —
# into a staging directory, for conversion elsewhere.
#
# ── Why this is separate from a 3D download pipeline ─────────────────────────
#
# MVC keeps the second eye as a dependent view of an AVC base view, and almost
# nothing plays it: not Plex, not libavcodec. So an MVC release is not something
# to put in a library — it is *source material* for mvctools, which turns it into
# the side-by-side form an ordinary player handles.
#
# That makes this the mirror image of a pipeline that feeds a library directly.
# A library pipeline wants `spec="3dfull"`, which takes full side-by-side and
# rejects MVC. This wants exactly the opposite:
#
#     spec="bd3d"      exactly MVC — rejects side-by-side, half-SBS and 2D
#
# A bare quality token is an exact match, so this takes the one tier a library
# pipeline cannot use and nothing else.
#
# ── Where things land ────────────────────────────────────────────────────────
#
# Everything goes to one staging directory and stops there. Nothing is moved
# into the library, because an MVC file in a library is an unplayable file in a
# library. Sync that directory to wherever the GPU is and run mvctools there:
#
#     mvctools --input "/inbox/Life of Pi (2012)/disc.iso" \
#             --output "/media/3dmovies/Life of Pi (2012).mkv"
#
# mvctools reads a disc image without mounting it and picks the right playlist
# itself, so an .iso needs no unpacking first — which is why this pipeline keeps
# them rather than rejecting them.
#
# Requirements: JACKETT_API_KEY, JACKETT_URL, TRAKT_CLIENT_ID/SECRET, a client.

inbox = "/data/media/3d-mvc-inbox"

JACKETT_URL = env("JACKETT_URL", default="http://localhost:9117")
JACKETT_KEY = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")

# ── What to look for ─────────────────────────────────────────────────────────

# Two ways in, merged, because they answer different questions.
#
# The watchlist branch *hunts*: discover searches the indexer for each title, so
# it can reach a release that scrolled out of the feed long ago. It only ever
# finds what is on the list.
#
# The feed branches *sweep*: a jackett source with a query returns the newest
# matching releases whatever their title, so anything new shows up without being
# asked for. Torznab has no OR operator and the terms of a query are ANDed, so
# one query cannot express "MVC or BD50 or BD25" — hence one input per token.
# Each is capped at 100 results by the indexer regardless of `limit`.
#
# `MVC` is the broad net: a native disc rip is often named "3D Bluray MVC" with
# no BDxx token. It is also the noisiest, because most things tagged MVC are
# 2D-to-3D conversions — but those parse as 3D-Conv and the `spec="bd3d"` gate
# below drops them, so the cost is feed slots rather than downloads.
want = input("trakt_list", type="movies", list="watchlist",
             client_id=env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID"),
             client_secret=env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET"))

found = process("discover", upstream=want, interval="24h", match_titles=True,
                search=[{"name": "jackett", "url": JACKETT_URL, "api_key": JACKETT_KEY,
                         "categories": ["2000"], "indexers": ["3dtorrents"],
                         "limit": 10, "timeout": "20s"}])

def mvc_feed_fn(query):
    return input("jackett", url=JACKETT_URL, api_key=JACKETT_KEY,
                 categories=["2000"], indexers=["3dtorrents"],
                 query=query, timeout="30s")

feed_mvc   = mvc_feed_fn(query="MVC")
feed_bd25  = mvc_feed_fn(query="BD25")
feed_bd50  = mvc_feed_fn(query="BD50")
feed_bd66  = mvc_feed_fn(query="BD66")
feed_bd100 = mvc_feed_fn(query="BD100")

# merge dedupes by URL, so a release matching two queries is carried once.
all_found = merge(found, feed_mvc, feed_bd25, feed_bd50, feed_bd66, feed_bd100)

# ── Cheap, name-only gates first ─────────────────────────────────────────────
#
# Everything down to `once` decides from the release name alone and makes no
# network call. Running these before the .torrent fetch and the tracker scrape
# means a reject costs nothing, and this feed rejects far more than it accepts.

meta = process("metainfo_file", upstream=all_found)
req  = process("require", upstream=meta, fields=["title", "video_year", "_quality"])

# MVC is Annex H of H.264/AVC, so a Blu-ray 3D MVC disc is always AVC. An
# H.265 tag therefore proves a release cannot be source material: re-encoding
# to HEVC is what destroys the MVC structure in the first place. This is the
# cheapest possible place to say so -- metainfo_file has already read the name
# and nothing has touched the network yet -- and it is worth saying because
# these releases were reaching the probe, which is the most expensive thing
# the pipeline does and fills the background worker's queue. An untagged
# release leaves `codec` absent, and an absent field compares false, so only
# an explicit H.265 claim is refused.
avc = process("condition", upstream=req,
              reject='codec == "H265"',
              reason="H.265: MVC is an H.264/AVC extension, so an HEVC " +
                     "re-encode cannot be the disc it was made from")

# Anything 3D at all, by rank: Half, Unspecified, Full and BD3D all pass, while
# a 2D-to-3D conversion and a plain 2D release do not. This is deliberately
# looser than `spec="bd3d"`, because the route below needs to see the releases
# whose layout is merely *unstated* — and it keeps that route from warning
# about hundreds of entries it was never going to take.
threed = process("quality", upstream=avc, spec="3d-half+", on_missing="reject")

drop = process("trailer", upstream=threed)

# local=True keeps this pipeline's history in its own bucket (movies:<task>).
#
# It matters because the tracker key is (title, year, 3D) and BD3D outranks
# every frame-compatible format. Sharing the tracker would mean that harvesting
# a film's disc makes a later full-SBS release read as a downgrade, so the
# pipeline that wanted a *playable* copy never gets one — for every title this
# one touches. With its own bucket it still never grabs the same film twice,
# and it tells the library pipelines nothing.
#
# The library pipelines should then gate on disk truth rather than on this
# tracker — `library` with sections=["3D Movies"] also sees the converted
# output once it lands.
film = process("movies", upstream=drop, local=True, reject_unmatched=True)
# retry_failed blocks the exact release a previous grab died on, so a dead
# torrent is not re-fetched night after night. It needs something durable to
# match on, and for this indexer that is the point: 3dtorrents reports no
# infohash attribute at all, and Jackett re-encrypts its download links every
# search — so for a long time there was nothing stable left and the same dead
# torrent came back under a fresh URL on every run.
#
# seen now also indexes source_id, which for Jackett is the tracker permalink
# and does not move. No config key is needed for that; the fingerprint below
# stays on the default "url" deliberately, because changing it would re-key
# this pipeline's existing history and un-see everything it has downloaded.
once = process("seen", upstream=film, local=True, retry_failed=True)

# ── Split on how much the name actually told us ──────────────────────────────
#
# A release name can be wrong in two directions, and they need opposite
# treatment. The direction people notice is a name claiming more than the
# release is: "COMPLETE BLURAY FULL-SBS" is a side-by-side re-encode, not the
# disc it names. The direction nobody notices is a name claiming *less* —
# because the release is refused and never looked at again.
#
#     Pacific Rim Uprising 2018 3D BluRay 1080p AVC Atmos TrueHD7 1 MTeam
#
# states no layout at all. Under a single `spec="bd3d"` gate it was refused;
# probing it showed a 49 GB MVC disc with 14 seeders, and it was the only live
# release of the ten the indexer had. The two the gate *did* admit were
# re-encodes with no seeders.
#
# The lanes run the same gates and differ in two small ways, and it is worth
# being straight about which of them earns the split.
#
# What does NOT differ is what counts as proof. Both lanes download only a
# release a probe has read as MVC, so a name claiming MVC buys a release a
# place in the queue and nothing more. (An earlier version let the asserted
# lane trust its name when the swarm would not serve; that was a compromise
# forced by probing inside the run, and background probing removed the need.)
#
# What actually needs separate lanes is the budget and the ordering. One limit
# node means one budget and one sort key, so merged into a single lane sorted
# by rating the ~107 asserted candidates would crowd out the ~56 ambiguous
# ones every night and the speculative ones would never be picked at all. The
# split is what guarantees the rescue lane its own protected slot, ordered by
# the thing that matters there (size, as triage) rather than by rating.
lanes = route(once,
    asserted  = 'video_3d_layout == "mvc"',
    ambiguous = 'video_3d_layout == "unspecified"',
    frame     = 'video_3d_layout == "half" or video_3d_layout == "full"')

# Frame-compatible: an ordinary decoder plays it, which is precisely why it is
# not source material. Rejected explicitly rather than left to fall off the
# ports, so the reason is recorded once here instead of as a WARN per entry.
process("condition", upstream=lanes.frame, rules=[
    {"reject": "true",
     "reason": "frame-compatible (half or full side-by-side): an ordinary " +
               "decoder plays it, which is exactly why it is not MVC source " +
               "material"},
])

# Two rules pull in opposite directions here, and the resolution is worth
# reading before changing any of the node order below.
#
# A node that can refuse a release belongs ABOVE `dedup`, which keeps one
# release per item and discards the rest. Refuse below it and the alternatives
# are already gone: the item is lost for the run, and since the same wave comes
# back and collapses to the same winner, for every run after it. That is not
# hypothetical — when the probe sat below `limit`, a release whose swarm would
# not serve won the collapse every night, was refused every night, and the film
# never arrived.
#
# But a probe is slow. A live disc takes two to three minutes to serve two
# pieces, and a seeder that can only dial in finds us at its next announce,
# often 30-60 minutes away. Above `dedup` there are dozens of releases a night,
# and a run cannot wait on all of those swarms.
#
# `background=True` resolves it. The node replays every verdict an earlier
# probe recorded in cache_probe and touches no network itself; each release
# nobody has read yet is queued for the daemon's background worker and passes
# through with no probe_* fields. The worker probes the queue one release at a
# time, each film's best release first, and retries an unserved one 3h and
# then 6h later before recording the failure. So the refusal happens above
# `dedup`, for free, and a release read today is decided by tomorrow's run.
#
# The worker runs only under `pipeliner daemon`. A `pipeliner run` queues and
# leaves the queue for the daemon to work through.

# ── The gates that cost a request, shared by both lanes ──────────────────────

def mvc_network_gates_fn(upstream):
    torrent = process("metainfo_torrent", upstream=upstream, fetch_timeout="1m")
    # torrent_info_hash is required because probe needs it downstream.
    files = process("require", upstream=torrent,
                    fields=["torrent_files", "torrent_info_hash"])
    # Archives and installers are rejected; **disc images are not**. An .iso is
    # the most common shape for an MVC release, and mvctools reads one
    # directly — so rejecting it, as a library pipeline must, would throw away
    # most of this feed.
    ok = process("content", upstream=files, reject=["*.rar", "*.exe"])
    # Stops here rather than at dedup: each lane applies its probe gate before
    # collapsing.
    alive = process("torrent_alive", upstream=ok, min_seeds=2, verify=True)
    # Replays what the worker has read and queues the rest; see above.
    return process("probe", upstream=alive, background=True,
                   no_peer_timeout="3m", timeout="10m")

# Only what a probe has read passes, and only if it read MVC. A release still
# in the queue carries no probe_ok, which != true rejects too: it waits for a
# later run rather than being downloaded on the strength of its name. The rule
# is the same in both lanes — the lanes differ in what is worth queueing, not
# in what counts as proof.
def mvc_probe_gate_fn(upstream):
    return process("condition", upstream=upstream, rules=[
        {"reject": "probe_ok != true",
         "reason": "no probe has read this release's container yet: it is " +
                   "queued for the background worker, or nobody served the " +
                   "sample"},
        {"reject": 'probe_3d_layout != "mvc"',
         "reason": "the probe read this release's container and it is not MVC"},
    ])

# ── Lane A: the name claimed MVC ─────────────────────────────────────────────
#
# A first run finds dozens and each is tens of gigabytes, so take a few a night
# rather than two terabytes at once. Highest-rated first, so the backlog drains
# in a useful order.
a_ok   = mvc_probe_gate_fn(mvc_network_gates_fn(lanes.asserted))
# One copy per film within the lane, after everything that can refuse a
# release, so the alternatives are still there when one is refused.
a_pick = process("dedup", upstream=a_ok)
a_few  = process("limit", upstream=a_pick, n=3, order="desc", sort="video_rating")

# ── Lane B: the name said nothing ────────────────────────────────────────────
#
# Size is used here to decide what is worth MEASURING — never to decide what a
# release is. Sampled across this indexer's 3D feed:
#
#     name states   n   min   median   max
#     MVC/BD3D      8  24.8     42.4  49.8 GB
#     unspecified   9   2.6      8.0  45.7 GB
#     full-SBS     59   1.6      8.5  45.3 GB
#     half-SBS     24   1.4      6.4  36.8 GB
#
# which is why size cannot assert: full side-by-side reaches 45.3 GB and sits
# inside the MVC range almost entirely, so a 40 GB file is not necessarily a
# disc. Asserting from size would be the same mistake as reading layout off a
# name, with a different proxy — and the disc is ten megabytes and a few
# minutes from simply saying what it is.
#
# What size does tell you is what a release is NOT. Nothing under ~20 GB is a
# 1080p MVC disc; the smallest observed is a BD25 at 24.8. The median
# unspecified release is 8 GB, so without this floor the worker's queue fills
# with things that could not possibly be what we want. The floor sits above
# the network gates, so it saves the .torrent fetch and the tracker scrape too.
b_big  = process("condition", upstream=lanes.ambiguous,
                 reject="torrent_file_size < 20000000000")

# Nothing claimed MVC here, so the probe gate is the only thing that can vouch
# for a release — and it is the same gate as lane A's. One per night, biggest
# first: this lane is speculative and must not eat the night's budget.
b_ok   = mvc_probe_gate_fn(mvc_network_gates_fn(b_big))
b_pick = process("dedup", upstream=b_ok)
b_few  = process("limit", upstream=b_pick, n=1, order="desc", sort="torrent_file_size")

# ── Join ─────────────────────────────────────────────────────────────────────
#
# A film can appear in both lanes — Pacific Rim Uprising had two MVC-named
# releases and one bare-3D one. A final dedup keeps a single copy, and prefers
# the asserted lane's BD3D over the ambiguous lane's unspecified layout, which
# ranks with Half.
chosen = process("dedup", upstream=merge(a_few, b_few))

# A directory per film keeps a disc image and its stray files together, and
# gives the sync something stable to watch.
path = process("pathfmt", upstream=chosen, field="inbox_path",
               path=inbox + "/{title} ({video_year})")

out = output("deluge", upstream=path, host="localhost", port=58846,
             password=env("DELUGE_PASSWORD", default="YOUR_DELUGE_PASSWORD"),
             move_completed_path="{inbox_path}", tls=False)

# Say what arrived, so the conversion run has a list to work from.
output("notify", upstream=out, via="email",
       config={
           "smtp_host": env("SMTP_HOST", default="smtp.example.com"),
           "smtp_port": 587,
           "username":  env("SMTP_USER", default="you@example.com"),
           "password":  env("SMTP_PASSWORD", default="YOUR_SMTP_PASSWORD"),
           "sender":    env("SMTP_USER", default="you@example.com"),
           "to":        env("NOTIFY_TO", default="you@example.com"),
           "html":      True,
       },
       title="3D MVC source: {{len .Entries}} arrived",
       body="""<h2>Ready to convert</h2>
<p>Sync the inbox, then run mvctools on each of these.</p>
<ul>
{{range .Entries}}<li>{{.Title}}<br><small>{{index .Fields "inbox_path"}}</small></li>
{{end}}</ul>""")

# Overnight: an MVC remux is tens of gigabytes, and there is no hurry.
pipeline("3d-mvc-harvest", schedule="0 3 * * *")
