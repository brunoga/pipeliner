# email-report-layout.star
#
# How to give every notification the same look without copy-pasting the
# markup into each pipeline.
#
# The problem this solves: a rich HTML email body is ~1.5 KB of markup, and
# a setup with four download pipelines ends up with four copies of it. They
# drift, and a styling change means four edits — with the same anchor text
# in each, so a careless search-and-replace hits the wrong one.
#
# The fix is to define the frame once as top-level fragments and compose
# each report from them. Only the middle of a card differs: a movie has
# rating/runtime/genres, an episode has season/network/air date, a premiere
# adds a one-click Follow button. Header bar, card frame, stat table, pills
# and footer are shared.
#
# Keeping the bodies in top-level variables matters for a second reason:
# the visual editor preserves a variable *reference* on a node, so a visual
# save leaves one definition intact instead of flattening 1.5 KB of markup
# onto four separate nodes.
#
# Requirements: SMTP_HOST/SMTP_USER/SMTP_PASSWORD/NOTIFY_TO, TMDB_API_KEY,
# TVDB_API_KEY. Set PIPELINER_BASE_URL and PIPELINER_ACTION_KEY for the
# premiere Follow button (see notification-action-link.star).

SMTP = {
    "smtp_host": env("SMTP_HOST", default="smtp.example.com"),
    "smtp_port": 587,
    "username":  env("SMTP_USER", default="you@example.com"),
    "password":  env("SMTP_PASSWORD", default="YOUR_SMTP_PASSWORD"),
    "sender":    env("SMTP_USER", default="you@example.com"),
    "to":        env("NOTIFY_TO", default="you@example.com"),
    "html":      True,
}

TMDB_API_KEY = env("TMDB_API_KEY", default="YOUR_TMDB_KEY")
TVDB_API_KEY = env("TVDB_API_KEY", default="YOUR_TVDB_KEY")

# ── Shared email chrome ──────────────────────────────────────────────────
#
# Every report below is built from these fragments, so the frame is defined
# once. Mail clients only render inline styles and table layout reliably —
# no stylesheet, no flexbox, no external CSS — and a poster sits in its own
# table cell rather than floating, which Outlook drops.
#
# Composition is plain string concatenation. Starlark's % and .format would
# both collide with template syntax ({{...}} and the "%" in printf verbs and
# CSS widths), and a def helper risks being picked up as a pipeline function.

EMAIL_PAGE_OPEN = """<div style="margin:0;padding:24px 12px;background:#f3f4f6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0" align="center" width="640" style="width:100%;max-width:640px;margin:0 auto;border-collapse:collapse;">"""

EMAIL_PAGE_CLOSE = """</table>
</div>"""

EMAIL_HEAD_OPEN = """<tr><td style="background:#1f2937;border-radius:10px 10px 0 0;padding:18px 22px;">
<div style="color:#f9fafb;font-size:17px;font-weight:600;line-height:1.3;">"""

EMAIL_HEAD_MID = """</div>
<div style="color:#9ca3af;font-size:13px;padding-top:4px;line-height:1.4;">"""

EMAIL_HEAD_CLOSE = """</div>
</td></tr>"""

EMAIL_CARD_OPEN = """<tr><td style="background:#ffffff;border-left:1px solid #e5e7eb;border-right:1px solid #e5e7eb;border-bottom:1px solid #e5e7eb;padding:18px 22px;">"""

EMAIL_CARD_CLOSE = """</td></tr>"""

EMAIL_FOOT_OPEN = """<tr><td style="background:#ffffff;border:1px solid #e5e7eb;border-top:none;border-radius:0 0 10px 10px;padding:14px 22px;color:#6b7280;font-size:12px;line-height:1.6;">"""

EMAIL_FOOT_CLOSE = """</td></tr>"""

# The middle of each card — the only part that differs per report.

