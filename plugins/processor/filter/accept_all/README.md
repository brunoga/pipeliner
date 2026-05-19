# accept_all

Accepts every entry that is not already accepted or rejected. Useful as a pass-through step when you want all entries from a source to reach an output sink without any filter logic.

## Config

No configuration options.

## Example

```python
src = input("rss", url="https://example.com/rss")
acc = process("accept_all", upstream=src)
output("list_add", upstream=acc, list="articles")
pipeline("sync-list", schedule="1h")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |
