# bluray-calendar.star
#
# Demonstrates bluray_releases used as a DAG **source** (Generate path).
#
# Scrapes the Blu-ray.com release calendar for the last two months and emits
# one entry per release. Filters down to just 3D Blu-ray releases and prints
# them — swap the print sink for notify/webhook/email if you want push.
#
# Side effect: every calendar pass also populates cache_bluray_index, which
# metainfo_bluray (see bluray-enrich.star) reads from so its downstream
# enrichment usually hits the cache rather than /search/.

cal      = input("bluray_releases",
                 country="us",
                 months=2,
                 formats=["BD3D"])
seen     = process("seen",          upstream=cal)
output("print", upstream=seen)

pipeline("bluray-new-3d", schedule="168h")
