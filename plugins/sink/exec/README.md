# exec

Runs a command for each accepted entry. The command string is a template rendered against the entry, then split into arguments and executed directly — **there is no shell, on any platform**. Pipes, globs, redirects and variable expansion are not supported, deliberately: the rendered string carries release titles straight from an indexer, and a shell would make them executable.

Running the program directly is also what makes this portable. There is no `sh` on Windows, and Go applies each OS's own argument quoting, so the same config works on Linux, macOS and Windows.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `command` | yes | — | The program to run. Without `args` it may carry arguments too and is split quote-aware; with `args` it is the program path verbatim |
| `args` | no | — | Arguments, one argv element each. No splitting happens, so spaces need no quoting |
| `ignore_errors` | no | `false` | Keep the entry accepted when the command exits non-zero, instead of failing it |

## Arguments with spaces

Splitting honours single quotes, double quotes and backslash escapes, so a path can be passed as one argument:

```python
output("exec", upstream=prev, command='convert "{file_location}" --out "{download_path}"')
```

For anything awkward to quote, use `args` — each element is exactly one argument and nothing is parsed:

```python
output("exec", upstream=prev,
       command="/usr/local/bin/mvctools",
       args=["--input", "{file_location}", "--output", "{download_path}", "--tag", "3D SBS"])
```

Quoting rules are the POSIX ones: inside single quotes everything is literal; inside double quotes a backslash escapes only `"` and `\`; outside quotes a backslash escapes the next character. Unterminated quotes fail the entry rather than executing a wrong split.

## Failures

A non-zero exit **fails the entry**. That matters when a tracker (`seen`, `movies`, `series`) sits upstream: the commit phase only records entries that were delivered successfully, so a failed command leaves the item untracked and it is retried on the next run. Set `ignore_errors=True` for fire-and-forget commands where that is not wanted.

One entry's command failing never aborts the sink — the remaining entries still run.

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

- Arguments are split quote-aware, or given exactly via `args`. There is no shell on any platform.
- Command errors are logged at error level and fail the entry unless `ignore_errors=True`; the remaining entries still run.
- Template functions (`upper`, `lower`, `scrub`, `replace`, etc.) are available — use `{title | scrub}` to produce filesystem-safe names.
