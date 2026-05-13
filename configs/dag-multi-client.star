# dag-multi-client.star
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
meta = process("metainfo_quality", upstream=seen)

# --- TV branch ---
series  = process("series",  upstream=meta,
    static=["Breaking Bad", "Severance"],
    tracking="strict", quality="720p+")
tv_dedup = process("dedup",  upstream=series)
tv_path  = process("pathfmt", upstream=tv_dedup,
    path="/media/tv/{title}/Season {series_season:02d}",
    field="download_path")
output("transmission", upstream=tv_path,
    host=trans_host, port=9091,
    path="{download_path}")

# --- Movie branch ---
movies    = process("movies",  upstream=meta,
    static=["Dune Part Two", "Oppenheimer"],
    quality="1080p+")
movie_path = process("pathfmt", upstream=movies,
    path="/media/movies/{title}",
    field="download_path")
output("qbittorrent", upstream=movie_path,
    host=qbit_host, port=8080,
    savepath="{download_path}")

pipeline("multi-client", schedule="30m")
