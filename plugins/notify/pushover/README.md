# pushover

Sends notifications via the [Pushover](https://pushover.net/) API.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `user` | string | yes | | Pushover User Key |
| `token` | string | yes | | Pushover API Token |
| `device` | string | no | | Optional device name to target |

## Example

```python
plugin("notify",
    via="pushover",
    user=env("PUSHOVER_USER"),
    token=env("PUSHOVER_TOKEN"),
)
```
