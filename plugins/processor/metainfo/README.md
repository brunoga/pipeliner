# Metainfo processors (`plugins/processor/metainfo/`)

Metainfo processors enrich entries with extra fields. Use them with `process("plugin-name", upstream=…)`.
Place each before any filter that reads the fields it produces.

| Plugin | Description |
|--------|-------------|
| [`metainfo_file`](file/README.md) | Parse the filename and annotate everything detectable in one pass: classify as series/movie, set series/movie/quality fields, and stamp `media_type` |
| [`metainfo_tmdb`](tmdb/README.md) | Enrich movie entries with TMDb metadata |
| [`metainfo_tvdb`](tvdb/README.md) | Enrich series entries with TheTVDB metadata |
| [`metainfo_trakt`](trakt/README.md) | Annotate entries with Trakt.tv metadata |
| [`metainfo_bluray`](bluray/README.md) | Enrich movie entries with Blu-ray.com metadata; sets `bluray_3d_release` to identify real 3D Blu-ray titles vs fake/upscaled 3D rips |
| [`metainfo_torrent`](torrent/README.md) | Read `.torrent` file metadata (info hash, size, file list) |
| [`metainfo_magnet`](magnet/README.md) | Annotate magnet-link entries with info hash, trackers, and DHT metadata |
| [`series_lifecycle`](lifecycle/README.md) | Classify tracked shows as complete/dormant/active from TheTVDB status + episode list vs the series tracker |
