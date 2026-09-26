# torrent-janitor.star
#
# Close the download loop: clean up the Transmission session and recover
# from failed grabs.
#
# The backend= key accepts "transmission", "qbittorrent", or "deluge" —
# torrent_session and torrent_control speak all three, so switching this
# config to another client is just a backend/host swap.
#
# Pipeline 1 (seed-janitor): remove torrents that have earned their keep —
# seeding with ratio >= 2.0 or more than 3 days (259200s) of seed time.
# Entries that don't match the condition stay undecided, so the
# torrent_control sink never sees them.
#
# Pipeline 2 (failed-grab-recovery): torrent_failed accepts dead torrents
# (errored in the client, or stalled/zero-progress for longer than
# stall_timeout) and fans out to two sinks:
#
#   - mark_failed writes the original release URL (resolved through the
#     grab records the transmission/qbittorrent/deluge sinks store at add time)
#     into the shared seen_failed bucket and un-tracks the episode/movie,
#     so a *different* release of the same content can be grabbed on the
#     next run of your download pipelines — put retry_failed=True on their
#     seen filter so the exact failed release stays blocked.
#   - torrent_control remove_with_data deletes the dead torrent AND its
#     partial data from disk; an HTML email reports what went.
#
# Requirements: TRANSMISSION_HOST (optional), PUSHOVER_USER, PUSHOVER_TOKEN,
# SMTP_HOST/SMTP_USER/SMTP_PASSWORD/NOTIFY_TO.

trans_host     = env("TRANSMISSION_HOST", default="localhost")
pushover_user  = env("PUSHOVER_USER", default="YOUR_PUSHOVER_USER")
pushover_token = env("PUSHOVER_TOKEN", default="YOUR_PUSHOVER_TOKEN")

SMTP = {
    "smtp_host": env("SMTP_HOST", default="smtp.example.com"),
    "smtp_port": 587,
    "username":  env("SMTP_USER", default="you@example.com"),
    "password":  env("SMTP_PASSWORD", default="YOUR_SMTP_PASSWORD"),
    "sender":    env("SMTP_USER", default="you@example.com"),
    "to":        env("NOTIFY_TO", default="you@example.com"),
    "html":      True,
}

