# set

Unconditionally sets one or more entry fields. Each config key becomes a field name; its value is a Go template rendered against the current entry fields.

## Config

Any key-value pairs. Values are Go template strings.

## Example

```python
task("my-task", [
    plugin("rss", url="https://example.com/feed"),
    plugin("set",
        category="tv",
        label="{{.series_name}}",
        custom_path="/mnt/nas/{{.series_name}}",
    ),
    plugin("qbittorrent", host="localhost", category="{{.category}}"),
])
```
