# movie-downloads.star
#
# Downloads movies from a static list at 1080p+, gated by a TMDb rating floor.

qbt_host    = "localhost"
qbt_user    = "admin"
qbt_pass    = "changeme"
tmdb_key    = "YOUR_TMDB_API_KEY"
movies_path = "/media/movies"

task("movies",
    [
        plugin("rss", url="https://example.com/rss/movies"),
        plugin("seen"),
        plugin("metainfo_quality"),
        plugin("metainfo_tmdb", api_key=tmdb_key),
        plugin("movies",
            quality="1080p",
            static=["Inception", "Interstellar", "The Dark Knight",
                    "Oppenheimer", "Dune"]),
        plugin("condition", reject="video_rating < 7.0"),
        plugin("pathfmt",
            path=movies_path + "/{title} ({video_year})",
            field="download_path"),
        plugin("qbittorrent",
            host=qbt_host, username=qbt_user, password=qbt_pass,
            savepath="{download_path}", category="movies"),
    ],
    schedule="6h")
