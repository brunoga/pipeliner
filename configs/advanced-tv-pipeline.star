# advanced-tv-pipeline.star
#
# A production-quality TV pipeline combining:
#   - Trakt watchlist as the dynamic show list (updated automatically)
#   - TVDB enrichment for canonical titles, episode metadata, ratings
#   - require(enriched) to drop unrecognised entries early
#   - condition to filter out content by rating category
#   - upgrade to track and improve quality over time
#   - dedup to pick the best copy from multiple variants in one run
#   - pathfmt with scrub for filesystem-safe paths
#   - notify/webhook for a per-run Discord summary
#
# Prerequisites:
#   pipeliner auth trakt --client-id ID --client-secret SECRET
#   TVDB_API_KEY, TRAKT_CLIENT_ID, DISCORD_WEBHOOK_URL in environment.

tvdb_key    = env("TVDB_API_KEY",          default="YOUR_TVDB_KEY")
trakt_id    = env("TRAKT_CLIENT_ID",       default="YOUR_TRAKT_CLIENT_ID")
discord_url = env("DISCORD_WEBHOOK_URL",   default="https://discord.com/api/webhooks/YOUR_WEBHOOK")
trans_host  = "localhost"
tv_path     = "/media/tv"

src  = input("rss", url="https://feeds.example.com/all")
seen = process("seen", upstream=src)

# ── Enrichment phase ────────────────────────────────────────────────────────
q    = process("metainfo_quality", upstream=seen)
ep   = process("metainfo_series",  upstream=q)
tvdb = process("metainfo_tvdb",    upstream=ep, api_key=tvdb_key)

# Drop entries that could not be identified by TVDB.
req  = process("require", upstream=tvdb, fields=["enriched"])

# Skip entries rated for adult content.
flt  = process("condition", upstream=req,
    reject="video_content_rating == 'TV-MA'")

# ── Series matching ─────────────────────────────────────────────────────────
# Dynamic list: Trakt watchlist is fetched and cached (default ttl=1h).
# Static fallback list supplements the watchlist.
series = process("series", upstream=flt,
    list=[{"name": "trakt_list", "client_id": trakt_id,
           "type": "shows",      "list":      "watchlist"}],
    static=["Severance"],
    tracking="strict", quality="720p+",
    ttl="2h")

# ── Quality upgrade ─────────────────────────────────────────────────────────
# Accept the entry only if it improves on the quality already downloaded.
# Target ceiling: 1080p BluRay — stop upgrading once we have that.
upg  = process("upgrade", upstream=series,
    target="1080p bluray")

# ── Dedup and output ────────────────────────────────────────────────────────
best = process("dedup",   upstream=upg)
fmt  = process("pathfmt", upstream=best,
    path=tv_path + "/{{.title | scrub}}/Season {{printf \"%02d\" .series_season}}",
    field="download_path")

output("transmission", upstream=fmt,
    host=trans_host, path="{download_path}")

output("notify", upstream=fmt,
    via="webhook",
    config={"url": discord_url},
    title="pipeliner — {{len .Entries}} episode(s)",
    body="{{range .Entries}}- {{.Title}}\n{{end}}")

pipeline("advanced-tv", schedule="1h")
