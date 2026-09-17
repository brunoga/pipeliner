# bitrate

Gates entries on their **implied bitrate** — torrent size divided by runtime — the one quality signal a release name cannot fake. A "2160p" encode at 12 Mbps is starved no matter what the name claims (the motivating case: a 4K 3D conversion that looked bad on screen but carried no CAM/TS marker in its name).

Both inputs are known **before downloading**: `torrent_file_size` from the indexer feed (or `metainfo_torrent`), `video_runtime` from TMDb/TVDB enrichment. The computed rate is stamped on every entry as `video_bitrate_mbps` for use in `condition` rules and notify templates; the optional per-resolution floors reject directly. Entries missing size or runtime are never rejected.

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
| MayProduce | `video_bitrate_mbps` |
| Requires | `torrent_file_size`, `video_runtime` |
