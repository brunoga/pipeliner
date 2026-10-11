# bitrate

Gates entries on their **implied video bitrate**, computed before downloading from the torrent's size and the runtime. The motivating case was a 4K 3D conversion that looked bad on screen but carried no CAM/TS marker in its name.

Two figures are stamped, because the file is not the video:

| field | what it is |
|---|---|
| `container_bitrate_mbps` | size ÷ runtime — every byte in the file, audio included |
| `video_bitrate_mbps` | the above minus an estimate of the audio |

The audio is a large and wholly unrelated share of the first figure. A 4K film with TrueHD Atmos 7.1 carries roughly 3.5 Mbps of audio; the same video with DD+ 5.1 carries 0.7. Measured on one real encode — 7.12 GB over 108 minutes, TrueHD Atmos plus an AC3 compatibility track — the file is 8.79 Mbps and the video is 5.12, so **42% of the number the floor used to judge was audio**. Judging the container figure rewards lossless audio and punishes efficient audio, which says nothing about the thing being gated.

The audio figure is an estimate taken from the format the release name states, so a release listing one track may carry three. Every value sits at the low end of its format's range, so the subtraction errs towards a larger video figure and a more lenient gate — the same principle as leaving entries with no size or runtime untouched.

Runtime comes from `probe_duration_sec` when a probe ran above this node (the container's own statement, needing no lookup) and from `video_runtime` otherwise. They disagree for an extended cut, where a provider's runtime describes a different film from the one in the torrent.

## Floors are H.264-equivalent

A configured floor is what a release would need **as H.264**. HEVC reaches the same picture at roughly 60% of that and AV1 at roughly 45%, so the floor each entry is held to is scaled by its codec:

| codec | scale | a 15 Mbps floor becomes |
|---|---|---|
| H.264 and older | 1.00 | 15.0 |
| HEVC / H.265 | 0.60 | 9.0 |
| AV1 | 0.45 | 6.8 |

Without this a single number either admits starved H.264 or refuses perfectly good HEVC — and the latter is what happened. A 15 Mbps floor for 2160p rejected HBO Max WEB-DLs at 7–10 Mbps, which are not encodes anyone chose the bitrate of: **a WEB-DL is a copy of what the service sent**, so calling it starved is calling the service starved. A rejection names the arithmetic (`9.0 Mbps (15 Mbps scaled by 0.60 for H.265)`) so a floor that is not the configured number explains itself.

## What a bitrate floor cannot tell you

It catches a release that is **broken**, not one that is merely worse. A quality-targeted encode spends what each scene needs, so a good 4K HEVC encode of clean digital footage can sit near 5 Mbps and score VMAF 98 while a bad one sits at the same number — the bitrate profiles have the same shape and only the result differs. Sampling the file does not fix this: measured across one such encode in 8-second windows, the rate ranges 1.8–7.0 Mbps, and a starved encode of the same material would look much the same.

For the claim a release name makes about its *resolution*, [`probe`](../../probe/)'s coded frame size is evidence where this is only an inference — see `probe_upscale`.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `min_2160p` | int | no | — | Minimum implied Mbps for 2160p entries (0/unset = no floor) |
| `min_1080p` | int | no | — | Minimum implied Mbps for 1080p entries |
| `min_720p` | int | no | — | Minimum implied Mbps for 720p entries |
| `min_other` | int | no | — | Minimum implied Mbps for other/unknown resolutions |

## Example

```python
meta = process("metainfo_tmdb", upstream=prev, api_key=TMDB_API_KEY)
rate = process("bitrate", upstream=meta, min_2160p=15, min_1080p=6)
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| MayProduce | `video_bitrate_mbps`, `container_bitrate_mbps` |
| Requires | `torrent_file_size`, and either `video_runtime` or `probe_duration_sec` |
