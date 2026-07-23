# prune-ended-favorites.star
#
# List hygiene with series_status — both supported patterns:
#
# 1. TheTVDB favorites: the v4 API has no favorites-removal endpoint
#    (/user/favorites supports GET and POST only), so favorites cannot be
#    pruned remotely. The supported pattern filters still-running shows into
#    a local list ("active_favorites") that series/discover pipelines consume
#    via list_match or series.list instead of the raw favorites.
#
# 2. Trakt lists DO support removal, so the watchlist is pruned remotely:
#    ended/cancelled shows are removed with the trakt_list_update sink.
#
# Requirements: TVDB_API_KEY, TVDB_USER_PIN, TRAKT_CLIENT_ID,
# TRAKT_CLIENT_SECRET (run `pipeliner auth trakt` once to authorise),
# PUSHOVER_USER, PUSHOVER_TOKEN.

tvdb_api_key        = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
tvdb_user_pin       = env("TVDB_USER_PIN", default="YOUR_TVDB_PIN")
trakt_client_id     = env("TRAKT_CLIENT_ID", default="YOUR_TRAKT_ID")
trakt_client_secret = env("TRAKT_CLIENT_SECRET", default="YOUR_TRAKT_SECRET")
pushover_user       = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token      = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")

# ── Pipeline 1: mirror still-running TheTVDB favorites into a local list ─────
#
# tvdb_favorites surfaces series_status ("Continuing", "Ended", "Cancelled",
# "Upcoming"). Ended and cancelled shows are rejected; everything else is
# accepted into the persistent "active_favorites" list. Point series.list or
# list_match at that list instead of the raw favorites.

favs  = input("tvdb_favorites", api_key=tvdb_api_key, user_pin=tvdb_user_pin)
alive = process("condition", upstream=favs, rules=[
    {"reject": 'series_status == "Ended" or series_status == "Cancelled"'},
    {"accept": 'true'},
])
keep  = output("list_add", upstream=alive, list="active_favorites")
note  = output("notify", upstream=keep, via="pushover",
               config={"user": pushover_user, "token": pushover_token},
               body="Still following: {{.Title}}")
pipeline("sync-active-favorites", schedule="168h")

# ── Pipeline 2: prune ended shows from the Trakt watchlist ───────────────────
#
# Trakt status values are lowercase ("returning series", "ended", "canceled").
# Ended shows are accepted and then removed from the watchlist remotely.

watch = input("trakt_list", client_id=trakt_client_id,
              client_secret=trakt_client_secret,
              type="shows", list="watchlist")
done  = process("condition", upstream=watch, rules=[
    {"accept": 'series_status == "ended" or series_status == "canceled"'},
    {"reject": 'true'},
])
prune = output("trakt_list_update", upstream=done,
               client_id=trakt_client_id, client_secret=trakt_client_secret,
               list="watchlist", action="remove")
pipeline("prune-trakt-watchlist", schedule="168h")
