# exec

Runs a shell command for each accepted entry. The command is a Go template rendered against the entry's field map.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `command` | string | yes | — | Shell command template |

## Example

```python
task("files", [
    plugin("filesystem", path="/downloads/watch"),
    plugin("exec", command='notify-send "New download" "{{.Title}}"'),
])
```

```python
task("cleanup", [
    plugin("filesystem", path="/media/tv", recursive=True, mask="*.nfo"),
    plugin("exec", command='rm "{{.location}}"'),
])
```

## Notes

- Commands are executed via the system shell (`/bin/sh -c`).
- Errors are logged as warnings; processing continues for remaining entries.
- Quote paths carefully — filenames with spaces require quoting inside the template.
