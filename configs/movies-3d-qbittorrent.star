# movies-3d-qbittorrent.star
#
# Downloads both the best non-3D and best 3D copy of each movie independently.
# 3D and non-3D versions are tracked separately so having one does not block
# the other. The 3D format quality (BD3D > Full SBS/OU > Half SBS/OU) takes
# precedence over resolution and source when ranking competing 3D copies.

qbt_host    = "localhost"
qbt_user    = "admin"
qbt_pass    = "changeme"
movies_path = "/media/movies"

movie_list = ["Inception", "Interstellar", "The Dark Knight", "Avatar"]

def qbit(savepath, category):
    return plugin("qbittorrent",
        host=qbt_host, username=qbt_user, password=qbt_pass,
        savepath=savepath, category=category)

# Non-3D: 1080p+ BluRay/WEB-DL only. Condition explicitly rejects any 3D
# release so the two tasks track independently.
task("movies-flat",
    [
        plugin("rss", url="https://example.com/rss/movies"),
        plugin("seen"),
        plugin("metainfo_quality"),
        plugin("condition", reject="video_is_3d == true"),
        plugin("movies", quality="1080p+ webrip+", static=movie_list),
        plugin("pathfmt",
            path=movies_path + "/{title} ({video_year})",
            field="download_path"),
        qbit(savepath="{download_path}", category="movies"),
    ],
    schedule="6h")

# 3D: accept any 3D release at 1080p+ — the quality spec "1080p+ 3d+"
# rejects non-3D entries automatically. The engine prefers BD3D > Full > Half.
task("movies-3d",
    [
        plugin("rss", url="https://example.com/rss/movies"),
        plugin("seen"),
        plugin("movies", quality="1080p+ 3d+", static=movie_list),
        plugin("pathfmt",
            path=movies_path + "/{title} ({video_year}) 3D",
            field="download_path"),
        qbit(savepath="{download_path}", category="movies-3d"),
    ],
    schedule="6h")
