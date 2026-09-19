# Examples

Standalone programs that talk to a running pipeliner daemon — remote
automation clients, not part of the daemon itself.

| Example | Purpose |
|---------|---------|
| [`request-movie`](request-movie/) | Push a movie title to an on-demand pipeline (`/api/ingest/{queue}` + immediate trigger) from anywhere. Pairs with [`configs/ondemand-request.star`](../configs/ondemand-request.star). |

Build any example with `go build ./examples/<name>` or run it directly with
`go run ./examples/<name> …`.
