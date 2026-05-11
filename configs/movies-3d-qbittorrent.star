# movies-3d-qbittorrent.star
#
# Downloads both the best non-3D and best 3D copy of each movie independently.
# 3D and non-3D versions are tracked separately so having one does not block
# the other. The 3D format quality (BD3D > Full SBS/OU > Half SBS/OU) takes
# precedence over resolution and source when ranking competing 3D copies.
#
# Two pipelines share the same movie list; dedup keeps one copy per variant.

qbt_host    = "localhost"
qbt_user    = "admin"
qbt_pass    = "changeme"
movies_path = "/media/movies"

movie_list = ["Inception", "Interstellar", "The Dark Knight", "Avatar"]

def qbit_output(upstream, category):
    output("qbittorrent", from_=upstream,
           host=qbt_host, username=qbt_user, password=qbt_pass,
           savepath="{download_path}", category=category)

# ── Pipeline 1: flat (non-3D), 1080p+ BluRay/WEB-DL ─────────────────────────
# condition explicitly rejects 3D so the two pipelines track independently.

src1    = input("rss", url="https://example.com/rss/movies")
seen1   = process("seen",             from_=src1)
q1      = process("metainfo_quality", from_=seen1)
no3d    = process("condition",        from_=q1, reject="video_is_3d == true")
movies1 = process("movies",           from_=no3d, quality="1080p+ webrip+",
                   static=movie_list)
dd1     = process("dedup",            from_=movies1)
fmt1    = process("pathfmt",          from_=dd1,
                   path=movies_path + "/{title} ({video_year})",
                   field="download_path")
qbit_output(fmt1, category="movies")

pipeline("movies-flat", schedule="6h")

# ── Pipeline 2: 3D only, any 3D format at 1080p+ ─────────────────────────────
# quality spec "1080p+ 3d+" rejects non-3D entries automatically.
# The dedup processor prefers BD3D > Full > Half among competing 3D copies.

src2    = input("rss", url="https://example.com/rss/movies")
seen2   = process("seen",    from_=src2)
movies2 = process("movies",  from_=seen2, quality="1080p+ 3d+", static=movie_list)
dd2     = process("dedup",   from_=movies2)
fmt2    = process("pathfmt", from_=dd2,
                   path=movies_path + "/{title} ({video_year}) 3D",
                   field="download_path")
qbit_output(fmt2, category="movies-3d")

pipeline("movies-3d", schedule="6h")
