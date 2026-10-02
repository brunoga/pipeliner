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
# So the shape is: watch a staging directory, convert what lands there into the
# library, and remember what has been done so nothing is converted twice.
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
src = input("filesystem", path=staging, recursive=True, mask="*.m2ts")

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
# to x264.
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
pipeline("convert-3d", schedule="0 3 * * *")
