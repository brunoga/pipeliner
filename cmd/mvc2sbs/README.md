# mvc2sbs

Converts a frame-packed Blu-ray 3D source (MVC) into a side-by-side MKV that an
ordinary decoder can play.

MVC stores the second eye as a *dependent view* of an AVC base view. Very few
players decode it — **Plex does not**, and neither does libavcodec, which drops
the dependent view outright — so a 3D Blu-ray sits in a library unwatchable
despite carrying a full 1080p image per eye. Side-by-side puts both eyes into a
single frame any H.264/HEVC decoder handles, at the cost of a re-encode.

## Status

Complete, and tested end to end against a real MVC source — see
[Testing without a disc](#testing-without-a-disc).

## The pipeline

```
tsMuxeR    demux the base (AVC) and dependent (MVC) views, audio, subs, chapters
  ↓        the two views are interleaved into one MVC stream, in process
edge264    decode both eyes and stack them side by side, as Y4M  ─┐
encoder    x264 or x265, or ffmpeg with a platform hardware encoder ─┘ piped
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
them is the one piece neither tool provides, and doing it here is what lets the
decoder be driven directly, with no frameserver in between.

It is about 150 lines: split both streams into NAL units, group them into access
units, and emit base-then-dependent per unit. Verified by decoding an interleaved
pair and a real combined stream and comparing the output byte for byte.

## Usage

```sh
mvc2sbs --check                       # preflight: what is installed, what is not
mvc2sbs --dry-run --input 00800.m2ts --output "Life of Pi (2012).mkv"
mvc2sbs --input 00800.m2ts --output "Life of Pi (2012).mkv"
```

### What you can point it at

| You have | Point at | Who picks the title |
|---|---|---|
| A `.iso` disc image | the `.iso` | **it does** |
| A ripped BDMV folder | the folder, or its `BDMV` | **it does** |
| A specific playlist | `BDMV/PLAYLIST/00800.mpls` | you |
| Loose streams | the feature's `.m2ts` | you |
| An MKV from MakeMKV | the `.mkv` | you |

**A disc image needs no mounting.** Mounting one requires root, which rules it
out for an unattended conversion, so the image is read directly — a Blu-ray is a
UDF 2.50 filesystem and a pure-Go reader handles it on every platform. Only the
files a conversion needs come out of it:

```
BDMV/PLAYLIST/*.mpls     which clips make up a title; tiny
BDMV/CLIPINF/*.clpi      stream metadata; tiny
BDMV/STREAM/SSIF/*.ssif  base and dependent views interleaved — the 3D stream
```

The base-view `BDMV/STREAM/*.m2ts` is **skipped**: on the disc it shares extents
with the matching SSIF, so copying both would write the base view twice, and
tsMuxeR reads a playlist perfectly well without it. `BACKUP`, `AUXDATA`,
`CERTIFICATE`, `JAR`, `BDJO` and `META` are skipped outright.

### Choosing the title

Given an image or a folder, every playlist is probed and **the longest 3D one
wins**. A disc holds a playlist per title — the feature, its trailers, the
menus, and often several near-duplicates of the feature — so picking by filename
or number gets a trailer as often as the film. Length is the signal that works:
a feature is tens of times longer than anything else on the disc.

```
mvc2sbs: reading the disc image (no mount needed)
mvc2sbs: extracted 142 of 1206 files from the image (31.4 GiB)
mvc2sbs: chose 00800.mpls (1h58m12s) from 37 playlists, 3 of them 3D
```

**The eye order comes from the disc too.** tsMuxeR reports whether the base view
is the left or the right eye, and the stacking compensates automatically — most
discs are left, some are not, and the difference is the difference between 3D
and a headache. `--swap-lr` overrides it when given explicitly.

It reports as it goes, because a feature film takes hours:

```
mvc2sbs: probing /media/3d-staging/disc.m2ts
mvc2sbs: source: base view track 4113, dependent view track 4114, 2 audio, 4 subtitle
mvc2sbs: demuxing both views and 6 other track(s)
mvc2sbs: decoding and encoding (nvenc)
mvc2sbs: muxing /media/3dmovies/Life of Pi (2012).mkv
mvc2sbs: done
```

A non-zero exit means the conversion did not happen, which is what lets
pipeliner retry it.

| Flag | Default | Description |
|---|---|---|
| `--check` | — | Report which external tools are present and which are missing, then exit |
| `--dry-run` | — | Print the commands that would run, without running them |
| `--keep-temp` | — | Leave the demuxed streams behind instead of deleting them |
| `--quiet` | — | Only report errors |
| `--input` | — | An `.m2ts`, a `.mpls` playlist from a BDMV, or an MKV — see [What you can point it at](#what-you-can-point-it-at) |
| `--output` | — | Destination `.mkv` |
| `--temp` | beside the output | Scratch space for the demuxed views |
| `--layout` | `full` | `full` (1080p per eye) or `half` (960p per eye, roughly half the size) |
| `--encoder` | `auto` | `auto`, `software`, `vaapi`, `videotoolbox`, `nvenc` (`x264` is still accepted for `software`) |
| `--codec` | `h264` | `h264` or `h265` — see [Codec](#codec) |
| `--swap-lr` | — | Exchange the eyes, for a disc whose base view is the right one |
| `--list` | — | Print the source's tracks and exit — see [Choosing tracks](#choosing-tracks) |
| `--list` | — | Print the source's tracks and exit — see [Choosing tracks](#choosing-tracks) |
| `--audio-lang` | — | Keep only audio in these languages, e.g. `eng` or `eng,fra` |
| `--audio-codec` | — | Keep only audio matching these codecs, e.g. `truehd` or `dts,ac3` |
| `--audio-best` | — | Of the audio that matches, keep only the highest-quality track |
| `--subs-lang` | — | Keep only subtitles in these languages, e.g. `eng,pt-br` |
| `--subs-codec` | — | Keep only subtitles matching these codecs |
| `--keep-fallback` | — | Keep the lossy core embedded in a lossless track instead of dropping it |
| `--name-audio-codec` | — | Append the kept audio codec to the output filename |
| `--remux` | — | Copy the disc's MVC video out with no re-encoding — see [Remuxing](#remuxing-instead-of-converting) |
| `--crf` | `18` | Quality target, 0–51; lower is better. **Not comparable between codecs** |
| `--preset` | `slow` | Software encoder speed/efficiency trade-off (x264 and x265 take the same names) |

`--layout full` is almost always what a 3D library wants: it is the only layout
that keeps the disc's resolution, and it is what the decoder emits natively, so
it costs no resample.

`--swap-lr` and `--layout half` are filters on the stacked frame, so they need an
ffmpeg encoder; asking for either with a software encoder is refused up front
rather than producing a file quietly missing what was asked for.

## Codec

`--codec h264` (the default) plays on anything, including hardware too old to
decode HEVC at all. `--codec h265` is materially smaller at the same quality: a
full-SBS frame is double width — 3840x1080 from a 1080p disc — which is exactly
the case HEVC's larger coding units were designed for.

The trade-off is decoder support. HEVC is widely but not universally
direct-played, and a client that has to *transcode* a 3840x1080 stream is worse
off than one direct-playing H.264. If the library is served to a mix of clients,
H.264 is the safer default; if you know what plays it, HEVC saves real space.

`--crf` means something different to each codec: x265 at a given CRF is roughly a
step *higher* quality — and larger — than x264 at the same number. Nothing here
adjusts it for you, because silently re-interpreting a number you typed is worse
than saying what it means. If you want HEVC's saving rather than its extra
quality, raise the CRF by two or three.

The codec is independent of the encoder: every encoder below produces either.

## Choosing tracks

By default every audio and subtitle track the disc carries is passed through
untouched, each tagged with the language tsMuxeR reported for it, so a player
can tell them apart.

That default is often not what you want, because **lossless audio dominates the
output**. A well-compressed conversion of a clean CG feature can come out with
2.5 GB of video and 10 GB of audio: the single TrueHD Atmos track alone was
more than twice the video on one measured disc. Dropping the tracks you will
never play is the largest saving available that costs no picture quality.

Start by seeing what is there:

```sh
mvc2sbs --list --input "Toy Story 1995 3D.iso" --temp /scratch
```

```
track kind               lang  codec                info
4113  video (base view)  und   H.264                Profile: High@4.1 Resolution: 1920:1080p
4114  video (dependent)  und   MVC                  H.264/MVC Views: 2
4352  audio              eng   TrueHD Atmos         Bitrate: 0Kbps Channels: 8
4353  audio              eng   AC3                  Bitrate: 640Kbps Channels: 6
4354  audio              fra   DTS-HD Master Audio  Channels: 6
4356  audio              und   AC3                  Bitrate: 192Kbps Channels: 2
4608  subtitle           eng   PGS
```

Then narrow it. Language and codec are **both** required when both are given,
so the pair names one track rather than the union of two sets:

```sh
mvc2sbs --input disc.iso --output out.mkv \
        --audio-lang eng --audio-codec truehd --subs-lang eng
```

Notes on the matching:

- A codec is matched as a **substring**, case-insensitively, against both the
  stream ID and the human type. `truehd` finds `A_TRUEHD`, and `dts` finds both
  `A_DTS` and `DTS-HD Master Audio`, so you need not know which spelling the
  disc used.
- A language is an ISO-639 code as the disc states it. **`und` matches a track
  the disc gave no language for**, which is how a commentary track with no tag
  is selected — and it means an English filter will not sweep untagged tracks
  in.
- A filter that matches **nothing is an error**, reported before any work is
  done, listing what the disc actually has. Carrying every track on would
  defeat the request, and dropping all audio would produce a film nobody can
  watch, discovered hours later.
- Filtering happens **before the demux**, so a narrowed selection means fewer
  tracks to extract and less scratch space, not merely a smaller output.

`--list` needs the toolchain, and for a disc image it reads the image to get at
a playlist — there is no way to ask tsMuxeR what a source holds without
extracting it first. Pass `--temp` somewhere with room, and expect it to cost
what a conversion's first stage costs.

### The best track, rather than a named one

Naming a codec means knowing what the disc has. `--audio-best` instead keeps the
single highest-quality track of those matching, and it composes with the
language filter — this is "the best English track", not "the best track, if it
happens to be English":

```sh
mvc2sbs --input disc.iso --output out.mkv \
        --audio-lang eng --audio-best --subs-lang eng,pt-br
```

The ranking is, in order:

1. **Lossless beats lossy.** TrueHD, DTS-HD Master Audio, LPCM and FLAC rank
   above DTS-HD High Resolution and E-AC-3, which rank above AC-3, DTS and AAC.
   No bitrate of a lossy codec puts back what it discarded.
2. **Then channels.** A 7.1 track is what a 7.1 system is for, and a
   higher-bitrate 5.1 mix cannot supply the two channels it does not have.
3. **Then bitrate**, where the disc states one. Lossless tracks often do not,
   which is why it is only ever a tie-breaker.
4. **Then the disc's own order**, so the choice is deterministic and one disc
   always gives one answer.

The chosen track is logged, because a decision made on your behalf should be
visible rather than inferred from the finished file hours later:

```
mvc2sbs: audio: TrueHD Atmos 8ch (eng) lossless
```

`--audio-best` does not apply to subtitles. Several are routinely wanted at
once, and ranking PGS streams against each other would mean nothing.

### Languages, and what a disc actually calls them

Several languages at once is just a list: `--subs-lang eng,pt-br`.

A Blu-ray has no way to say *Brazilian* Portuguese in ISO-639-2 — there is only
`por` — so authoring tools variously emit `por`, or the non-standard `pob` or
`ptb`. Asking for `pt-br` matches whichever the disc chose, so you need not
know which. The same goes for the pairs where ISO-639-2 has both a
bibliographic and a terminological code and sources disagree about which to
use: `fra`/`fre`, `deu`/`ger` and `zho`/`chi` each match either spelling. A code
with no alias entry matches itself, so nothing is lost by not being listed.

### Naming the output after the audio

`--name-audio-codec` inserts the kept codec before the extension:

```
Toy Story (1995) 3D FSBS.mkv  ->  Toy Story (1995) 3D FSBS.TrueHD-Atmos.mkv
```

Useful with `--audio-best`, where the codec is whatever the disc turned out to
offer. The rename happens after the conversion, because the codec is not known
until the source has been probed, and probing a disc image twice to decide a
filename would cost as much as the conversion's first stage. The final path is
printed to stdout either way, so a script driving this need not guess at it.

## What plays the result, and at what resolution

The output declares its layout in the Matroska `StereoMode` element
(`side_by_side_left_first`), so a player need not infer 3D from the filename or
be told by hand. That flag is what decides whether the resolution this spent
hours preserving survives to the screen.

| player | what it does | per eye |
|---|---|---|
| **Kodi / CoreELEC** | reads the flag, splits the frame, emits HDMI **frame-packed** 3D | **1920x1080** |
| Plex | recognises only *half*-SBS and *half*-TAB, from the filename; treats this as flat 2D | 960x1080 at best |

Full per-eye resolution needs **frame packing**, the HDMI 3D format that carries
two complete 1920x1080 frames in one 1920x2205 transport. The side-by-side HDMI
format cannot: it squeezes both eyes into a single 1920x1080 frame, so each eye
is 960 columns stretched back to 1920, whatever the source file held.

So a full-SBS file is a 1:1 pixel map through a frame-packing player — each
1920x1080 view lands on the panel untouched — and a waste through anything that
only speaks half-SBS, which scales it down before the display ever sees it.
That is also the ceiling worth chasing: MVC on a 3D Blu-ray is 1080p per eye,
and consumer 3D displays present Full HD per eye, so 1920x1080 is both what the
disc holds and what the screen can show.

CoreELEC 21.1 and later handle `(F)SBS`, `(F)TAB` and MVC frame packing on
devices using the hdmitx20 driver. Half-SBS (`--layout half`) exists for players
that will only take that, and costs half the horizontal detail by definition.

## Tools, and why each is needed

Four, and one of them is either/or:

| Tool | What it does | Why nothing else will do |
|---|---|---|
| **tsMuxeR** | Gets the base and dependent views out of the disc, plus audio, subtitles and chapters | ffmpeg cannot: libavcodec drops the MVC dependent view outright |
| **edge264** | Decodes both eyes and stacks them side by side as Y4M | The only open-source *software* MVC decoder there is |
| **x264** / **x265** *or* **ffmpeg** | Re-encodes the stacked frames | Side-by-side is a new frame layout, so a re-encode is unavoidable. x264 or x265 follows `--codec`; ffmpeg instead, for GPU encoding or the `--swap-lr` / half-SBS filters |
| **mkvmerge** | Muxes the video back with the audio, subtitles and chapters | — |

Installing them is left to you; `--check` says what is missing, what each one
does, and where to start:

```
platform: linux   encoder: software   codec: h264

  ok       tsmuxer    /usr/local/bin/tsMuxeR
  ok       edge264    /usr/local/bin/edge264
  ok       x264       /usr/bin/x264
  ok       mkvmerge   /usr/bin/mkvmerge

all 4 required tools present
```

`--check` exits non-zero when anything is missing, so it works as a preflight.

### Architectures

Everything here works on 64-bit Arm — Apple Silicon, a Raspberry Pi 4/5 — as
well as x86_64:

| | x86_64 | arm64 |
|---|---|---|
| **tsMuxeR** | prebuilt (Linux, Windows) | prebuilt on macOS; **build the CLI** on Arm Linux |
| **edge264** | build (seconds) | build; its CI runs the full JVT conformance corpus on arm64 macOS *and* arm64 Linux |
| **x264 / ffmpeg / mkvmerge** | packaged | packaged |

The one gap is that upstream publishes a single Linux tsMuxeR binary and it is
x86_64. That is a packaging gap, not a portability one: its **CLI needs no Qt** —
that is the GUI alone — so on Arm Linux it is

```sh
apt install build-essential cmake ninja-build zlib1g-dev libfreetype-dev
cmake -S . -B build -G Ninja && ninja -C build tsmuxer
```

about a minute. `Dockerfile.mvc2sbs` does exactly that, which is what makes the
image multi-architecture.

## What it does with the rest of the disc

Audio and subtitle tracks are demuxed alongside the two views and muxed into the
output in the order the source listed them, so the first audio track stays
first. One output track per disc track: a Blu-ray TrueHD stream carries an
embedded AC-3 core for players that cannot decode TrueHD, and DTS-HD carries a
plain DTS core the same way, which tsMuxeR writes as a single file named
`.ac3+thd`. mkvmerge presents that as two tracks, so each demuxed file is
identified and only the track the disc listed is kept — the core is the same
audio, lossily, and an extra track the probe never reported would be a
surprise. The match is on the codec rather than the position, since the two
tools spell codecs differently (`TRUE-HD` against `TrueHD Atmos`) and the
primary is not promised to come first. Each is tagged with the language tsMuxeR reported for it; a track the
disc gave no language for is passed untagged rather than guessed at, since an
absent tag already means undetermined in Matroska and claiming a language the
disc never stated would be worse than saying nothing. Use
[the track filters](#choosing-tracks) to carry fewer of them. A track the demux
failed to produce is reported and skipped — that costs a language, not the
film.

The views are identified by **stream ID**, not by order or track number: a disc
is not obliged to list them in any order, and taking the wrong one as the base
gives a stream that cannot decode at all.

A source that is not 3D is refused by name rather than failing obscurely:

```
mvc2sbs: no MVC track: this source is not 3D (found V_MPEG4/ISO/AVC (track 4113))
```

So are two MVC tracks, an MVC track with no AVC base view, and an elementary
stream handed in where a container was expected.

## Testing without a disc

tsMuxeR muxes as well as demuxes, which makes an end-to-end test possible with
no 3D Blu-ray: mux a pair of MVC elementary streams into a real 3D m2ts, then
convert it. `TestRunnerConvertsARealSource` does exactly that and checks the
output is 1280×480 with its audio intact.

The MVC streams come from
[mvc-source](https://github.com/jens-duttke/mvc-source)'s `tests/fixtures`,
which ships `mvc_base.264`, `mvc_dependent.mvc` and `mvc_combined.264` at 35, 23
and 57 KB — the exact shape a demux produces. Point the tests at them:

```sh
MVC_TEST_FIXTURES=/path/to/mvc-source/tests/fixtures go test ./internal/mvc/
```

They skip when it is unset, so CI stays green without the toolchain.

## Encoders per platform

| Platform | Hardware | Software |
|---|---|---|
| Linux | NVENC, VAAPI | x264 / x265 |
| macOS | VideoToolbox | x264 / x265 |
| Windows | NVENC | x264 / x265 |

`auto` runs a **one-frame trial encode** for each candidate and takes the first
that succeeds, falling back to software. Merely finding ffmpeg is not evidence a
GPU is present — a stock build advertises `h264_nvenc` on a machine with no
NVIDIA card — and discovering that at the encode step would waste the hours
already spent decoding.

The trial is run for the codec being produced, not for the encoder in the
abstract: a GPU generation can carry an H.264 encoder and no HEVC one, so
`--codec h265` can fall back to x265 on the same machine where `--codec h264`
picks NVENC.

## Docker

`Dockerfile.mvc2sbs` carries the whole toolchain, for amd64 and arm64. Alpine,
the same base as the pipeliner image:

A release publishes it, for amd64 and arm64:

```sh
docker run --rm -v /media:/media ghcr.io/brunoga/mvc2sbs:latest --check
```

Or build it yourself:

```sh
docker build -f Dockerfile.mvc2sbs -t mvc2sbs .
docker buildx build --platform linux/amd64,linux/arm64 -f Dockerfile.mvc2sbs -t mvc2sbs .
```

| | size |
|---|---|
| pipeliner server image | 39 MB |
| mvc2sbs, amd64 | 189 MB |
| mvc2sbs, arm64 | 165 MB |

tsMuxeR is built rather than downloaded so the arm64 image is a real arm64 image
— verified by building it and checking every binary reports
`ELF 64-bit ARM aarch64`. edge264 is given an architecture baseline
(`x86-64-v2` or `armv8-a+simd`) instead of the Makefile's default
`-march=native`, which would otherwise bake the builder's CPU into a distributed
image.

### Running the conversion out of the image

mvc2sbs knows nothing about Docker: it runs its four tools from its own PATH, so
inside the image it just works. That means you can skip installing the toolchain
on the host and let pipeliner drive the image instead — `exec` takes an `args`
list, so `docker` becomes the command and nothing needs quoting:

```python
output("exec", upstream=once, command="docker",
       args=["run", "--rm",
             "--volume", "/media:/media",
             "--gpus", "all",                    # or --device /dev/dri for VAAPI
             "ghcr.io/brunoga/mvc2sbs:latest",
             "--input", "{file_location}",
             "--output", "{sbs_path}", "--quiet"])
```

Both forms behave identically to the pipeline: a non-zero exit fails the entry,
so the conversion is retried rather than recorded as done. Hardware encoding
needs the device passed through, which is the one thing the container cannot
arrange for itself.

### Why this is a separate image

It no longer *has* to be. The toolchain is multi-architecture now and Alpine
carries all of it, so folding it into the server image would work. Two reasons
not to:

- **The tools want to be where the GPU is, and that is usually not where the
  scheduler runs.** Hardware encoding needs `/dev/dri` or the NVIDIA runtime;
  giving the long-lived scheduler that access, on a box that may have no card at
  all, buys nothing.
- **It would be a 5x image for a minority feature** — 39 MB to roughly 190 MB
  for every pipeliner user, most of whom have no 3D discs.

A conversion is also hours of CPU or GPU work, so keeping it out of the
scheduler's container means it cannot take the scheduler down with it.

## Driving it from pipeliner

Pair it with the [`exec`](../../plugins/sink/exec/README.md) sink, whose `args`
list passes each value as one argument, so paths with spaces need no quoting:

```python
src  = input("filesystem", path="/media/3d-staging", recursive=True, mask="*.iso")
meta = process("metainfo_file", upstream=src)
conv = output("exec", upstream=meta,
              command="/usr/local/bin/mvc2sbs",
              args=["--input", "{file_location}",
                    "--output", "/media/3dmovies/{title} ({video_year}).mkv"])
pipeline("convert-3d", schedule="0 4 * * *")
```

A non-zero exit fails the entry, so a failed conversion is not recorded as done
and is retried next run.
