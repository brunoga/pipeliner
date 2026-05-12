# movie-downloads.star
#
# Downloads movies from a static list at 1080p+, gated by a TMDb rating floor.
#
# Replace api_key, qBittorrent credentials, feed URL, and movie list.

qbt_host    = "localhost"
qbt_user    = "admin"
qbt_pass    = "changeme"
tmdb_key    = env("TMDB_API_KEY", default="YOUR_TMDB_API_KEY")
movies_path = "/media/movies"

src    = input("rss", url="https://example.com/rss/movies")
seen   = process("seen",             upstream=src)
q      = process("metainfo_quality", upstream=seen)
tmdb   = process("metainfo_tmdb",   upstream=q, api_key=tmdb_key)
movies = process("movies",           upstream=tmdb,
                  quality="1080p",
                  static=["Inception", "Interstellar", "The Dark Knight",
                           "Oppenheimer", "Dune"])
cond   = process("condition",        upstream=movies, reject="video_rating < 7.0")
fmt    = process("pathfmt",          upstream=cond,
                  path=movies_path + "/{title} ({video_year})",
                  field="download_path")
output("qbittorrent", upstream=fmt,
       host=qbt_host, username=qbt_user, password=qbt_pass,
       savepath="{download_path}", category="movies")

pipeline("movies", schedule="6h")
