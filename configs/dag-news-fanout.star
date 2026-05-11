# dag-news-fanout.star
#
# RSS news pipeline with fan-out: matching articles are sent to email AND
# saved to a persistent list for later review.
#
# This is the canonical "multiplexer" pattern: one processor output feeds
# two independent sinks. Each sink receives its own copy of the entries.
#
# Replace SMTP settings and feed URLs with your values.

smtp_host = "smtp.example.com"
smtp_port = 587
smtp_user = env("SMTP_USER")
smtp_pass = env("SMTP_PASS")
mail_to   = "you@example.com"

feed1 = input("rss", url="https://feeds.arstechnica.com/arstechnica/index")
feed2 = input("rss", url="https://www.wired.com/feed/rss")

all_news = merge(feed1, feed2)

seen     = process("seen",   from_=all_news, local=True)
filtered = process("regexp", from_=seen,
    accept=["(?i)linux|open.?source|security|AI|machine.learning"],
    reject=["(?i)advertisement|sponsor"])
accepted = process("accept_all", from_=filtered)

# Fan-out: both sinks receive the same filtered articles independently.
output("email", from_=accepted,
    smtp_host=smtp_host,
    smtp_port=smtp_port,
    username=smtp_user,
    password=smtp_pass,
    **{"from": smtp_user},
    to=mail_to,
    subject="Tech digest: {{len .Entries}} article(s)")

output("list_add", from_=accepted, list="tech_articles")

pipeline("tech-news-fanout", schedule="1h")
