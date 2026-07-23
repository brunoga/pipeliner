# prune-ended-favorites.star
#
# List hygiene with series_status — both supported patterns:
#
# 1. TheTVDB favorites: the v4 API has no favorites-removal endpoint
#    (/user/favorites supports GET and POST only), so with v4 credentials
#    alone favorites cannot be pruned remotely. The primary pattern filters
#    still-running shows into a local list ("active_favorites") that
#    series/discover pipelines consume via list_match or series.list instead
#    of the raw favorites. (Direct removal IS possible via the legacy v3
#    API — see the commented-out alternative below Pipeline 1.)
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

# ── Alternative: remove ended shows from the favorites directly ──────────────
#
# tvdb_favorites_add can remove favorites via TheTVDB's legacy v3 API. It
# needs the legacy credential pair — the "unique ID" (userkey) and username
# from the thetvdb.com account dashboard (Account → Edit Information) — in
# addition to the v4 api_key/user_pin. Uncomment and set TVDB_LEGACY_USER_KEY
# and TVDB_LEGACY_USER_NAME to prune the favorites remotely:
#
# tvdb_legacy_user_key  = env("TVDB_LEGACY_USER_KEY", default="YOUR_V3_USERKEY")
# tvdb_legacy_user_name = env("TVDB_LEGACY_USER_NAME", default="YOUR_V3_USERNAME")
#
# favs2 = input("tvdb_favorites", api_key=tvdb_api_key, user_pin=tvdb_user_pin)
# ended = process("condition", upstream=favs2, rules=[
#     {"accept": 'series_status == "Ended" or series_status == "Cancelled"'},
#     {"reject": 'true'},
# ])
# gone  = output("tvdb_favorites_add", upstream=ended, action="remove",
#                api_key=tvdb_api_key, user_pin=tvdb_user_pin,
#                legacy_user_key=tvdb_legacy_user_key,
#                legacy_user_name=tvdb_legacy_user_name)
# pipeline("prune-tvdb-favorites", schedule="168h")

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
