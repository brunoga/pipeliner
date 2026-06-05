# Modify processors (`plugins/processor/modify/`)

Modify processors mutate entry fields. Use them with `process("plugin-name", upstream=…)`.

| Plugin | Description |
|--------|-------------|
| [`pathfmt`](pathfmt/README.md) | Render a path pattern into a named field, with automatic scrubbing |
| [`set`](set/README.md) | Unconditionally set one or more entry fields |
| [`swap_state`](swap_state/README.md) | Swap entries between two states (e.g. `accepted` ↔ `rejected`) so downstream nodes can act on entries others rejected or failed |
