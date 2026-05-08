# content

Rejects or requires entries based on the filenames inside a torrent.

Requires `metainfo_torrent` (or another plugin that sets `torrent_files`) to run
first so that entry field is populated.

## Config

```yaml
content:
  reject:            # glob patterns — reject if ANY file matches
    - "*.exe"
    - "*.nfo"
  require:           # glob patterns — reject if NO file matches
    - "*.mkv"
```

At least one of `reject` or `require` must be specified. Patterns use
[`path.Match`](https://pkg.go.dev/path#Match) semantics and are tested against
both the full path and the filename component (`path.Base`).

Entries without a `torrent_files` field are left undecided (neither accepted
nor rejected).

## Example

```yaml
tasks:
  tv:
    - rss:
        url: "https://example.com/rss"
    - metainfo_torrent:
    - content:
        reject:
          - "*.exe"
          - "*.bat"
        require:
          - "*.mkv"
```
