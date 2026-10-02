# 3d-mvc-harvest.star
#
# Downloads Blu-ray 3D source material — MVC, including whole disc images —
# into a staging directory, for conversion elsewhere.
#
# ── Why this is separate from a 3D download pipeline ─────────────────────────
#
# MVC keeps the second eye as a dependent view of an AVC base view, and almost
# nothing plays it: not Plex, not libavcodec. So an MVC release is not something
# to put in a library — it is *source material* for mvc2sbs, which turns it into
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
# library. Sync that directory to wherever the GPU is and run mvc2sbs there:
#
#     mvc2sbs --input "/inbox/Life of Pi (2012)/disc.iso" \
#             --output "/media/3dmovies/Life of Pi (2012).mkv"
#
# mvc2sbs reads a disc image without mounting it and picks the right playlist
# itself, so an .iso needs no unpacking first — which is why this pipeline keeps
# them rather than rejecting them.
#
# Requirements: JACKETT_API_KEY, JACKETT_URL, TRAKT_CLIENT_ID/SECRET, a client.

inbox = "/data/media/3d-mvc-inbox"

JACKETT_URL = env("JACKETT_URL", default="http://localhost:9117")
JACKETT_KEY = env("JACKETT_API_KEY", default="YOUR_JACKETT_KEY")

# ── What to look for ─────────────────────────────────────────────────────────

# The watchlist supplies the titles; discover searches for each one.
want = input("trakt_list", type="movies", list="watchlist",
             client_id=env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID"),
             client_secret=env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET"))

found = process("discover", upstream=want, interval="24h", match_titles=True,
                search=[{"name": "jackett", "url": JACKETT_URL, "api_key": JACKETT_KEY,
                         "categories": ["2000"], "indexers": ["3dtorrents"],
                         "limit": 10, "timeout": "20s"}])

# ── Cheap, name-only gates first ─────────────────────────────────────────────
#
# Everything down to `once` decides from the release name alone and makes no
# network call. Running these before the .torrent fetch and the tracker scrape
# means a reject costs nothing, and this feed rejects far more than it accepts.

meta = process("metainfo_file", upstream=found)
req  = process("require", upstream=meta, fields=["title", "video_year", "_quality"])

# Exactly MVC. This is the whole point of the pipeline.
mvc  = process("quality", upstream=req, spec="bd3d", on_missing="reject")

drop = process("trailer", upstream=mvc)
film = process("movies", upstream=drop, reject_unmatched=True)
once = process("seen", upstream=film, local=True, retry_failed=True)

# ── Then the ones that cost a request ────────────────────────────────────────

torrent = process("metainfo_torrent", upstream=once, fetch_timeout="1m")
files   = process("require", upstream=torrent, fields=["torrent_files"])

# Archives and installers are rejected; **disc images are not**. An .iso is the
# most common shape for an MVC release, and mvc2sbs reads one directly — so
# rejecting it, as a library pipeline must, would throw away most of this feed.
ok = process("content", upstream=files, reject=["*.rar", "*.exe"])

alive = process("torrent_alive", upstream=ok, min_seeds=2, verify=True)

# One copy per film: dedup goes after everything that can refuse a release, so
# the alternatives are still there when one is refused.
pick = process("dedup", upstream=alive)

# ── Download, and stop ───────────────────────────────────────────────────────

# A directory per film keeps a disc image and its stray files together, and
# gives the sync something stable to watch.
path = process("pathfmt", upstream=pick, field="inbox_path",
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
<p>Sync the inbox, then run mvc2sbs on each of these.</p>
<ul>
{{range .Entries}}<li>{{.Title}}<br><small>{{index .Fields "inbox_path"}}</small></li>
{{end}}</ul>""")

# Overnight: an MVC remux is tens of gigabytes, and there is no hurry.
pipeline("3d-mvc-harvest", schedule="0 3 * * *")
