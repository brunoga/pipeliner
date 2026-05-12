# ══════════════════════════════════════════════════════════════════════════════
# personal.star — media automation
# ══════════════════════════════════════════════════════════════════════════════

# ── Infrastructure ────────────────────────────────────────────────────────────

jackett_url  = "https://jackett.bug-br.org.br"
jackett_key  = env("JACKETT_API_KEY",  default="i244yht9xxivm038znbun9l8e861i4ab")
deluge_host  = env("DELUGE_HOST",      default="mrshine.bug-br.org.br")
deluge_pass  = env("DELUGE_PASS",      default="changeme")

# ── API keys ──────────────────────────────────────────────────────────────────

tvdb_key     = env("TVDB_API_KEY",     default="c537f724-8410-4dab-96ba-aee4de474ab7")
tmdb_key     = env("TMDB_API_KEY",     default="ffb27dd65d888f3a034fd8fba5ff3263")
trakt_id     = env("TRAKT_CLIENT_ID",  default="8dea8550dc2024c52e9ebefeba1e915a8964d6a47845e1c8aab1041a37085802")
trakt_secret = env("TRAKT_SECRET",     default="9f450b18b16aa8c1df6a5ab9b8050548c2e70811f57bf09a99ae85fc2f43af42")

# ── Email transport ───────────────────────────────────────────────────────────

smtp_host    = "smtp.gmail.com"
smtp_user    = env("SMTP_USER", default="bruno.albuquerque@gmail.com")
smtp_pass    = env("SMTP_PASS", default="changeme")
smtp_to      = "bga@gmail.com"

# ══════════════════════════════════════════════════════════════════════════════
# Email body templates
# ══════════════════════════════════════════════════════════════════════════════

TV_SHOW_CARD = """
{{range .Entries}}
<div style="margin-bottom:2em;font-family:sans-serif">
  {{with index .Fields "video_poster"}}<img src="{{.}}" style="max-width:200px;float:left;margin-right:1em">{{end}}
  <h2>
    <a href="https://thetvdb.com/series/{{index .Fields "tvdb_slug"}}">{{index .Fields "title"}}</a>
    {{index .Fields "series_episode_id"}}
  </h2>
  <b>Network:</b> {{index .Fields "series_network"}}<br>
  <b>Language:</b> {{index .Fields "video_language"}}<br>
  <b>Country:</b>  {{index .Fields "video_country"}}<br>
  <b>Cast:</b>     {{join ", " (index .Fields "video_cast")}}<br>
  <b>Genres:</b>   {{join ", " (index .Fields "video_genres")}}<br>
  {{with index .Fields "series_first_air_date"}}<b>First aired:</b> {{formatdate "January 2, 2006" .}}<br>{{end}}
  <p>{{index .Fields "description"}}</p>
  {{with index .Fields "video_trailers"}}
    <b>Trailers:</b><br>{{range .}}<a href="{{.}}">▶ Trailer</a><br>{{end}}
  {{end}}
  <div style="clear:both"></div>
</div>
{{end}}"""

TV_EPISODE_CARD = """
{{range .Entries}}
<div style="margin-bottom:2em;font-family:sans-serif">
  {{with index .Fields "video_poster"}}<img src="{{.}}" style="max-width:200px;float:left;margin-right:1em">{{end}}
  <h2>
    <a href="https://thetvdb.com/series/{{index .Fields "tvdb_slug"}}">{{index .Fields "title"}}</a>
    — {{index .Fields "series_episode_id"}}
  </h2>
  {{with index .Fields "series_episode_title"}}<b>Episode:</b> {{.}}<br>{{end}}
  {{with index .Fields "series_episode_air_date"}}<b>Aired:</b> {{formatdate "January 2, 2006" .}}<br>{{end}}
  <b>Network:</b>  {{index .Fields "series_network"}}<br>
  <b>Language:</b> {{index .Fields "video_language"}}<br>
  {{with index .Fields "series_episode_description"}}<p>{{.}}</p>{{end}}
  {{with index .Fields "series_episode_image"}}<img src="{{.}}" style="max-width:400px;clear:left"><br>{{end}}
  <div style="clear:both"></div>
</div>
{{end}}"""

