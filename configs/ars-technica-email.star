# ars-technica-email.star
#
# Fetches Ars Technica articles matching selected keywords and sends a digest
# email. Uses local=True on seen so each machine running this config has its
# own seen store — articles appear independently on every machine.

smtp_host = "smtp.example.com"
smtp_port = 587
smtp_user = env("SMTP_USER", default="user@example.com")
smtp_pass = env("SMTP_PASS", default="changeme")
mail_to   = "you@example.com"

src      = input("rss", url="https://feeds.arstechnica.com/arstechnica/index")
seen     = process("seen",   upstream=src, local=True)
filtered = process("regexp", upstream=seen,
                   accept=["(?i)(linux|open.?source|security|AI|machine.learning)"])
accepted = process("accept_all", upstream=filtered)
output("notify", upstream=accepted,
       via="email",
       title="Ars Technica: {{len .Entries}} new article(s)",
       body="{{range .Entries}}- {{.Title}}\n  {{.URL}}\n\n{{end}}",
       config={
           "smtp_host": smtp_host,
           "smtp_port": smtp_port,
           "username":  smtp_user,
           "password":  smtp_pass,
           "from":      smtp_user,
           "to":        mail_to,
       })

pipeline("ars-technica", schedule="30m")
