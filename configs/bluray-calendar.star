# bluray-calendar.star
#
# Demonstrates bluray_releases used as a DAG **source** (Generate path).
#
# Pipeline 1 — recurring weekly scan for newly released 3D Blu-rays. The
# formats=["BD3D"] config auto-routes Generate to /3d/releasedates.php
# (Blu-ray.com's BD3D-only calendar) so each window fetches one small page
# instead of a large generic page with 100+ mostly-irrelevant rows.
#
# Side effect: every calendar pass also populates cache_bluray_index, which
# metainfo_bluray (see bluray-enrich.star) reads from so downstream
# enrichment usually hits the cache rather than /search/.

cal      = input("bluray_releases",
                 country="us",
                 months=2,
                 formats=["BD3D"])
seen     = process("seen",          upstream=cal)
output("print", upstream=seen)

pipeline("bluray-new-3d", schedule="168h")


# Pipeline 2 — one-shot historical backfill of the entire BD3D catalog.
# BD3D launched in September 2010; scanning every month since populates
# cache_bluray_index with ~all known 3D Blu-ray releases. ~180 requests at
# the default 1 req/sec ≈ 3 minutes. After running it once, metainfo_bluray
# enrichment for any 3D-era movie hits the local cache without an HTTP
# round-trip. Schedule this very rarely (e.g. yearly) or trigger it manually
# via the web UI / CLI.

backfill = input("bluray_releases",
                 country="us",
                 from_year=2010, from_month=9,
                 to_year=2025,  to_month=12,
                 formats=["BD3D"])
output("print", upstream=backfill)

pipeline("bluray-3d-backfill", schedule="8760h")
