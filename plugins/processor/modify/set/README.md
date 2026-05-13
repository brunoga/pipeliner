# set

Unconditionally sets one or more entry fields. Each config key becomes a field name; its value is a Go template rendered against the current entry fields.

## Config

Any key-value pairs. Values are Go template strings.

## Example

```python
src    = input("rss", url="https://example.com/rss")
tagged = process("set", upstream=src, category="tv")
output("print", upstream=tagged)
pipeline("tagged-feed")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | all field names listed in the config |
| Requires | any fields referenced in the value patterns |
