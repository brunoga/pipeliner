# set

Unconditionally sets one or more entry fields. Each config key becomes the field name written on the entry; its value is a `{field}` pattern or Go template rendered against the current entry fields.

## Config

Any key-value pairs are accepted. The key is the field name to set; the value is a pattern string.

```python
tagged = process("set", upstream=upstream,
    category="tv",                 # plain string
    label="{title} — {video_year}",  # {field} interpolation
    slug="{{.title | lower | replace \" \" \"-\"}}")  # Go template
```

## Example

```python
src    = input("rss", url="https://example.com/rss")
tagged = process("set", upstream=src, category="tv", feed="main")
output("print", upstream=tagged)
pipeline("tagged-feed", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | all field names listed in the config |
| Requires | any fields referenced in the value patterns |
