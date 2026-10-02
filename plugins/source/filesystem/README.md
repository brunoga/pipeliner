# filesystem

Walks a local directory and emits one entry per file. Entry URLs use the `file://` scheme.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `path` | yes | — | Directory to scan |
| `recursive` | no | `false` | Recurse into subdirectories |
| `mask` | no | — | Glob pattern to filter filenames (e.g. `*.torrent`) |
| `stable_for` | no | — | Skip a file whose content changed more recently than this (e.g. `2m`), leaving it for a later run |

## Fields set on entry

| Field | Description |
|-------|-------------|
| `source` | Origin in the form `filesystem:<path>` (e.g. `filesystem:/downloads/watch`) |
| `title` | File name (same as `file_name`) |
| `file_name` | File name including extension |
| `file_extension` | File extension including the leading dot (e.g. `.torrent`) |
| `file_location` | Absolute file path |
| `file_size` | File size in bytes |
| `file_modified_time` | Last-modified timestamp |

## DAG role

| Property | Value |
|----------|-------|
| Role | `source` |
| Produces | `source`, `title`, `file_name`, `file_extension`, `file_location`, `file_size`, `file_modified_time` |
| Requires | — |

## Example

```python
src   = input("filesystem", path="/downloads/watch", mask="*.torrent")
meta  = process("metainfo_torrent", upstream=src)
output("transmission", upstream=meta, host="localhost")
pipeline("watch-folder", schedule="5m")
```

## Using it as a watch folder

A short schedule plus a downstream `seen` filter is the whole mechanism: each
scan emits every matching file, `seen` drops the ones already handled, and
because `seen` commits only after the sinks confirm, a file whose processing
failed is retried on the next run rather than silently lost.

```python
src  = input("filesystem", path="/downloads/watch", mask="*.torrent",
             stable_for="1m")
once = process("seen", upstream=src, local=True, fields=["file_location"])
output("transmission", upstream=once, host="localhost")
pipeline("watch-folder", schedule="5m")
```

There is no filesystem-notification path, which is deliberate. A scan cannot
miss a file that appeared while the daemon was stopped, and it works on a
network mount, where writes made on the far side raise no local events at all.
For the jobs this feeds, the latency a notification would save is not worth a
second mechanism that can only ever be an optimisation.

### How `stable_for` decides

Each scan fingerprints every **item** — the top-level child of the watched
directory — over the path, size and modification time of every file beneath it.
An item is emitted once two scans have seen the same fingerprint, at least
`stable_for` apart. Anything that changes in between restarts the window.

The item, not the file, is the unit because that is the unit things arrive in:
a download client, a torrent and an rsync each produce one directory (or one
file) per release. The `mask` decides what is *emitted*; it plays no part in
deciding what has settled.

Two consequences worth knowing:

- **Nothing is emitted on first sight**, which costs one scan interval of
  latency. A single observation cannot tell a finished delivery from a paused
  one, and for a tree there is nothing in one observation to inspect — it
  simply has fewer files in it than it will have.
- **Timestamps are not trusted on their own.** `rsync -t` restores the source's
  modification time, and `rsync -a` implies it, so a file that arrived seconds
  ago can carry a timestamp days old. An age-based test would call such a file
  settled immediately; comparing contents across the window does not care.

A change has to hide from both the size and the timestamp, for every file in
the item, to go unnoticed.

### Directory trees

The plugin emits files, never directories — but settling is per item, so a
whole tree is withheld while any part of it is still arriving. For a tree,
match the one file it has exactly one of and derive the directory with
`dirname`:

```python
disc = input("filesystem", path="/media/3d-staging", recursive=True,
             mask="index.bdmv", stable_for="2m")
# ... --input "{{dirname .file_location}}"  →  /media/3d-staging/Movie/BDMV
```

`mask="*.m2ts"` with `recursive=True` would instead match every stream file in
`BDMV/STREAM`, which is dozens of entries for one film. See
`configs/convert-3d-mvc.star` for the worked example.

Note that `index.bdmv` is small and written early, so a test based on *its*
timestamp would pass long before the streams beside it had arrived. Settling
per item is what makes matching it safe.

### Cost

A scan stats every file under the watched path, not just the ones the mask
selects, because an unmasked file arriving still means its item changed.
`recursive=False` limits that to the top level. The settle snapshots are one
small database row per item in flight, pruned as items leave.
