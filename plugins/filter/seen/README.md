# seen

Rejects entries already processed in a previous run. Computes a SHA-256 fingerprint from configured entry fields and checks it against a persistent SQLite store.

## Config

| Key | Type | Required | Default | Description |
|-----|------|----------|---------|-------------|
| `db` | string | no | `pipeliner.db` | SQLite database path |
| `fields` | string or list | no | `["url"]` | Entry fields to include in the fingerprint |
| `local` | bool | no | false | Scope the seen store to this task name |

## Example

```yaml
tasks:
  my-task:
    rss:
      url: "https://example.com/feed"
    seen:
      db: pipeliner.db
      fields: [url]
```

## Notes

- Fingerprints are written to the store during the **learn** phase, which runs after all output plugins complete successfully.
- Use `local: true` when multiple tasks consume the same feed but should track seen entries independently.
