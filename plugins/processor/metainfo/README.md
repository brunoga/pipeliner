# Metainfo processors (`plugins/processor/metainfo/`)

Metainfo processors enrich entries with extra fields. Use them with `process("plugin-name", from_=…)`.
Place each before any filter that reads the fields it produces.

| Plugin | Description |
|--------|-------------|
| [`metainfo_quality`](quality/README.md) | Parse quality tags (resolution, source, codec) from the title |
| [`metainfo_series`](series/README.md) | Parse series name, season, and episode from the title |
| [`metainfo_tmdb`](tmdb/README.md) | Enrich movie entries with TMDb metadata |
| [`metainfo_tvdb`](tvdb/README.md) | Enrich series entries with TheTVDB metadata |
| [`metainfo_trakt`](trakt/README.md) | Annotate entries with Trakt.tv metadata |
| [`metainfo_torrent`](torrent/README.md) | Read `.torrent` file metadata (info hash, size, file list) |
| [`metainfo_magnet`](magnet/README.md) | Annotate magnet-link entries with info hash, trackers, and DHT metadata |
