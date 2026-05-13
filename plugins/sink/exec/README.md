# exec

Runs a shell command for each accepted entry. The command is a Go template rendered against the entry's field map.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `command` | string | yes | — | Shell command template |

## Example

```python
output("exec", upstream=ready,
       command='notify-send "New" "{{.Title}}"' )
```

```python
output("exec", upstream=ready,
       command='notify-send "New" "{{.Title}}"' )
```

## Notes

- Commands are executed via the system shell (`/bin/sh -c`).
- Errors are logged as warnings; processing continues for remaining entries.
- Quote paths carefully — filenames with spaces require quoting inside the template.

## DAG role

| Property | Value |
|----------|-------|
| Role | `sink` |
| Produces | — |
| Requires | — |
