# exec

Runs a command for each accepted entry. The command string is a template rendered against the entry, then split on whitespace and executed directly — there is no shell. Pipes, globs, redirects, and shell variable expansion are not supported.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `command` | yes | — | Command template; split on whitespace and executed directly |

## Example

```python
src  = input("filesystem", path="/downloads", mask="*.torrent")
meta = process("metainfo_torrent", upstream=src)
output("exec", upstream=meta,
       command="transmission-remote --add {file_location}")
pipeline("watch-folder", schedule="1m")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |

## Notes

- The command is split on whitespace — arguments with spaces must be handled by designing the command so spaces don't appear in interpolated values, or by using a wrapper script.
- Command errors are logged at error level; processing continues for remaining entries (the entry is not marked failed).
- Template functions (`upper`, `lower`, `scrub`, `replace`, etc.) are available — use `{title | scrub}` to produce filesystem-safe names.
