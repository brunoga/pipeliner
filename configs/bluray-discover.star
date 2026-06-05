# bluray-discover.star
#
# Demonstrates bluray_releases used as a **search backend** for discover().
#
# discover() takes upstream entries whose .Title fields supply the search
# queries, then calls Search() on each configured backend for each title.
# Here the only backend is bluray_releases, so each title resolves to its
# Blu-ray.com catalog entries (typically one per format: BD, UHD, BD3D).
#
# Requirements: TRAKT_CLIENT_ID. Only the (public) Trakt client_id is
# needed — "trending" is a public list, no OAuth required.
#
# The discover per-title cooldown (interval) keeps repeat runs cheap; results
# returned by bluray_releases.Search() also persist to cache_bluray_index, so
# the second pass for the same title hits the cache rather than /search/.

trakt_id = env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID")

# Upstream title source: top trending movies on Trakt. The .Title field on
# each entry is what discover() feeds to Search().
trending = input("trakt_list",
                 client_id=trakt_id,
                 type="movies",
                 list="trending",
                 limit=25)

disc = process("discover",
               upstream=trending,
               search=[{"name": "bluray_releases"}],
               interval="168h")

# Keep only the 3D editions among the catalog hits. Expressed as a reject so
# non-matching entries are actively dropped — accept-only would leave them
# Undecided and PassThrough would forward them to the sink unchanged.
only_3d = process("condition", upstream=disc,
                  reject="bluray_format != 'BD3D'")

output("print", upstream=only_3d)

pipeline("bluray-find-3d-editions", schedule="24h")
