# notification-action-link.star
#
# Remote automation: put a one-click button in a notification email that acts
# on the item when you click it — "follow this series" without visiting TheTVDB,
# logging in, and hunting for the favourites button.
#
# A mail client can only produce a GET with no custom headers, so it cannot use
# the ingest endpoint's bearer token or a browser session. The signedaction
# template helper instead mints a link that carries its own HMAC signature: it
# authorises exactly the one action it was minted for, and clicking it opens a
# confirmation page (mail providers prefetch links, so the GET never acts).
# Confirming pushes the item onto an ingest queue and triggers the pipeline
# below, which does the work.
#
# 1. Start the daemon with a public URL and an ingest token:
#        PIPELINER_PUBLIC_URL=https://pipeliner.example.com \
#        PIPELINER_INGEST_TOKEN=secret \
#        pipeliner daemon --config notification-action-link.star --web :8080 ...
#    The token doubles as the signing secret; without both, links render as an
#    empty string and the template stays valid.
#
# Requirements: PIPELINER_PUBLIC_URL, PIPELINER_INGEST_TOKEN, TVDB_API_KEY,
# TVDB_USER_PIN, plus SMTP settings for the notification itself.

tvdb_key  = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")
tvdb_pin  = env("TVDB_USER_PIN", default="YOUR_TVDB_PIN")

SMTP = {
    "smtp_host": env("SMTP_HOST", default="smtp.example.com"),
    "smtp_port": 587,
    "username":  env("SMTP_USER", default="you@example.com"),
    "password":  env("SMTP_PASSWORD", default="app-password"),
    "sender":    env("SMTP_USER", default="you@example.com"),
    "to":        [env("NOTIFY_TO", default="you@example.com")],
    "html":      True,
}

# ── The pipeline the link drives ─────────────────────────────────────────────
# Drains whatever the confirmation page pushed and adds it to TheTVDB
# favourites. No schedule: it runs only when a link is confirmed.
requests = input("webhook", queue="favorites")
valid    = process("require", upstream=requests, fields=["tvdb_id"])
output("tvdb_favorites_add", upstream=valid, api_key=tvdb_key, user_pin=tvdb_pin)
pipeline("tvshows-favorite-add")

# ── A premiere notification carrying the link ────────────────────────────────
# signedaction takes the queue, the pipeline to trigger, the button label, the
# item title, then field/value pairs that become entry fields on the pushed
# item — here the tvdb_id that tvdb_favorites_add requires.
src      = input("rss", url=env("TV_FEED", default="https://example.com/tv.rss"))
meta     = process("metainfo_file", upstream=src)
enriched = process("metainfo_tvdb", upstream=meta, api_key=tvdb_key)
fresh    = process("require", upstream=enriched, fields=["enriched", "tvdb_id", "_quality"])
new      = process("premiere", upstream=fresh)

output("notify", upstream=new, via="email", config=SMTP,
       title="New series premiere: {{len .Entries}}",
       body="""{{range .Entries}}
  <h2>{{index .Fields "title"}}</h2>
  <p>{{index .Fields "description"}}</p>
  <p><a href="{{signedaction "favorites" "tvshows-favorite-add" "⭐ Follow this series" (index .Fields "title") "tvdb_id" (index .Fields "tvdb_id")}}">⭐ Follow this series</a></p>
{{end}}""")
pipeline("tvshows-premiere", schedule="1h")
