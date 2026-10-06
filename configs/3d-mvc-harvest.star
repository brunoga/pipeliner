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

# Exactly MVC. This is the whole point of the pipeline.
mvc  = process("quality", upstream=req, spec="bd3d", on_missing="reject")

drop = process("trailer", upstream=mvc)

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
once = process("seen", upstream=film, local=True, retry_failed=True)

# ── Then the ones that cost a request ────────────────────────────────────────

torrent = process("metainfo_torrent", upstream=once, fetch_timeout="1m")
files   = process("require", upstream=torrent, fields=["torrent_files"])

# Archives and installers are rejected; **disc images are not**. An .iso is the
# most common shape for an MVC release, and mvctools reads one directly — so
# rejecting it, as a library pipeline must, would throw away most of this feed.
ok = process("content", upstream=files, reject=["*.rar", "*.exe"])

alive = process("torrent_alive", upstream=ok, min_seeds=2, verify=True)

# One copy per film: dedup goes after everything that can refuse a release, so
# the alternatives are still there when one is refused.
pick = process("dedup", upstream=alive)

# ── Download, and stop ───────────────────────────────────────────────────────

# A first run finds dozens of these and each is tens of gigabytes, so take a
# few a night rather than two terabytes at once. Highest-rated first, so the
# backlog drains in a useful order. A real run of this against a 989-title
# watchlist accepted 65 releases, which would have been about 2 TB.
few = process("limit", upstream=pick, n=3, order="desc", sort="video_rating")

# A directory per film keeps a disc image and its stray files together, and
# gives the sync something stable to watch.
path = process("pathfmt", upstream=few, field="inbox_path",
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
