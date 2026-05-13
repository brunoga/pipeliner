# Filter processors (`plugins/processor/filter/`)

Filter processors accept or reject entries. Use them with `process("plugin-name", upstream=…)`.

| Plugin | Description |
|--------|-------------|
| [`seen`](seen/README.md) | Reject entries already processed in a previous run |
| [`series`](series/README.md) | Accept episodes of configured TV shows; track downloads |
| [`movies`](movies/README.md) | Accept movies from a configured title list; track downloads |
| [`list_match`](list_match/README.md) | Accept entries whose title is in a persistent cross-pipeline list |
| [`trakt`](trakt/README.md) | Accept entries whose title matches a Trakt.tv list |
| [`tvdb`](tvdb/README.md) | Accept entries whose title matches TheTVDB user favorites |
| [`quality`](quality/README.md) | Reject entries below or above a quality range |
| [`regexp`](regexp/README.md) | Accept or reject entries by regular expression |
| [`exists`](exists/README.md) | Reject entries whose target file already exists on disk |
| [`condition`](condition/README.md) | Accept or reject entries using boolean expressions |
| [`content`](content/README.md) | Reject or require entries based on torrent file contents |
| [`premiere`](premiere/README.md) | Accept only the first episode of series not previously seen |
| [`torrentalive`](torrentalive/README.md) | Reject torrents with no active seeders |
| [`upgrade`](upgrade/README.md) | Accept entries that are a quality upgrade over what is on disk |
| [`require`](require/README.md) | Reject entries missing one or more required fields |
| [`accept_all`](accept_all/README.md) | Accept every undecided entry unconditionally |
| [`dedup`](dedup/README.md) | Keep the best-quality copy when multiple entries refer to the same episode or movie |
