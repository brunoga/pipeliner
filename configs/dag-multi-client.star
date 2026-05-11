# dag-multi-client.star
#
# Send TV episodes to Transmission and movies to qBittorrent from the same
# RSS feed, using two independent processor branches after a shared seen/dedup
# stage.
#
# This pattern is impossible in the linear engine (a single task can only
# have one pipeline path). In the DAG model, after the shared head, the
# graph branches: the series branch and the movies branch are independent
# processor chains that both feed their own sink.

rss_url    = "https://feeds.example.com/all"
trans_host = "localhost"
qbit_host  = "localhost"

src  = input("rss",             url=rss_url)
seen = process("seen",          from_=src)
meta = process("metainfo_quality", from_=seen)

# --- TV branch ---
series  = process("series",  from_=meta,
    static=["Breaking Bad", "Severance"],
    tracking="strict", quality="720p+")
tv_dedup = process("dedup",  from_=series)
tv_path  = process("pathfmt", from_=tv_dedup,
    path="/media/tv/{title}/Season {series_season:02d}",
    field="download_path")
output("transmission", from_=tv_path,
    host=trans_host, port=9091,
    path="{download_path}")

# --- Movie branch ---
movies    = process("movies",  from_=meta,
    static=["Dune Part Two", "Oppenheimer"],
    quality="1080p+")
movie_path = process("pathfmt", from_=movies,
    path="/media/movies/{title}",
    field="download_path")
output("qbittorrent", from_=movie_path,
    host=qbit_host, port=8080,
    savepath="{download_path}")

pipeline("multi-client", schedule="30m")
