# bd3d2sbs

Converts a frame-packed Blu-ray 3D source (MVC) into a side-by-side MKV that an
ordinary decoder can play.

MVC stores the second eye as a *dependent view* of an AVC base view. Very few
players decode it — **Plex does not** — so a 3D Blu-ray sits in a library
unwatchable, despite carrying a full 1080p image per eye. Side-by-side puts both
eyes into a single frame that any H.264/HEVC decoder handles, at the cost of a
re-encode.

This is a Go rewrite of [bd3d2sbs](https://github.com/Michal-Szczepaniak/bd3d2sbs)
(itself a Linux port of the Windows tool BD3D2MK3D), with the platform
differences made explicit so the same binary reasons correctly on Linux, macOS
and Windows.

## Status

| Part | State |
|---|---|
| Toolchain detection and reporting (`--check`) | done |
| Conversion planning and `--dry-run` | done |
| Running the conversion | **not implemented** |

The runner is missing one piece: tsMuxeR needs a *meta file* naming the exact
playlist and track numbers to demux, and those come from parsing tsMuxeR's own
listing of the source. That parsing cannot be written honestly without the tool
and a real MVC source to check it against — so it is left undone rather than
guessed at. Everything up to it is tested.

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
| `--temp` | beside the output | Scratch space for demuxed streams |
| `--layout` | `full` | `full` (1080p per eye) or `half` (960p per eye, roughly half the size) |
| `--encoder` | `auto` | `auto`, `x264`, `vaapi`, `videotoolbox`, `nvenc` |
| `--crf` | `18` | Quality target, 0–51; lower is better |
| `--preset` | `slow` | x264 speed/efficiency trade-off |

`--layout full` is almost always what a 3D library wants: it is the only layout
that keeps the disc's resolution. `half` halves the horizontal detail per eye.

## The pipeline

```
tsMuxeR      demux the base (AVC) and dependent (MVC) views, audio, subs, chapters
VapourSynth  decode both views and stack them into one frame  ─┐
encoder      x264, or ffmpeg with a platform hardware encoder  ─┘ piped, not spooled
mkvmerge     mux the result back together
```

The decode streams Y4M straight into the encoder. Spooling raw frames for a
feature film would be hundreds of gigabytes.

## Tools, and why each is needed

Installing them is left to you; `--check` says what is missing, what each one
does, and where to start:

```
platform: linux   encoder: x264

  ok       ffprobe    /usr/bin/ffprobe
           ffprobe version 8.0.1
  MISSING  tsmuxer    demux the base and dependent MVC views, audio, subtitles and chapters
           try: build the fork bd3d2sbs vendors — distribution tsMuxeR packages usually lack MVC demuxing
  ...
4 of 5 tools missing
```

`--check` exits non-zero when anything is missing, so it works as a preflight in
a script.

**The awkward one is tsMuxeR.** Most packaged builds cannot demux MVC; bd3d2sbs
vendors a fork for exactly that reason. VapourSynth additionally needs the
`mvc-source` plugin and the `edge264-mvc` decoder, neither of which is packaged
anywhere.

## Encoders per platform

| Platform | Hardware | Software |
|---|---|---|
| Linux | NVENC, VAAPI | x264 |
| macOS | VideoToolbox | x264 |
| Windows | NVENC | x264 |

`auto` picks the best one whose toolchain is actually installed, falling back to
x264. Asking for an encoder the platform cannot have (VAAPI on macOS, say) is
refused up front rather than hours into a run.

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
and is retried on the next run.
