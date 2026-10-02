# bd3d2sbs

Converts a frame-packed Blu-ray 3D source (MVC) into a side-by-side MKV that an
ordinary decoder can play.

MVC stores the second eye as a *dependent view* of an AVC base view. Very few
players decode it — **Plex does not**, and neither does libavcodec, which drops
the dependent view outright — so a 3D Blu-ray sits in a library unwatchable
despite carrying a full 1080p image per eye. Side-by-side puts both eyes into a
single frame any H.264/HEVC decoder handles, at the cost of a re-encode.

Named after [bd3d2sbs](https://github.com/Michal-Szczepaniak/bd3d2sbs), whose
pipeline this started from, but it does not follow it: see
[Why this is not a port](#why-this-is-not-a-port).

## Status

| Part | State |
|---|---|
| Toolchain detection and reporting (`--check`) | done |
| MVC stream interleaving | done, bit-exact against a real combined stream |
| Conversion planning and `--dry-run` | done |
| Running the conversion | **not implemented** |

The runner is missing one piece: tsMuxeR needs a *meta file* naming the exact
playlist and track numbers to demux, and those come from parsing tsMuxeR's own
listing of the source. That parsing cannot be written honestly without a real 3D
Blu-ray to check it against. Everything after it is tested against real MVC
streams.

## The pipeline

```
tsMuxeR    demux the base (AVC) and dependent (MVC) views, audio, subs, chapters
  ↓        the two views are interleaved into one MVC stream, in process
edge264    decode both eyes and stack them side by side, as Y4M  ─┐
encoder    x264, or ffmpeg with a platform hardware encoder       ─┘ piped
mkvmerge   mux the result back together
```

Nothing is spooled between stages: the interleaved stream goes to the decoder's
stdin and the decoded frames go straight into the encoder. Raw frames for a
feature film are hundreds of gigabytes, and a third copy of the demuxed pair
would be tens.

### Why the interleaving is needed

tsMuxeR's documentation is explicit that it **always splits** a combined AVC/MVC
track into a base `.264` and a dependent `.mvc`. A decoder wants them as one
stream, with each picture's base NALs followed by its dependent NALs. Joining
them is the one piece neither tool provides, and it is the only reason bd3d2sbs
needs a frameserver at all.

Doing it here is about 150 lines: split both streams into NAL units, group them
into access units, and emit base-then-dependent per unit. Verified by decoding
an interleaved pair and a real combined stream and comparing the output
byte-for-byte.

## Usage

```sh
bd3d2sbs --check
bd3d2sbs --dry-run --input disc.iso --output "Life of Pi (2012).mkv"
```

| Flag | Default | Description |
|---|---|---|
| `--check` | — | Report which external tools are present and which are missing, then exit |
| `--dry-run` | — | Print the commands that would run |
| `--input` | — | A `.iso`, a BDMV directory, or an MKV from MakeMKV |
| `--output` | — | Destination `.mkv` |
| `--temp` | beside the output | Scratch space for the demuxed views |
| `--layout` | `full` | `full` (1080p per eye) or `half` (960p per eye, roughly half the size) |
| `--encoder` | `auto` | `auto`, `x264`, `vaapi`, `videotoolbox`, `nvenc` |
| `--swap-lr` | — | Exchange the eyes, for a disc whose base view is the right one |
| `--crf` | `18` | Quality target, 0–51; lower is better |
| `--preset` | `slow` | x264 speed/efficiency trade-off |

`--layout full` is almost always what a 3D library wants: it is the only layout
that keeps the disc's resolution, and it is what the decoder emits natively, so
it costs no resample.

`--swap-lr` and `--layout half` are filters on the stacked frame, so they need an
ffmpeg encoder; asking for either with `x264` is refused up front rather than
producing a file quietly missing what was asked for.

## Tools, and why each is needed

Installing them is left to you; `--check` says what is missing, what each one
does, and where to start:

```
platform: linux   encoder: x264

  ok       ffprobe    /usr/bin/ffprobe
  ok       tsmuxer    /usr/local/bin/tsMuxeR
  ok       edge264    /usr/local/bin/edge264
  ok       x264       /usr/bin/x264
  ok       mkvmerge   /usr/bin/mkvmerge

all 5 required tools present
```

`--check` exits non-zero when anything is missing, so it works as a preflight.

- **tsMuxeR** — upstream's own release binaries demux MVC, for Linux, macOS and
  Windows. No build and no fork needed.
- **edge264** — the only open-source *software* MVC decoder. One small C program
  with no dependencies, whose Makefile targets macOS, Linux and Windows.
- **x264 / ffmpeg / mkvmerge** — packaged everywhere.

## Encoders per platform

| Platform | Hardware | Software |
|---|---|---|
| Linux | NVENC, VAAPI | x264 |
| macOS | VideoToolbox | x264 |
| Windows | NVENC | x264 |

`auto` runs a **one-frame trial encode** for each candidate and takes the first
that succeeds, falling back to x264. Merely finding ffmpeg is not evidence a GPU
is present — a stock build advertises `h264_nvenc` on a machine with no NVIDIA
card — and discovering that at the encode step would waste the hours already
spent decoding.

## Docker

`Dockerfile.bd3d2sbs` builds an amd64 image with the whole toolchain (~620 MB):

```sh
docker build -f Dockerfile.bd3d2sbs -t bd3d2sbs .
docker run --rm -v /media:/media bd3d2sbs --check
```

It is deliberately **not** part of the pipeliner server image. tsMuxeR ships only
an x86_64 Linux build while that image is built for amd64, arm64 and arm/v7; and
a conversion is hours of work needing device passthrough (`/dev/dri`, or the
NVIDIA runtime), which belongs in a container started for the job rather than in
the scheduler.

## Driving it from pipeliner

Pair it with the [`exec`](../../plugins/sink/exec/README.md) sink, whose `args`
list passes each value as one argument, so paths with spaces need no quoting:

```python
src  = input("filesystem", path="/media/3d-staging", recursive=True, mask="*.iso")
meta = process("metainfo_file", upstream=src)
conv = output("exec", upstream=meta,
              command="/usr/local/bin/bd3d2sbs",
              args=["--input", "{file_location}",
                    "--output", "/media/3dmovies/{title} ({video_year}).mkv"])
pipeline("convert-3d", schedule="0 4 * * *")
```

A non-zero exit fails the entry, so a failed conversion is not recorded as done
and is retried next run.

## Why this is not a port

bd3d2sbs builds VapourSynth from source (pinned to R65), builds a fork of
tsMuxeR, and builds a VapourSynth source plugin and the decoder it wraps. Each
of those turned out to be avoidable:

| bd3d2sbs | here | why |
|---|---|---|
| VapourSynth R65, built from source | not used | It was only there to host the source plugin. The decoder emits stacked Y4M itself (`-O`), so the frameserver, Python and the plugin all drop out. R65 was pinned because bd3d2sbs's script uses autotools, which newer tags dropped — a build-system artifact, not a requirement; its own script prefers a system install, and the plugin's real requirement is API4, i.e. R63+. |
| mvc-source plugin, built from source | not used | Its job was interleaving the two views and stacking them. The interleaving is ~150 lines here; the stacking is a decoder flag. |
| tsMuxeR, teaching-droid fork, built from source | upstream release binary | The fork's commits are GUI, translation and changelog work — none touch MVC — and upstream is an ancestor of it. Upstream's prebuilt binaries demux MVC and exist for all three platforms. |
| edge264, pinned `v2026.07.22+5` | `v2026.09.22` | Two months of fixes newer. |
| mvc-source, pinned `v0.8.0` | n/a | Upstream was already at `v0.11.0`. |
| `fzf` for interactive track picking | not used | A scheduled conversion cannot prompt. |

The result is four external tools instead of four builds, and a genuinely
cross-platform story: every remaining dependency has an official binary or a
Makefile that targets macOS, Linux and Windows.
