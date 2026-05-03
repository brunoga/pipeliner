# pathscrub

Sanitizes path fields to remove or replace characters that are invalid on the
target filesystem. Useful before writing `download_path` to disk.

Also available as template functions `scrub` and `scrubwin` for inline use in
`pathfmt` or `set` templates.

## Config

```yaml
pathscrub:
  target: windows    # "windows", "linux", or "generic" (default: generic)
  fields:            # which entry fields to sanitize (default: ["download_path"])
    - download_path
    - title
```

**Targets:**
- `windows` — replaces `< > : " / \ | ? *`, control characters, and reserved
  names (CON, PRN, AUX, NUL, COM1–9, LPT1–9); strips trailing dots and spaces.
- `linux` — replaces `/` and null bytes only.
- `generic` — same rules as `windows` (safe for cross-platform use).

## Example

```yaml
tasks:
  movies:
    rss:
      url: "https://example.com/rss"
    pathfmt:
      path: '/movies/{{.Title}}'
    pathscrub:
      target: generic
    deluge:
      path: "{{.download_path}}"
```
