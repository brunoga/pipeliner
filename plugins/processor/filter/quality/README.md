# quality

Rejects entries whose parsed video quality falls outside a configured range. At least one of `min` or `max` must be set. Place `metainfo_file` upstream so the `video_quality` field is populated before this filter runs.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `min` | conditional | — | Minimum quality spec (e.g. `720p`, `webrip`) |
| `max` | conditional | — | Maximum quality spec (e.g. `1080p`, `bluray`) |

Quality specs are compared across six dimensions: resolution, source, codec, audio, colour range, and 3D format. A release must meet or exceed every configured dimension to pass.

## Spec syntax

| Form | Meaning | Example |
|------|---------|---------|
| `value` | Exact value | `1080p`, `webrip` |
| `min-max` | Inclusive range | `720p-1080p`, `hdtv-bluray` |
| `value+` | This value or better | `720p+`, `webrip+` |

### Recognised values (low → high)

| Dimension | Values |
|-----------|--------|
| Resolution | `sd`, `480p`, `576p`, `720p`, `1080p`, `2160p` / `4k` |
| Source | `cam`, `ts` / `tc`, `scr`, `dvdrip`, `tvrip`, `hdtv`, `webrip`, `webdl` / `web-dl`, `bluray`, `remux` |
| Codec | `xvid`, `divx`, `h264` / `x264`, `h265` / `x265` / `hevc`, `av1` |
| Audio | `mp3`, `aac`, `dd` / `dolbydigital`, `dts`, `truehd`, `atmos` |
| Colour range | `sdr`, `hdr`, `hdr10`, `dv` / `dolbyvision` |
| 3D format | `3d` / `3d-half` / `half`, `3d-full` / `full` / `sbs` / `ou`, `bd3d` / `bd` |

## CAM/TS/SCR rejection

Theater-recorded and pre-release sources sit at the bottom of the source hierarchy. Any `webrip+` or higher spec automatically rejects them — no extra condition needed.

| Source | Detected markers |
|--------|-----------------|
| `CAM` | `CAM`, `CAMRIP`, `HDCAM` |
| `TS` | `TS`, `HDTS`, `TC`, `HDTC`, `TELESYNC`, `TELECINE` |
| `SCR` | `SCR`, `SCREENER`, `DVDScr`, `DVDScreener`, `BDScr` |

## Example

```python
src = input("rss", url="https://example.com/rss")
q   = process("metainfo_file", upstream=src)
flt = process("quality", upstream=q, min="720p webrip+")
acc = process("accept_all", upstream=flt)
output("transmission", upstream=acc, host="localhost")
pipeline("hd-only", schedule="1h")
```

The `series`, `movies`, and `premiere` plugins accept a `quality=` key directly, eliminating the need for a separate `quality` node:

```python
meta   = process("metainfo_file", upstream=seen)
series = process("series",        upstream=meta, static=["Breaking Bad"], quality="720p+ webrip+")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | `quality` (parsed quality string, usable in condition expressions) |
| Requires | — |

## Notes

- When a 3D token is included in the spec, non-3D entries are rejected automatically; omitting a 3D token leaves it unconstrained.
- The `series`, `movies`, and `premiere` filters apply quality filtering internally — a separate `quality` node is only needed for finer control or pipelines without those filters.
