# Examples

Standalone programs that talk to a running pipeliner daemon — remote
automation clients, not part of the daemon itself.

| Example | Purpose |
|---------|---------|
| [`enqueue`](enqueue/) | Push a title — movie, show, disc — to a pipeline (`/api/ingest?pipeline=…` + immediate trigger) from anywhere. The queue is derived from the pipeline, so you name one thing, not two. Pairs with [`configs/ondemand-request.star`](../configs/ondemand-request.star). |

Build any example with `go build ./examples/<name>` or run it directly with
`go run ./examples/<name> …`.
