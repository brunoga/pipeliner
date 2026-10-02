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

### `stable_for` is a safety net, not a completion signal

It compares a file's modification time against the window, which covers the
common case well: a copy in progress keeps bumping the mtime, so the file stays
too young to emit. A download client that preallocates the full size and fills
it out of order is covered too — the size being final from the start plays no
part in the test.

Two cases defeat it:

- A writer that restores the original timestamp after writing (`rsync -t`
  resuming a file, `tar -p`) can look settled while incomplete.
- For content delivered as a **directory tree**, no single file's age says
  anything about the tree. A disc's `BDMV/index.bdmv` is small and written
  early, so it settles long before the streams beside it.

The airtight pattern for both is to deliver into a staging directory and rename
into the watched one. A rename within a filesystem is atomic, so the watched
directory never holds a partial file and nothing has to be inferred:

```sh
rsync -a remote:/rips/ /media/incoming/ && mv /media/incoming/* /media/staging/
```

### Directory trees

The plugin emits files, never directories. For a tree, match the one file it
has exactly one of and derive the directory with `dirname`:

```python
disc = input("filesystem", path="/media/3d-staging", recursive=True,
             mask="index.bdmv", stable_for="2m")
# ... --input "{{dirname .file_location}}"  →  /media/3d-staging/Movie/BDMV
```

`mask="*.m2ts"` with `recursive=True` would instead match every stream file in
`BDMV/STREAM`, which is dozens of entries for one film. See
`configs/convert-3d-mvc.star` for the worked example.