# The purge report. Mail clients only render inline styles and table
# layout reliably, so that is all this uses — no flexbox, no stylesheet,
# no external images. The helpers turn raw field values into readable
# text: filesize for byte counts, duration for seed time in seconds, ago
# for timestamps, and sumfield to total the bytes reclaimed across the
# batch. torrent_last_activity, torrent_tracker_host and torrent_label
# are MayProduce fields, hence the with/else around them.
JANITOR_REPORT = """
{{$n := len .Entries}}<div style="margin:0;padding:24px 12px;background:#f3f4f6;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table role="presentation" cellpadding="0" cellspacing="0" border="0" align="center" width="640" style="width:100%;max-width:640px;margin:0 auto;border-collapse:collapse;">

<tr><td style="background:#1f2937;border-radius:10px 10px 0 0;padding:18px 22px;">
<div style="color:#f9fafb;font-size:17px;font-weight:600;line-height:1.3;">Torrent janitor</div>
<div style="color:#9ca3af;font-size:13px;padding-top:4px;line-height:1.4;">Purged {{$n}} dead torrent{{if ne $n 1}}s{{end}} and reclaimed {{filesize (sumfield "torrent_downloaded" .Entries)}} of disk</div>
</td></tr>

{{range .Entries}}
<tr><td style="background:#ffffff;border-left:1px solid #e5e7eb;border-right:1px solid #e5e7eb;border-bottom:1px solid #e5e7eb;padding:18px 22px;">

<div style="font-size:15px;font-weight:600;color:#111827;line-height:1.4;word-break:break-word;">{{.Title}}</div>

{{with index .Fields "torrent_state"}}<div style="padding-top:8px;"><span style="background:#fee2e2;color:#991b1b;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.05em;padding:3px 9px;border-radius:99px;white-space:nowrap;">{{.}}</span></div>{{end}}

{{with .AcceptReason}}<div style="color:#b91c1c;font-size:13px;line-height:1.5;padding-top:10px;">{{.}}</div>{{end}}
{{with index .Fields "torrent_error"}}<div style="color:#6b7280;font-size:12px;line-height:1.5;padding-top:4px;">{{.}}</div>{{end}}

{{$p := index .Fields "torrent_progress"}}
<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;margin-top:14px;">
<tr>
<td width="{{printf "%.0f" $p}}%" height="6" style="background:#ef4444;font-size:0;line-height:0;border-radius:3px 0 0 3px;">&nbsp;</td>
<td height="6" style="background:#e5e7eb;font-size:0;line-height:0;border-radius:0 3px 3px 0;">&nbsp;</td>
</tr>
</table>

<table role="presentation" cellpadding="0" cellspacing="0" border="0" width="100%" style="width:100%;border-collapse:collapse;margin-top:12px;font-size:13px;line-height:1.5;">
<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;width:1%;">Downloaded</td>
<td style="padding:4px 0;color:#111827;">{{filesize (index .Fields "torrent_downloaded")}} of {{filesize (index .Fields "torrent_file_size")}} &middot; {{printf "%.1f" $p}}%</td>
</tr>
<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;">Swarm</td>
<td style="padding:4px 0;color:#111827;">{{index .Fields "torrent_connected_seeds"}} seed{{if ne (index .Fields "torrent_connected_seeds") 1}}s{{end}} &middot; {{index .Fields "torrent_connected_peers"}} peer{{if ne (index .Fields "torrent_connected_peers") 1}}s{{end}} connected</td>
</tr>
<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;">Uploaded</td>
<td style="padding:4px 0;color:#111827;">{{filesize (index .Fields "torrent_uploaded")}} &middot; ratio {{printf "%.2f" (index .Fields "torrent_ratio")}}</td>
</tr>
{{with index .Fields "torrent_seed_time"}}<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;">Seeded for</td>
<td style="padding:4px 0;color:#111827;">{{duration .}}</td>
</tr>{{end}}
<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;">Added</td>
<td style="padding:4px 0;color:#111827;">{{ago (index .Fields "torrent_added_at")}}</td>
</tr>
<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;">Last activity</td>
<td style="padding:4px 0;color:#111827;">{{with index .Fields "torrent_last_activity"}}{{ago .}}{{else}}never{{end}}</td>
</tr>
{{with index .Fields "torrent_tracker_host"}}<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;">Tracker</td>
<td style="padding:4px 0;color:#111827;">{{.}}</td>
</tr>{{end}}
{{with index .Fields "torrent_label"}}<tr>
<td style="padding:4px 12px 4px 0;color:#6b7280;white-space:nowrap;">Label</td>
<td style="padding:4px 0;color:#111827;">{{.}}</td>
</tr>{{end}}
</table>

<div style="margin-top:12px;padding-top:10px;border-top:1px solid #f3f4f6;color:#9ca3af;font-size:11px;line-height:1.6;word-break:break-all;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;">
{{index .Fields "torrent_info_hash"}}{{with index .Fields "torrent_download_dir"}}<br>{{.}}{{end}}
</div>

</td></tr>
{{end}}

<tr><td style="background:#ffffff;border:1px solid #e5e7eb;border-top:none;border-radius:0 0 10px 10px;padding:14px 22px;color:#6b7280;font-size:12px;line-height:1.6;">
Each release above is blocked from future grabs by its info hash, and its episode or movie was un-tracked so a different release can be picked up on the next run.
</td></tr>

</table>
</div>
"""

# ── Pipeline 1: remove well-seeded torrents ──────────────────────────────────

sess1 = input("torrent_session", backend="transmission", host=trans_host)
done  = process("condition", upstream=sess1, rules=[
    {"accept": 'torrent_state == "seeding" and (torrent_ratio >= 2.0 or torrent_seed_time > 259200)'},
    {"reject": "true"},
])
output("torrent_control", upstream=done,
       action="remove", backend="transmission", host=trans_host)

pipeline("seed-janitor", schedule="6h")

# ── Pipeline 2: detect dead grabs, mark them failed, purge them ──────────────

sess2  = input("torrent_session", backend="transmission", host=trans_host)
failed = process("torrent_failed", upstream=sess2, stall_timeout="4h")

# Branch 1: record the failure so the release is never re-grabbed and the
# episode/movie is un-tracked (a different release can be tried).
output("mark_failed", upstream=failed)

# Branch 2: purge the dead torrent and its partial files, then notify.
purged = output("torrent_control", upstream=failed,
                action="remove_with_data", backend="transmission", host=trans_host)
output("notify", upstream=purged, via="email", config=SMTP,
       title="Janitor purged {{len .Entries}} dead torrent{{if ne (len .Entries) 1}}s{{end}}",
       body=JANITOR_REPORT)

# Prefer a push? Swap the sink above for this one — same upstream, and the
# plain-text body suits a phone notification better than HTML does:
#
#   output("notify", upstream=purged, via="pushover",
#          config={"user": pushover_user, "token": pushover_token},
#          title="Dead torrent purged",
#          body="{{range .Entries}}{{.Title}} — {{.AcceptReason}}\n{{end}}")

pipeline("failed-grab-recovery", schedule="1h")
