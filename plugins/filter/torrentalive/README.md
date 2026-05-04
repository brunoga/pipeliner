# torrent_alive

Rejects torrents with fewer than a minimum number of seeds.

Entries without a `torrent_seeds` field are left undecided (no-op). The
`torrent_seeds` field is populated by the `rss` input plugin from torrent
namespace extensions in RSS feeds (nyaa, Jackett, ezrss, etc.).

## Config

```yaml
torrent_alive:
  min_seeds: 5   # minimum seeds required (default: 1)
```

## Example

```yaml
tasks:
  anime:
    rss:
      url: "https://nyaa.si/?page=rss&cats=1_2&filter=2"
    torrent_alive:
      min_seeds: 3
    series:
      shows:
        - "My Hero Academia"
```
