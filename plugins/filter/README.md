# Filter plugins

Filter plugins decide the fate of each entry. An entry starts **undecided** and a filter can move it to **accepted** or **rejected**. Once rejected an entry cannot be un-rejected. Entries still undecided after all filters are dropped and never passed to output plugins.

Filters run in the order they appear in the config file.

| Plugin | Description |
|--------|-------------|
| [seen](seen/README.md) | Reject entries already processed in a previous run |
| [series](series/README.md) | Accept episodes of configured TV shows; track downloads |
| [movies](movies/README.md) | Accept movies from a configured title list; track downloads |
| [list_match](list_match/README.md) | Accept entries whose title is in a persistent cross-task list |
| [trakt](trakt/README.md) | Accept entries whose title matches a Trakt.tv list |
| [tvdb](tvdb/README.md) | Accept entries whose title matches TheTVDB user favorites |
| [quality](quality/README.md) | Reject entries below or above a quality range |
| [regexp](regexp/README.md) | Accept or reject entries by regular expression |
| [exists](exists/README.md) | Reject entries whose target file already exists on disk |
| [condition](condition/README.md) | Accept or reject entries using boolean expressions |
| [content](content/README.md) | Reject or require entries based on torrent file contents |
| [premiere](premiere/README.md) | Reject entries for episodes that have not yet aired |
| [torrentalive](torrentalive/README.md) | Reject torrents with no active seeders |
| [upgrade](upgrade/README.md) | Accept entries that represent a quality upgrade over what is on disk |
| [require](require/README.md) | Reject entries missing one or more required fields |
| [accept_all](accept_all/README.md) | Accept every undecided entry unconditionally |

## Recommended ordering

A well-ordered filter chain rejects cheaply first:

1. `seen` — fast database lookup; eliminates already-processed entries early
2. Title-based filter (`series`, `movies`, `list_match`, `trakt`, `tvdb`, or `regexp`) — narrow to relevant content
3. `quality` — enforce a resolution/source floor before hitting external APIs
4. `premiere` — skip entries that haven't aired yet
5. `exists` — skip if the file is already on disk
6. `content` / `torrentalive` — torrent-specific checks (require metainfo first)
7. `condition` — any remaining business logic via boolean expressions
8. `upgrade` — accept only if the new copy is strictly better
9. `require` — gate on required field presence before output
10. `accept_all` — when used, place last to catch anything not yet decided
