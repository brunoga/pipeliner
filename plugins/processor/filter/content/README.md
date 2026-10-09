# content

Rejects entries based on the filenames inside a torrent or archive. The file list is resolved in this order:

1. **`torrent_files`** — set by `metainfo_torrent` or `metainfo_magnet`. Most complete; contains every file in the torrent.
2. **`file_location` basename** — set by the `filesystem` plugin for local files.
3. **URL path component** — last segment of the entry URL, for direct-download links whose URL reveals the file type.

When none of the above is available the check is skipped and a warning is logged. Run `--log-level debug` to see which source was used.

Patterns use [`path.Match`](https://pkg.go.dev/path#Match) semantics and are tested against both the full path and the filename component.

**Matching ignores case**, on both sides. A release group writes the extension however it pleases — `SUPERGIRL.ISO`, `Movie.RAR`, `sample.MKV` — and `path.Match` is case-sensitive, so `reject=["*.iso"]` used to pass a 90 GB disc image named `SUPERGIRL.ISO` straight through to the download client. Nothing was logged, because the check ran and found nothing to object to. Nobody writing that pattern means "only the lowercase ones".

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `reject` | conditional | — | Glob patterns; entry rejected if ANY file matches |
| `require` | conditional | — | Glob patterns; entry rejected if NO file matches |

At least one of `reject` or `require` must be specified.

## Example

```python
meta = process("metainfo_torrent", upstream=upstream)
flt  = process("content", upstream=meta,
               reject=["*.exe", "*.bat"], require=["*.mkv"])
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | `torrent_files` (falls back to URL/`file_location` when absent) |
