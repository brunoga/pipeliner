# accept_all

Accepts every entry that is not already accepted or rejected. Useful as a pass-through step before `list_add` when you want to store entries from an input plugin without applying any filter logic.

## Config

No configuration options. Call with just the plugin name:

```python
plugin("accept_all")
```

## DAG role

| Property | Value |
|----------|-------|
| Role | `processor` |
| Produces | — |
| Requires | — |

## Example — sync a Trakt watchlist into a persistent list

```python
src = input("rss", url="https://example.com/rss")
acc = process("accept_all", upstream=src)
output("list_add", upstream=acc, list="watchlist")
pipeline("sync-list", schedule="1h")
```