MOVIE_CARD = """<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;">
<tr>
{{with index .Fields "video_poster"}}<td width="110" style="width:110px;padding:0 14px 0 0;vertical-align:top;">
<img src="{{.}}" width="110" style="width:110px;max-width:110px;display:block;border-radius:6px;" alt="">
</td>{{end}}
<td style="vertical-align:top;">
<div style="font-size:15px;font-weight:600;color:#111827;line-height:1.4;word-break:break-word;">{{with index .Fields "tmdb_id"}}<a href="https://www.themoviedb.org/movie/{{.}}" style="color:#1d4ed8;text-decoration:none;">{{index $e.Fields "title"}}</a>{{else}}{{index .Fields "title"}}{{end}} <span style="color:#6b7280;font-weight:400;">({{index .Fields "video_year"}})</span></div><div style="color:#9ca3af;font-size:11px;line-height:1.5;padding-top:5px;word-break:break-all;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;">{{.Title}}</div><div style="padding-top:9px;">{{with index .Fields "video_quality"}}<span style="background:#dbeafe;color:#1e40af;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;display:inline-block;margin:0 4px 4px 0;">{{.}}</span>{{end}}{{with index .Fields "source"}}<span style="background:#f3f4f6;color:#374151;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;display:inline-block;margin:0 4px 4px 0;">{{.}}</span>{{end}}{{with index .Fields "video_content_rating"}}<span style="background:#f3f4f6;color:#374151;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;display:inline-block;margin:0 4px 4px 0;">{{.}}</span>{{end}}</div>{{with index .Fields "movie_tagline"}}<div style="color:#6b7280;font-size:13px;font-style:italic;line-height:1.5;padding-top:10px;">{{.}}</div>{{end}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;margin-top:12px;font-size:13px;line-height:1.5;">{{with index .Fields "video_rating"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Rating</td><td style="padding:4px 0;color:#111827;">{{.}}{{with index $e.Fields "video_votes"}} <span style="color:#6b7280;">({{.}} votes)</span>{{end}}</td></tr>{{end}}{{with index .Fields "video_runtime"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Runtime</td><td style="padding:4px 0;color:#111827;">{{.}} min</td></tr>{{end}}{{with index .Fields "video_genres"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Genres</td><td style="padding:4px 0;color:#111827;">{{join ", " .}}</td></tr>{{end}}{{with index .Fields "video_cast"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Cast</td><td style="padding:4px 0;color:#111827;">{{join ", " .}}</td></tr>{{end}}{{with index .Fields "video_language"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Language</td><td style="padding:4px 0;color:#111827;">{{.}}{{with index $e.Fields "video_country"}} &middot; {{.}}{{end}}</td></tr>{{end}}{{with index .Fields "torrent_file_size"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Size</td><td style="padding:4px 0;color:#111827;">{{filesize .}}</td></tr>{{end}}</table>{{with index .Fields "description"}}<div style="color:#6b7280;font-size:13px;line-height:1.55;padding-top:10px;">{{.}}</div>{{end}}{{with index .Fields "video_trailers"}}<div style="padding-top:10px;font-size:13px;">{{range .}}<a href="{{.}}" style="color:#1d4ed8;text-decoration:none;">&#9654; Trailer</a><br>{{end}}</div>{{end}}{{with .AcceptReason}}<div style="margin-top:12px;padding-top:10px;border-top:1px solid #f3f4f6;color:#9ca3af;font-size:11px;line-height:1.5;">{{.}}</div>{{end}}
</td>
</tr>
</table>"""

EPISODE_CARD = """<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;">
<tr>
{{with index .Fields "video_poster"}}<td width="110" style="width:110px;padding:0 14px 0 0;vertical-align:top;">
<img src="{{.}}" width="110" style="width:110px;max-width:110px;display:block;border-radius:6px;" alt="">
</td>{{end}}
<td style="vertical-align:top;">
<div style="font-size:15px;font-weight:600;color:#111827;line-height:1.4;word-break:break-word;">{{with index .Fields "tvdb_slug"}}<a href="https://thetvdb.com/series/{{.}}" style="color:#1d4ed8;text-decoration:none;">{{index $e.Fields "title"}}</a>{{else}}{{index .Fields "title"}}{{end}} <span style="color:#6b7280;font-weight:400;">{{index .Fields "series_episode_id"}}</span></div><div style="color:#9ca3af;font-size:11px;line-height:1.5;padding-top:5px;word-break:break-all;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;">{{.Title}}</div><div style="padding-top:9px;">{{with index .Fields "video_quality"}}<span style="background:#dbeafe;color:#1e40af;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;display:inline-block;margin:0 4px 4px 0;">{{.}}</span>{{end}}{{with index .Fields "source"}}<span style="background:#f3f4f6;color:#374151;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;display:inline-block;margin:0 4px 4px 0;">{{.}}</span>{{end}}</div><table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;margin-top:12px;font-size:13px;line-height:1.5;">{{with index .Fields "series_episode_title"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Episode</td><td style="padding:4px 0;color:#111827;">{{.}}</td></tr>{{end}}{{with index .Fields "series_episode_air_date"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Aired</td><td style="padding:4px 0;color:#111827;">{{formatdate "January 2, 2006" .}}</td></tr>{{end}}{{with index .Fields "series_network"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Network</td><td style="padding:4px 0;color:#111827;">{{.}}</td></tr>{{end}}{{with index .Fields "video_language"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Language</td><td style="padding:4px 0;color:#111827;">{{.}}</td></tr>{{end}}{{with index .Fields "torrent_file_size"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Size</td><td style="padding:4px 0;color:#111827;">{{filesize .}}</td></tr>{{end}}</table>{{with index .Fields "series_episode_description"}}<div style="color:#6b7280;font-size:13px;line-height:1.55;padding-top:10px;">{{.}}</div>{{end}}{{with .AcceptReason}}<div style="margin-top:12px;padding-top:10px;border-top:1px solid #f3f4f6;color:#9ca3af;font-size:11px;line-height:1.5;">{{.}}</div>{{end}}
</td>
</tr>
</table>"""

