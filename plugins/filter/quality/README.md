# quality

Rejects entries whose parsed video quality falls outside a configured range. At least one of `min`, `max`, or a `+`-suffix spec must be set.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `min` | string | conditional | — | Minimum quality spec (e.g. `720p`, `webrip`) |
| `max` | string | conditional | — | Maximum quality spec |

Quality specs are compared across six dimensions: resolution, source, codec, audio, color range, and 3D format. A release must meet or exceed every configured dimension to pass.

## Spec syntax

A spec token names a single value or a range for one quality dimension. Tokens are separated by spaces; all must match.

| Form | Meaning | Example |
|------|---------|---------|
| `value` | Exact value | `1080p`, `webrip` |
| `min-max` | Inclusive range | `720p-1080p`, `hdtv-bluray` |
| `value+` | This value or better (no upper bound) | `720p+`, `dvdrip+` |

### Recognized values

| Dimension | Values (low → high) |
|-----------|---------------------|
| Resolution | `sd`, `480p`, `576p`, `720p`, `1080p`, `2160p` / `4k` |
| Source | `dvdrip`, `tvrip`, `hdtv`, `webrip`, `webdl` / `web-dl`, `bluray`, `remux` |
| Codec | `xvid`, `divx`, `h264` / `x264`, `h265` / `x265` / `hevc`, `av1` |
| Audio | `mp3`, `aac`, `dd` / `dolbydigital`, `dts`, `truehd`, `atmos` |
| Color range | `sdr`, `hdr`, `hdr10`, `dv` / `dolbyvision` |
| 3D format | `3d` / `3d-half` / `half`, `3d-full` / `full` / `sbs` / `ou`, `bd3d` / `bd` |

## 3D format dimension

When a 3D token is included in the spec, non-3D entries are rejected automatically — the zero value of Format3D (not 3D) is below any 3D tier. Omitting a 3D token leaves the dimension unconstrained, accepting both 3D and non-3D entries.

```yaml
movies:
  quality: 1080p+ 3d+      # any 3D at 1080p or better
  quality: 1080p+ bd3d     # exactly BD3D
  quality: 1080p+          # unchanged — accepts both 3D and non-3D
```

## Implicit CAM/TS rejection

Releases with no recognized source (CAM, TS, etc.) have source `Unknown = 0`. Any `MinSource` constraint rejects them automatically — no extra rule needed.

## Example — standalone filter plugin

```yaml
tasks:
  hd-only:
    - rss:
        url: "https://example.com/feed"
    - quality:
        min: 720p
        max: 1080p
```

## Example — inline quality spec in series / movies / premiere

The `series`, `movies`, and `premiere` filters accept a `quality:` key directly, eliminating the need for a separate `quality` plugin:

```yaml
tasks:
  tv:
    - rss:
        url: "https://example.com/feed"
    - series:
        static: ["Breaking Bad"]
        quality: 720p+ webrip+

  movies-3d:
    - rss:
        url: "https://example.com/feed"
    - movies:
        static: ["Avatar", "Inception"]
        quality: 1080p+ bd3d    # BD3D only; non-3D copies rejected automatically
```
