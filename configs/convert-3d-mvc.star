# convert-3d-mvc.star
#
# Converts Blu-ray 3D rips (MVC) into side-by-side files a normal player can
# decode, by handing each one to the mvc2sbs binary.
#
# ── Why this pipeline exists ─────────────────────────────────────────────────
#
# MVC stores the second eye as a dependent view of an AVC base view. Very few
# players decode it — Plex does not, and neither does libavcodec — so a 3D
# Blu-ray rip sits in a library unwatchable despite carrying a full image per
# eye. Side-by-side puts both eyes in one frame any H.264 decoder handles.
#
# So the shape is: scan a staging directory every few minutes, convert what has
# landed there into the library, and remember what has been done so nothing is
# converted twice.
#
# ── What this needs ──────────────────────────────────────────────────────────
#
#   * mvc2sbs on PATH, with its own four tools. Run `mvc2sbs --check` first: it
#     reports what is missing and what each one is for. Installing them is a
#     one-off; a conversion that starts without them wastes the demux.
#   * Somewhere to put the rips. This watches /media/3d-staging and writes into
#     /media/3dmovies.
#
# ── The ordering that matters ────────────────────────────────────────────────
#
# `seen` goes LAST, immediately before the sink, and `local=True` keeps its
# record to this pipeline. The commit phase only records entries a sink
# accepted, so a conversion that fails leaves the file untracked and it is
# retried on the next run — which is what you want for a job that can fail for
# a transient reason like a full disk.
#
# `exec` fails the entry on a non-zero exit, which is what makes that work.
#
# ── Delivering the rips ──────────────────────────────────────────────────────
#
# stable_for handles a rip that is still arriving, at the cost of one scan
# interval before anything is emitted. If you would rather not wait at all,
# land the rip somewhere else and rename it in: a rename within one filesystem
# is atomic, so this directory only ever holds complete deliveries and
# stable_for can be dropped entirely.
#
#     rsync -a remote:/rips/ /media/3d-incoming/ && mv /media/3d-incoming/* /media/3d-staging/
#
# Both work. The rename is faster and needs no settling window; stable_for
# needs no cooperation from whatever puts the files there, which is why it is
# what this sample uses.

staging = "/media/3d-staging"
library = "/media/3dmovies"

SMTP = {
    "smtp_host": env("SMTP_HOST", default="smtp.example.com"),
    "smtp_port": 587,
    "username":  env("SMTP_USER", default="you@example.com"),
    "password":  env("SMTP_PASSWORD", default="YOUR_SMTP_PASSWORD"),
    "sender":    env("SMTP_USER", default="you@example.com"),
    "to":        env("NOTIFY_TO", default="you@example.com"),
    "html":      True,
}

# ── Find the rips ────────────────────────────────────────────────────────────

# A 3D rip is an .m2ts (or an .iso / BDMV directory, which mvc2sbs also takes).
#
# stable_for withholds a rip until two scans have seen it unchanged two minutes
# apart, so one still being copied in is left for a later run instead of being
# handed to mvc2sbs half-written. The unit is the top-level item — the
# directory (or single file) a release arrives as — so a disc delivered as a
# tree settles as a whole rather than one file at a time.
#
# Note what it does *not* do: trust timestamps. `rsync -a` preserves the
# source's modification times, so a file that arrived seconds ago can look days
# old; comparing contents across the window does not care.
src = input("filesystem", path=staging, recursive=True, mask="*.m2ts",
            stable_for="2m")

# ── Other shapes a rip arrives in ────────────────────────────────────────────
#
# A disc image is one file, so it needs only a different mask:
#
#     iso = input("filesystem", path=staging, recursive=True, mask="*.iso",
#                 stable_for="2m")
#
# A node with several upstreams merges them, so an `iso` source can feed
# `metainfo_file` alongside `src` and the rest of the pipeline is unchanged.
#
# A full disc delivered as a BDMV *tree* needs more care. `mask="*.m2ts"` with
# recursive=True matches every stream file in BDMV/STREAM, which is dozens of
# entries for one film. Match the one file every disc has exactly one of
# instead, and hand mvc2sbs the directory holding it:
#
#     disc = input("filesystem", path=staging, recursive=True,
#                  mask="index.bdmv", stable_for="2m")
#     ...
#     args=["--input", "{{dirname .file_location}}", ...]
#
# dirname turns /staging/Movie/BDMV/index.bdmv into /staging/Movie/BDMV, which
# mvc2sbs accepts. index.bdmv is small and written early, so its own timestamp
# says nothing about whether the streams beside it have arrived — but settling
# is per item, so the disc is still withheld until the whole tree stops
# changing. That is what makes matching one file inside a tree safe.

# metainfo_file parses the filename into title, year and quality.
meta = process("metainfo_file", upstream=src)