PREMIERE_CARD = """<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;">
<tr>
{{with index .Fields "video_poster"}}<td width="110" style="width:110px;padding:0 14px 0 0;vertical-align:top;">
<img src="{{.}}" width="110" style="width:110px;max-width:110px;display:block;border-radius:6px;" alt="">
</td>{{end}}
<td style="vertical-align:top;">
<div style="font-size:15px;font-weight:600;color:#111827;line-height:1.4;word-break:break-word;">{{with index .Fields "tvdb_slug"}}<a href="https://thetvdb.com/series/{{.}}" style="color:#1d4ed8;text-decoration:none;">{{index $e.Fields "title"}}</a>{{else}}{{index .Fields "title"}}{{end}} <span style="color:#6b7280;font-weight:400;">{{index .Fields "series_episode_id"}}</span></div><div style="padding-top:10px;">{{$u := signedaction "favorites" "tvshows-favorite-add" "Follow" (index .Fields "title") "tvdb_id" (index .Fields "tvdb_id")}}{{if $u}}<a href="{{$u}}" style="background:#1d4ed8;color:#ffffff;font-size:13px;font-weight:600;text-decoration:none;padding:8px 16px;border-radius:6px;display:inline-block;">&#9733; Add to favorites</a>{{else}}<span style="color:#6b7280;font-size:12px;">Automatically added</span>{{end}}</div><div style="color:#9ca3af;font-size:11px;line-height:1.5;padding-top:5px;word-break:break-all;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;">{{.Title}}</div><div style="padding-top:9px;">{{with index .Fields "video_quality"}}<span style="background:#dbeafe;color:#1e40af;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;display:inline-block;margin:0 4px 4px 0;">{{.}}</span>{{end}}{{with index .Fields "source"}}<span style="background:#f3f4f6;color:#374151;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;display:inline-block;margin:0 4px 4px 0;">{{.}}</span>{{end}}</div><table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;margin-top:12px;font-size:13px;line-height:1.5;">{{with index .Fields "series_episode_title"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Episode</td><td style="padding:4px 0;color:#111827;">{{.}}</td></tr>{{end}}{{with index .Fields "series_episode_air_date"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Aired</td><td style="padding:4px 0;color:#111827;">{{formatdate "January 2, 2006" .}}</td></tr>{{end}}{{with index .Fields "series_network"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Network</td><td style="padding:4px 0;color:#111827;">{{.}}</td></tr>{{end}}{{with index .Fields "video_language"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Language</td><td style="padding:4px 0;color:#111827;">{{.}}</td></tr>{{end}}{{with index .Fields "torrent_file_size"}}<tr><td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;vertical-align:top;">Size</td><td style="padding:4px 0;color:#111827;">{{filesize .}}</td></tr>{{end}}</table>{{with index .Fields "series_episode_description"}}<div style="color:#6b7280;font-size:13px;line-height:1.55;padding-top:10px;">{{.}}</div>{{end}}{{with .AcceptReason}}<div style="margin-top:12px;padding-top:10px;border-top:1px solid #f3f4f6;color:#9ca3af;font-size:11px;line-height:1.5;">{{.}}</div>{{end}}
</td>
</tr>
</table>"""

# Assembled reports. Point a notify body= at one of these.

