# ars-technica-email.star
#
# Fetches Ars Technica articles matching selected keywords and sends a digest
# email. Uses local: true on seen so each task run gets its own seen store
# (articles appear in every machine that runs this config independently).

smtp_host = "smtp.example.com"
smtp_port = 587
smtp_user = "user@example.com"
smtp_pass = "changeme"
mail_to   = "you@example.com"

task("ars-technica",
    [
        plugin("rss", url="https://feeds.arstechnica.com/arstechnica/index"),
        plugin("seen", local=True),
        plugin("regexp", accept=["(?i)(linux|open.?source|security|AI|machine.learning)"]),
        plugin("notify",
            via="email",
            title="Ars Technica: {{len .Entries}} new article(s)",
            body="{{range .Entries}}- {{.Title}}\n  {{.URL}}\n\n{{end}}",
            config={
                "smtp_host": smtp_host,
                "port":      smtp_port,
                "username":  smtp_user,
                "password":  smtp_pass,
                "from":      smtp_user,
                "to":        mail_to,
            }),
    ],
    schedule="30m")
