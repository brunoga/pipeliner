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
seen   = process("seen",          upstream=src)
meta   = process("metainfo_file", upstream=seen)
req    = process("require",       upstream=meta,
                  fields=["title", "video_year", "_quality"])
tmdb   = process("metainfo_tmdb", upstream=req, api_key=tmdb_key)
q      = process("quality",       upstream=tmdb, spec="1080p")
movies = process("movies",        upstream=q,
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
