# multi-client.star
#
# Send TV episodes to Transmission and movies to qBittorrent from the same
# RSS feed, using two independent processor branches after a shared seen/dedup
# stage.
#
# After the shared head the graph branches: the series branch and the movies
# branch are independent processor chains that each feed their own sink.

rss_url    = "https://feeds.example.com/all"
trans_host = "localhost"
qbit_host  = "localhost"

src  = input("rss",             url=rss_url)
seen = process("seen",          upstream=src)
meta = process("metainfo_file", upstream=seen)

# --- TV branch ---
tv_req   = process("require", upstream=meta,
    fields=["title", "series_episode_id", "series_season",
            "series_episode", "_quality"])
tv_q     = process("quality", upstream=tv_req, spec="720p+")
series  = process("series",   upstream=tv_q,
    static=["Breaking Bad", "Severance"],
    tracking="strict")
tv_dedup = process("dedup",   upstream=series)
tv_path  = process("pathfmt", upstream=tv_dedup,
    path="/media/tv/{title}/Season {series_season:02d}",
    field="download_path")
output("transmission", upstream=tv_path,
    host=trans_host, port=9091,
    path="{download_path}")

# --- Movie branch ---
movie_req = process("require", upstream=meta,
    fields=["title", "video_year", "_quality"])
movie_q   = process("quality", upstream=movie_req, spec="1080p+")
movies    = process("movies",  upstream=movie_q,
    static=["Dune Part Two", "Oppenheimer"])
movie_path = process("pathfmt", upstream=movies,
    path="/media/movies/{title}",
    field="download_path")
output("qbittorrent", upstream=movie_path,
    host=qbit_host, port=8080,
    savepath="{download_path}")

pipeline("multi-client", schedule="30m")
