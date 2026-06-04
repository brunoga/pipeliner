# bluray-enrich.star
#
# Demonstrates metainfo_bluray used as a **processor** that enriches an
# arbitrary movie source with Blu-ray.com metadata, plus a route() that
# acts on bluray_3d_release to separate real 3D releases from fake/upscaled
# ones tagged as 3D in their filename.
#
# Pipeline:
#   RSS → seen → metainfo_file → require → metainfo_bluray
#       → route(real_3d, fake_3d, other)
#       → three print sinks (replace with download targets in production)

src      = input("rss",             url="https://feeds.example.com/movies")
seen     = process("seen",          upstream=src)
meta     = process("metainfo_file", upstream=seen)
req      = process("require",       upstream=meta,
                   fields=["title", "video_year", "_quality"])
bluray   = process("metainfo_bluray", upstream=req)

routes = route(bluray,
    real_3d = "video_is_3d == true and bluray_3d_release == true",
    fake_3d = "video_is_3d == true and bluray_3d_release != true",
    other   = "video_is_3d != true",
)

output("print", upstream=routes.real_3d)
output("print", upstream=routes.fake_3d)
output("print", upstream=routes.other)

pipeline("bluray-fake-3d-detector", schedule="6h")
