# upgrade-quality.star
#
# Downloads quality upgrades for configured TV shows. When a higher-quality
# release of an already-downloaded episode appears (e.g. a BluRay encode of
# a WEB-DL episode), upgrade accepts it; otherwise the entry is rejected.
#
# upgrade tracks the best-known quality per episode. State is committed only
# after the download succeeds (CommitPlugin), so a failed download is retried.
# Once an episode reaches the target ceiling quality it is not upgraded again.
#
# Typical placement: after metainfo_quality and metainfo_series (to build the
# episode key), before dedup (to pick the best from multiple variants).

rss_url    = "https://feeds.example.com/all"
trans_host = "localhost"
tv_path    = "/media/tv"

src    = input("rss",               url=rss_url)
seen   = process("seen",            upstream=src)
q      = process("metainfo_quality", upstream=seen)
ep     = process("metainfo_series",  upstream=q)
upg    = process("upgrade",         upstream=ep,
    target="1080p bluray",   # stop upgrading once we have a 1080p BluRay
    on_lower="reject")       # reject re-downloads at the same or lower quality
best   = process("dedup",           upstream=upg)
fmt    = process("pathfmt",         upstream=best,
    path=tv_path + "/{title}/Season {series_season:02d}",
    field="download_path")
output("transmission", upstream=fmt,
    host=trans_host, path="{download_path}")

pipeline("quality-upgrade", schedule="6h")
