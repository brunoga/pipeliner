# pipeline-triggers.star
#
# Pipeline triggers: pipeline("b", after="a") runs b when a finishes a
# successful (non-dry) run; after="a:accepted" runs it only when a accepted
# at least one entry. Cycles and unknown references are rejected by
# `pipeliner check`. Dry runs never cascade.
#
# Here: a sync pipeline maintains a local list, and a notify pipeline runs
# only when the sync actually accepted something new.

tvdb_api_key   = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")

favs  = input("tvdb_favorites", api_key=tvdb_api_key, user_pin=env("TVDB_USER_PIN", default="PIN"))
alive = process("condition", upstream=favs,
                reject='series_status == "Ended" or series_status == "Cancelled"')
keep  = output("list_add", upstream=alive, list="active_favorites")
pipeline("sync-favorites", schedule="24h")

src  = input("series_tracker")
life = process("series_lifecycle", upstream=src, api_key=tvdb_api_key)
done = process("condition", upstream=life, accept='series_lifecycle == "complete"')
note = output("notify", upstream=done, via="pushover",
              config={"user": pushover_user, "token": pushover_token},
              body="Series complete: {{.Title}}")
pipeline("completion-check", after="sync-favorites:accepted")