MOVIE_CARD = """
{{range .Entries}}
<div style="margin-bottom:2em;font-family:sans-serif">
  {{with index .Fields "video_poster"}}<img src="{{.}}" style="max-width:200px;float:left;margin-right:1em">{{end}}
  <h2>
    <a href="https://www.themoviedb.org/movie/{{index .Fields "tmdb_id"}}">{{index .Fields "title"}}</a>
    ({{index .Fields "video_year"}})
  </h2>
  <b>Quality:</b>        {{index .Fields "video_quality"}}<br>
  <b>Rating:</b>         {{index .Fields "video_rating"}} ({{index .Fields "video_votes"}} votes)<br>
  <b>Runtime:</b>        {{index .Fields "video_runtime"}} min<br>
  <b>Language:</b>       {{index .Fields "video_language"}}<br>
  <b>Country:</b>        {{index .Fields "video_country"}}<br>
  <b>Content rating:</b> {{index .Fields "video_content_rating"}}<br>
  <b>Genres:</b>         {{join ", " (index .Fields "video_genres")}}<br>
  <b>Cast:</b>           {{join ", " (index .Fields "video_cast")}}<br>
  {{with index .Fields "movie_tagline"}}<i>{{.}}</i><br>{{end}}
  <p>{{index .Fields "description"}}</p>
  {{with index .Fields "video_trailers"}}
    <b>Trailers:</b><br>{{range .}}<a href="{{.}}">▶ Trailer</a><br>{{end}}
  {{end}}
  <div style="clear:both"></div>
</div>
{{end}}"""

# ══════════════════════════════════════════════════════════════════════════════
# Helpers
#
# Processor helpers take an upstream node and return the new tail node.
# Sink helpers call output() as a side-effect and return nothing.
# ══════════════════════════════════════════════════════════════════════════════

def jackett_source(indexers, categories):
    src = input("jackett_input",
                url=jackett_url, api_key=jackett_key,
                indexers=indexers, categories=categories, limit=500)
    return process("torrent_alive", upstream=src, min_seeds=1)

def tvdb_enrich(up):
    m = process("metainfo_tvdb", upstream=up, api_key=tvdb_key, cache_ttl="12h")
    return process("require", upstream=m, fields=["enriched"])

def tmdb_enrich(up, cache_ttl):
    m = process("metainfo_tmdb", upstream=up, api_key=tmdb_key, cache_ttl=cache_ttl)
    return process("require", upstream=m, fields=["enriched"])

def tv_genre_filter(up):
    return process("condition", upstream=up, rules=[
        {"reject": 'video_language != "" and video_language != "English"'},
        {"reject": 'video_genres contains "Documentary"'},
        {"reject": 'video_genres contains "Reality"'},
        {"reject": 'video_genres contains "Game Show"'},
        {"reject": 'video_genres contains "Talk Show"'},
    ])

def content_check(up):
    # Most expensive step — fetch .torrent / resolve DHT. Always runs last.
    t = process("metainfo_torrent", upstream=up)
    m = process("metainfo_magnet",  upstream=t, resolve_timeout="1m")
    return process("content", upstream=m, reject=["*.rar", "*.iso", "*.exe"])

def trakt_watchlist(type, ttl):
    return [{"name":          "trakt_list",
             "client_id":     trakt_id,
             "client_secret": trakt_secret,
             "type":          type,
             "list":          "watchlist",
             "ttl":           ttl}]

def deluge_out(up, move_path):
    output("deluge", upstream=up,
           host=deluge_host, password=deluge_pass,
           path="", move_completed_path=move_path)

def email_out(up, subject, body_template):
    output("email", upstream=up,
           smtp_host=smtp_host, smtp_port=587,
           username=smtp_user, password=smtp_pass,
           to=smtp_to, html=True,
           subject=subject, body_template=body_template,
           **{"from": smtp_user})

# ══════════════════════════════════════════════════════════════════════════════
# Pipeline: tvshows-discover
#
# Finds series premieres (S01E01) across all indexed TV content. No explicit
# show list — anything TVDB recognises as a new English show is a candidate.
# The premiere filter records accepted shows; the same show will not be
# downloaded again on the next run.
# ══════════════════════════════════════════════════════════════════════════════

disc_src    = jackett_source(indexers=["torrenting", "showrss"], categories=["5000"])
disc_prem   = process("premiere", upstream=disc_src,
                       quality="720p+ webrip+")
disc_dd     = process("dedup", upstream=disc_prem)   # best copy per show when ties
disc_enrich = tvdb_enrich(disc_dd)
disc_genres = tv_genre_filter(disc_enrich)
disc_recent = process("condition", upstream=disc_genres,
                       reject='series_first_air_date != "" and series_first_air_date < daysago(365)')
