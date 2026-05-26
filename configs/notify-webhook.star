# notify-webhook.star
#
# Downloads TV episodes and sends a webhook notification (e.g. Discord, Slack,
# Gotify) summarising accepted entries after each run.
#
# The notify sink fires once per run, not once per entry. It only sends when
# at least one entry was accepted (set on="all" to always notify).
#
# The body template has access to:
#   .Entries   — list of accepted Entry values (.Title, .URL, .Fields)
#   .Task      — pipeline name
#   .Accepted  — count of accepted entries

discord_url = env("DISCORD_WEBHOOK_URL", default="https://discord.com/api/webhooks/YOUR_WEBHOOK")
trans_host  = "localhost"
tv_path     = "/media/tv"

src    = input("rss",               url="https://feeds.example.com/all")
seen   = process("seen",            upstream=src)
q      = process("metainfo_file", upstream=seen)
series = process("series",          upstream=q,
    static=["Breaking Bad", "Severance"],
    tracking="strict", quality="720p+")
best   = process("dedup",           upstream=series)
fmt    = process("pathfmt",         upstream=best,
    path=tv_path + "/{title}/Season {series_season:02d}",
    field="download_path")

output("transmission", upstream=fmt,
    host=trans_host, path="{download_path}")

# notify fires once per run with a summary of all accepted entries.
output("notify", upstream=fmt,
    via="webhook",
    config={"url": discord_url},
    title="pipeliner: {{len .Entries}} episode(s) queued",
    body="{{range .Entries}}- {{.Title}}\n{{end}}")

pipeline("tv-with-notify", schedule="1h")
