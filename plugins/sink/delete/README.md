# delete

Deletes the file at each entry's `file_location`, and the folders that leaves empty.

It only deletes **below the configured `roots`**, and never removes a root itself. An entry whose file is outside every root fails instead. Emptied folders are removed up to, but not including, the root, so a library folder such as `/data/media/3d-mvc-inbox` survives being emptied (a `rmdir -p` would have removed it).

Only files are deleted. A directory, or a path that does not exist, fails the entry. A symbolic link is removed as a link and never followed. A dry run deletes nothing.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `roots` | yes | — | Absolute directories below which files may be deleted (`/` is refused) |
| `prune_empty` | no | `true` | Remove folders a deletion leaves empty, up to the root |

## Example

Delete media files older than a week, except those a torrent still seeds from:

```python
files = input("filesystem", path="/data/media", recursive=True)
old   = process("condition", upstream=files, rules=[
    {"accept": "file_modified_time < daysago(7)"},
    {"reject": "true"},
])
free  = process("torrent_in_use", upstream=old, backend="deluge", password=env("DELUGE_PASSWORD"))
output("delete", upstream=free, roots=["/data/media/series", "/data/media/movies"])
pipeline("media-cleanup", schedule="30 0 * * *")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `file_location` |