# Without a title and year there is nowhere to put the result.
req = process("require", upstream=meta, fields=["title", "video_year"])

# Ignore the small files a rip leaves beside the feature — trailers, menus.
# A 3D feature is tens of gigabytes; nothing real is under 2 GB.
big = process("condition", upstream=req, reject="file_size < 2000000000")

# ── Where the converted file goes ────────────────────────────────────────────

# The output path, built once here so the exec arguments and the notification
# agree on it.
out = process("pathfmt", upstream=big, field="sbs_path",
              path=library + "/{title} ({video_year})/{title} ({video_year}) 3D FSBS.mkv")

# ── Convert ──────────────────────────────────────────────────────────────────

# seen records a file only after the conversion succeeded, so a failure is
# retried rather than silently skipped.
once = process("seen", upstream=out, local=True, fields=["file_location"])

# Each args element is one argument, so paths with spaces need no quoting.
#
# --layout full keeps 1080p per eye, which is the only reason to do this at all;
# half would halve the horizontal detail. --encoder auto runs a one-frame trial
# encode and takes the fastest that actually works on this machine, falling back
# to software.
#
# --codec h264 is the default and plays on anything. Add "--codec", "h265" for a
# materially smaller file at the same quality — a full-SBS frame is 3840x1080,
# which is where HEVC pays off most — but check your players direct-play it
# first: a client that has to transcode a frame that wide is worse off than one
# direct-playing H.264. Note that --crf does not mean the same thing to both
# codecs; x265 at 18 is higher quality and larger than x264 at 18, so raise it
# by two or three if what you want is the saving.
# Every audio and subtitle track the disc carries is passed through by default,
# each tagged with its language. That is often not what you want: lossless audio
# dominates a well-compressed conversion, and on a measured disc the single
# TrueHD Atmos track was more than twice the size of the video. Narrowing to the
# tracks you will actually play is the largest saving that costs no picture
# quality — around 30% of the file on a disc with half a dozen audio tracks.
#
# Add, for the English lossless mix and English subtitles only:
#
#     "--audio-lang", "eng", "--audio-codec", "truehd", "--subs-lang", "eng",
#
# Language and codec are both required when both are given, so that names one
# track rather than everything English plus everything TrueHD. A filter that
# matches nothing fails the entry before the conversion starts, which `seen`
# then leaves untracked for a retry. Run `mvc2sbs --list --input <disc>` to see
# what a disc offers first.
convert = output("exec", upstream=once,
                 command="/usr/local/bin/mvc2sbs",
                 args=["--input", "{file_location}",
                       "--output", "{sbs_path}",
                       "--layout", "full",
                       "--encoder", "auto",
                       "--quiet"])

# ── Alternative: run it out of its container ─────────────────────────────────
#
# mvc2sbs knows nothing about Docker — it runs its four tools from its own PATH,
# so inside the image it just works. To drive that image from here instead of
# installing the toolchain on the host, make `docker` the command and let the
# args carry the rest. There is no shell, so each argument is its own element
# and paths with spaces need no quoting:
#
#   convert = output("exec", upstream=once,
#                    command="docker",
#                    args=["run", "--rm",
#                          "--volume", staging + ":" + staging + ":ro",
#                          "--volume", library + ":" + library,
#                          # For GPU encoding, pass the device through too:
#                          #   "--device", "/dev/dri",            (VAAPI)
#                          #   "--gpus", "all",                   (NVENC)
#                          "ghcr.io/brunoga/mvc2sbs:latest",
#                          "--input", "{file_location}",
#                          "--output", "{sbs_path}",
#                          "--layout", "full",
#                          "--encoder", "auto",
#                          "--quiet"])
#
# Both forms behave the same to the pipeline: a non-zero exit fails the entry,
# so `seen` does not record it and the next run tries again.

# Tell someone it happened. Chained after the converter, so a failed conversion
# sends nothing: the sink failed the entry and a chained sink only sees what
# its upstream accepted.
output("notify", upstream=convert, via="email", config=SMTP,
       title="3D conversion: {{len .Entries}} file(s)",
       body="""<h2>Converted to side-by-side</h2>
<ul>
{{range .Entries}}<li>{{.Title}} — {{index .Fields "sbs_path"}}</li>
{{end}}</ul>""")

# Overnight, and not hourly: a feature film is hours of work even on a GPU, and
# the scheduler skips a run whose previous one is still going.
# Every five minutes, not nightly: a conversion is hours of CPU, so starting it
# minutes after a rip lands rather than seconds costs nothing, and a scan of a
# staging directory is cheap enough to run this often. The schedule is the
# whole watching mechanism — there is no filesystem-notification path to go
# wrong, and a rip that appears while the daemon is down is picked up by the
# next scan rather than missed.
pipeline("convert-3d", schedule="5m")
