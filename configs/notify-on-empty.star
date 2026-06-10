# notify-on-empty.star
#
# Notify by email when an RSS feed returns no entries — useful for spotting
# feeds that broke, search terms that need adjusting, or releases that
# haven't come out yet.
#
# The trick is the report_empty processor on a fan-out branch from the
# source: it emits a single marker entry only when its upstream produced
# nothing, and emits nothing when the upstream actually returned results.
# The main path (transmission) reads from `src` directly and is unaffected.

smtp_host = "smtp.example.com"
smtp_port = 587
smtp_user = env("SMTP_USER", default="user@example.com")
smtp_pass = env("SMTP_PASS", default="changeme")
mail_to   = "you@example.com"
feed_url  = "https://feeds.example.com/my-show"

src  = input("rss",            url=feed_url)

# Main path — runs unchanged on whatever the feed returned.
meta = process("metainfo_file", upstream=src)
seen = process("seen",          upstream=meta)
output("transmission",          upstream=seen, host="localhost")

# Alert path — a fan-out branch off the source.
# report_empty emits a single marker entry only when src returned 0 entries,
# otherwise it emits nothing. No marker means notify gets called with an
# empty slice and silently does nothing — exactly the behavior we want when
# the feed had new items.
alert = process("report_empty", upstream=src,
                message="RSS feed returned no entries this run")

output("notify", upstream=alert,
       via="email",
       title="Pipeliner alert: empty feed run",
       body="{{range .Entries}}{{.Title}}{{end}}",
       config={
           "smtp_host": smtp_host,
           "smtp_port": smtp_port,
           "username":  smtp_user,
           "password":  smtp_pass,
           "sender":    smtp_user,
           "to":        mail_to,
       })

pipeline("notify-on-empty", schedule="1h")
