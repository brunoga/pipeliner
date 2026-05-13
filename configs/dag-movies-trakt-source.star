# dag-movies-trakt-source.star
#
# Movie downloads driven by a Trakt watchlist, using trakt_list as a
# standalone DAG source node rather than inside movies.from.
#
# The DAG makes the topology explicit: the Trakt watchlist and the torrent
# RSS feed are both visible as source nodes. The movies processor filters
# the RSS entries by matching them against the Trakt titles.
#
# Replace client_id, access_token, feed URL, and qBittorrent host.

trakt_id     = env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID")
trakt_secret = env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET")  # enables automatic OAuth management

# Source 1: Trakt watchlist as a title list (emits entries with movie titles).
watchlist = input("trakt_list",
    client_id=trakt_id,
    client_secret=trakt_secret,
    type="movies",
    list="watchlist")

# Source 2: Torrent RSS feed.
rss_src = input("rss", url="https://feeds.example.com/movies/1080p")

# Merge: the movies processor uses trakt titles to filter RSS entries.
# In this pattern both sources flow into movies, which uses the trakt_list
# entries as the accepted title set and the rss entries as candidates.
all_src = merge(rss_src, watchlist)

seen   = process("seen",            upstream=all_src)
meta   = process("metainfo_quality", upstream=seen)
tmdb   = process("metainfo_tmdb",   upstream=meta, api_key=env("TMDB_KEY", default="YOUR_TMDB_KEY"))
movies = process("movies",          upstream=tmdb,
    quality="1080p+",
    list=[{"name": "trakt_list",
           "client_id": trakt_id,
           "client_secret": trakt_secret,
           "type": "movies",
           "list": "watchlist"}])
enrich_ok = process("require", upstream=movies, fields=["enriched"])
pathfmt   = process("pathfmt", upstream=enrich_ok,
    path="/media/movies/{title} ({video_year})",
    field="download_path")

output("qbittorrent", upstream=pathfmt,
    host="localhost",
    port=8080,
    savepath="{download_path}")

pipeline("movies-trakt", schedule="2h")
