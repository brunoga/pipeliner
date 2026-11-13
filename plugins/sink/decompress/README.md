# decompress

Extracts RAR, ZIP and 7z archives to a destination directory.

- **RAR** (1.5 to 5, including multi-volume sets named `.r00`/`.r01` or `.part1.rar`/`.part2.rar`) and **ZIP** are extracted in-process. No tool needs to be installed.
- **7z** uses the system `7z` (the Docker image ships Alpine's `7zip`).
- `tool` forces a system tool (`unrar`, `7z` or `unar`) for every archive.

Before this, a system tool was required. The Docker image had none, so the plugin failed to start there. Alpine's `7zip` cannot read RAR, and Alpine packages neither `unrar` nor `unar`, which is why RAR is now handled in-process.

## What is extracted

The archive comes from:

1. `file_location`, set by `filesystem` or `download`; otherwise
2. a torrent's `torrent_download_dir` joined with its name, set by `torrent_session`; otherwise
3. a URL ending in `.rar`, `.zip` or `.7z`.

A folder is searched recursively for the **first volume of every archive set**: `x.rar` (not `x.r00`), `x.part1.rar` (not `x.part2.rar`), `x.7z.001`. A season pack is often one RAR set per episode, each in its own folder. Folders and files named `sample` are skipped.

## Safety

- **Nothing is overwritten.** A file that already exists is skipped (the system tools are run with `-o-`, `-aos` or `-skip`). Running the same extraction twice is harmless.
- Each file is written as `name.partial` and renamed when complete, so an interrupted run leaves nothing a later run would mistake for a finished file.
- An archive entry with an absolute name, or one that climbs out with `..`, fails the extraction instead of writing outside the destination.
- Extracted folders and files **take after the destination root** (`to`): its owner and group, and its permission bits (setgid included; files without execute). Pipeliner often runs as root in a container while the library belongs to a user and a group shared with the download client. Without this, an extracted season folder was root's with mode 0755, and the client could not move a later episode into it.
- A failed archive fails the entry. The other archive sets of the same torrent are still extracted.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `to` | yes | — | Destination directory for extracted files |
| `mirror_from` | no | — | Staging root: the archive's directory below it is kept below `to` |
| `keep_dirs` | no | `true` | Preserve the archive's internal directory structure |
| `delete_archive` | no | `false` | Remove the whole archive set (all volumes) after a successful extraction |
| `tool` | no | `builtin` | `builtin`, or force `unrar`, `7z` or `unar` |

## `mirror_from`

`mirror_from` maps a staging tree onto a library. A torrent downloaded into `/data/downloads/tv-staging/Agatha All Along/Season 01` with

```python
output("decompress", upstream=done, to="/data/media/series",
       mirror_from="/data/downloads/tv-staging")
```

is extracted into `/data/media/series/Agatha All Along/Season 01`. The release's own folder name (`Agatha.All.Along.S01.1080p-GRP`) is not carried over. A torrent outside `mirror_from` fails instead of being extracted to a guessed location.

## Example

Unpack finished torrents from a staging area:

```python
session = input("torrent_session", backend="deluge", host="localhost", password=env("DELUGE_PASSWORD"))
done    = process("condition", upstream=session, rules=[
    {"accept": 'torrent_progress >= 100 and torrent_download_dir contains "/tv-staging/"'},
    {"reject": "true"},
])
new     = process("seen", upstream=done, local=True)
output("decompress", upstream=new, to="/data/media/series", mirror_from="/data/downloads/tv-staging")
pipeline("tv-unpack", schedule="10m")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | `file_location` or `torrent_download_dir` |