disc_paths  = process("pathfmt", upstream=disc_recent,
                       path="/data/media/series/{title}/Season 01",
                       field="download_path")

deluge_out(disc_paths, "{download_path}")
email_out(disc_paths,
          subject="Pipeliner new premiere(s): {{len .Entries}} show(s)",
          body_template=TV_SHOW_CARD)

pipeline("tvshows-discover")

# ══════════════════════════════════════════════════════════════════════════════
# Pipeline: tvshows-favorites
#
# Downloads all episodes of shows in the TVDB favorites list, including
# backfill. tracking="follow" records each accepted episode so it is not
# downloaded again on subsequent runs.
# ══════════════════════════════════════════════════════════════════════════════

fav_src    = jackett_source(indexers=["torrenting", "showrss"], categories=["5000"])
fav_srs    = process("series", upstream=fav_src,
                      tracking="follow", quality="720p+ webrip+",
                      **{"from": [{"name":     "tvdb_favorites",
                                   "api_key":  tvdb_key,
                                   "user_pin": "J51VXRNPNUKY5J4U"}]})
fav_dd     = process("dedup", upstream=fav_srs)    # best copy per episode when ties
fav_enrich = tvdb_enrich(fav_dd)
fav_genres = tv_genre_filter(fav_enrich)
fav_paths  = process("pathfmt", upstream=fav_genres,
                      path="/data/media/series/{title}/Season {series_season:02d}",
                      field="download_path")

deluge_out(fav_paths, "{download_path}")
email_out(fav_paths,
          subject="Pipeliner downloaded {{len .Entries}} episode(s)",
          body_template=TV_EPISODE_CARD)

pipeline("tvshows-favorites")

# ══════════════════════════════════════════════════════════════════════════════
# Pipeline: movies-3d
#
# Downloads 3D movies from the Trakt watchlist. Tracked independently from
# the non-3D pipeline — having a 3D copy does not block downloading a flat
# copy, and vice versa.
# ══════════════════════════════════════════════════════════════════════════════

m3d_src     = jackett_source(indexers=["torrenting", "3dtorrents", "therarbg", "yts"],
                              categories=["2000"])
m3d_movs    = process("movies", upstream=m3d_src,
                        quality="3dfull",
                        **{"from": trakt_watchlist("movies", "1h")})
m3d_dd      = process("dedup",   upstream=m3d_movs) # best 3D format (BD3D > Full > Half)
m3d_enrich  = tmdb_enrich(m3d_dd, cache_ttl="24h")
m3d_content = content_check(m3d_enrich)
m3d_paths   = process("pathfmt", upstream=m3d_content,
                        path="/data/media/3dmovies/{title} ({video_year}) 3D",
                        field="move_completed")

deluge_out(m3d_paths, "{move_completed}")
email_out(m3d_paths,
          subject="Pipeliner 3D movie(s): {{len .Entries}} downloaded",
          body_template=MOVIE_CARD)

pipeline("movies-3d")

# ══════════════════════════════════════════════════════════════════════════════
# Pipeline: movies
#
# Downloads non-3D movies from the Trakt watchlist at 1080p+. 3D releases are
# rejected before the movies filter so they are never recorded here, keeping
# the two trackers fully independent.
# ══════════════════════════════════════════════════════════════════════════════

mov_src     = jackett_source(indexers=["torrenting", "therarbg", "yts"],
                              categories=["2000"])
mov_qual    = process("metainfo_quality", upstream=mov_src)
mov_no3d    = process("condition",        upstream=mov_qual, reject="video_is_3d == true")
mov_movs    = process("movies", upstream=mov_no3d,
                        quality="1080p+",
                        **{"from": trakt_watchlist("movies", "1h")})
mov_dd      = process("dedup",   upstream=mov_movs) # best 1080p source (Blu-ray > WEB > etc.)
mov_enrich  = tmdb_enrich(mov_dd, cache_ttl="48h")
mov_content = content_check(mov_enrich)
mov_paths   = process("pathfmt", upstream=mov_content,
                        path="/data/media/movies/{title} ({video_year})",
                        field="move_completed")

deluge_out(mov_paths, "{move_completed}")
email_out(mov_paths,
          subject="Pipeliner movie(s): {{len .Entries}} downloaded",
          body_template=MOVIE_CARD)

pipeline("movies")