MOVIE_REPORT = (
    EMAIL_PAGE_OPEN
    + EMAIL_HEAD_OPEN + "New movies"
    + EMAIL_HEAD_MID + "{{len .Entries}} movie{{if ne (len .Entries) 1}}s{{end}} sent to the download client"
    + EMAIL_HEAD_CLOSE
    + "{{range .Entries}}" + EMAIL_CARD_OPEN + "{{$e := .}}"
    + MOVIE_CARD
    + EMAIL_CARD_CLOSE + "{{end}}"
    + EMAIL_FOOT_OPEN + "Sent by pipeliner."
    + EMAIL_FOOT_CLOSE
    + EMAIL_PAGE_CLOSE
)

EPISODE_REPORT = (
    EMAIL_PAGE_OPEN
    + EMAIL_HEAD_OPEN + "New episodes"
    + EMAIL_HEAD_MID + "{{len .Entries}} episode{{if ne (len .Entries) 1}}s{{end}} sent to the download client"
    + EMAIL_HEAD_CLOSE
    + "{{range .Entries}}" + EMAIL_CARD_OPEN + "{{$e := .}}"
    + EPISODE_CARD
    + EMAIL_CARD_CLOSE + "{{end}}"
    + EMAIL_FOOT_OPEN + "Sent by pipeliner."
    + EMAIL_FOOT_CLOSE
    + EMAIL_PAGE_CLOSE
)

PREMIERE_REPORT = (
    EMAIL_PAGE_OPEN
    + EMAIL_HEAD_OPEN + "Series premieres"
    + EMAIL_HEAD_MID + "{{len .Entries}} premiere{{if ne (len .Entries) 1}}s{{end}} airing soon"
    + EMAIL_HEAD_CLOSE
    + "{{range .Entries}}" + EMAIL_CARD_OPEN + "{{$e := .}}"
    + PREMIERE_CARD
    + EMAIL_CARD_CLOSE + "{{end}}"
    + EMAIL_FOOT_OPEN + "Following a series adds it to your TVDb favorites, which the download pipelines track."
    + EMAIL_FOOT_CLOSE
    + EMAIL_PAGE_CLOSE
)


# ── Pipeline 1: movie downloads ──────────────────────────────────────────

m_src  = input("rss", url=env("MOVIE_FEED", default="https://feeds.example.com/movies"))
m_meta = process("metainfo_file", upstream=m_src)
m_req  = process("require", upstream=m_meta, fields=["title", "video_year"])
m_tmdb = process("metainfo_tmdb", upstream=m_req, api_key=TMDB_API_KEY)
output("notify", upstream=m_tmdb, via="email", config=SMTP,
       title="Downloaded {{len .Entries}} movie{{if ne (len .Entries) 1}}s{{end}}",
       body=MOVIE_REPORT)

pipeline("movie-downloads", schedule="1h")

# ── Pipeline 2: episode downloads ────────────────────────────────────────

e_src  = input("rss", url=env("TV_FEED", default="https://feeds.example.com/tv"))
e_meta = process("metainfo_file", upstream=e_src)
e_req  = process("require", upstream=e_meta, fields=["title", "series_episode_id"])
e_tvdb = process("metainfo_tvdb", upstream=e_req, api_key=TVDB_API_KEY)
output("notify", upstream=e_tvdb, via="email", config=SMTP,
       title="Downloaded {{len .Entries}} episode{{if ne (len .Entries) 1}}s{{end}}",
       body=EPISODE_REPORT)

pipeline("episode-downloads", schedule="1h")

# ── Pipeline 3: premieres, with a one-click Follow button ────────────────
#
# PREMIERE_REPORT is EPISODE_CARD plus the signedaction button. The button
# renders as plain text ("Automatically added") on installs without a
# public base URL and signing key configured, so the template stays valid
# either way.

p_src  = input("rss", url=env("TV_FEED", default="https://feeds.example.com/tv"))
p_meta = process("metainfo_file", upstream=p_src)
p_req  = process("require", upstream=p_meta,
                 fields=["title", "series_episode_id", "series_season",
                         "series_episode", "_quality"])
p_qual = process("quality", upstream=p_req, spec="720p+ webrip+")
p_new  = process("premiere", upstream=p_qual, season=1, episode=1, reject_unmatched=True)
p_tvdb = process("metainfo_tvdb", upstream=p_new, api_key=TVDB_API_KEY)
output("notify", upstream=p_tvdb, via="email", config=SMTP,
       title="{{len .Entries}} premiere{{if ne (len .Entries) 1}}s{{end}} airing soon",
       body=PREMIERE_REPORT)

pipeline("premieres", schedule="0 9 * * *")
