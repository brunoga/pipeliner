# content

Rejects or requires entries based on filenames. The file list is resolved in order:

1. **`torrent_files`** — set by `metainfo_torrent` (from the `.torrent` file) or `metainfo_magnet` (via DHT). Most complete; includes every file in the torrent.
2. **`file_location` basename** — set by the `filesystem` plugin for local files.
3. **URL path component** — the last segment of `e.URL`, for direct-download entries whose URL reveals the file type (e.g. `http://tracker.com/file.rar`).

When none of the above is available the check is skipped and a `Warn` is logged. Run `--log-level debug` to see which fallback source was used.

## Config

```python
plugin("content",
    reject=["*.exe", "*.nfo"],   # glob patterns — reject if ANY file matches
    require=["*.mkv"],           # glob patterns — reject if NO file matches
)
```

At least one of `reject` or `require` must be specified. Patterns use
[`path.Match`](https://pkg.go.dev/path#Match) semantics and are tested against
both the full path and the filename component (`path.Base`).

## Example

```python
task("tv", [
    plugin("rss", url="https://example.com/rss"),
    plugin("metainfo_torrent"),
    plugin("content",
        reject=["*.exe", "*.bat"],
        require=["*.mkv"],
    ),
])
```
