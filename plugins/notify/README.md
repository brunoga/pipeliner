# Notify plugins

Notify plugins send side-channel notifications about a completed run — independent of individual entry processing. They receive the full run result (accepted count, rejected count, duration) rather than individual entries.

| Plugin | Description |
|--------|-------------|
| [email](email/README.md) | Send a run-summary email via SMTP |
| [pushover](pushover/README.md) | Send a Pushover push notification |
| [webhook](webhook/README.md) | POST a run summary to an HTTP endpoint |

Notify plugins are typically used via the [`notify` output plugin](../output/notify/README.md), which selects the notifier type at runtime:

```python
task("my-task", [
    plugin("rss", url="https://example.com/feed"),
    # ... filters ...
    plugin("notify", via="webhook", url="https://hooks.example.com/pipeliner"),
])
```
