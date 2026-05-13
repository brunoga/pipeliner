# Contributing to pipeliner

Thanks for your interest in contributing.

## Prerequisites

- Go (version specified in `go.mod`)
- SQLite development libraries (`libsqlite3-dev` on Debian/Ubuntu, `sqlite` on macOS via Homebrew)
- [`golangci-lint`](https://golangci-lint.run/welcome/install/) for linting

## Building

```sh
go build ./cmd/pipeliner
```

## Running tests

```sh
go test ./...

# With race detector (recommended before submitting a PR):
go test -race ./...
```

## Linting

```sh
golangci-lint run
```

## Adding a plugin

Plugins live under `plugins/source/`, `plugins/processor/`, or `plugins/sink/` depending on their role. Within `processor/` there are subdirectories for `metainfo/`, `filter/`, and `modify/`. To add a plugin:

1. Create the package directory (e.g. `plugins/processor/filter/myfilter/`) and implement `SourcePlugin`, `ProcessorPlugin`, or `SinkPlugin` from `internal/plugin/plugin.go`.
2. Call `plugin.Register` in an `init()` function with a `plugin.Descriptor` that sets `Role`, `Produces`, and `Requires`.
3. Add a blank import of your new package in `cmd/pipeliner/main.go`.
4. Write tests alongside the plugin code.
5. Add a `README.md` documenting config options and the DAG role table.
6. Update the sub-directory index (e.g. `plugins/processor/filter/README.md`) to include the new plugin.

See `plugins/processor/filter/regexp/` as a template.

## Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | When to use |
|--------|-------------|
| `feat` | New feature or plugin |
| `feat(plugin)` | New plugin specifically |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `ci` | CI / tooling changes |
| `chore` | Maintenance (deps, build, etc.) |
| `test` | Tests only |
| `refactor` | Refactor without behaviour change |

## Pull requests

- Keep PRs focused — one logical change per PR.
- All CI checks must pass before merge.
- Include tests for any new behaviour.
- For new plugins, include a README and a sample config snippet.

## Code style

- Standard `gofmt` formatting.
- No unnecessary comments — the code should speak for itself.
- Error strings are lowercase and do not end with punctuation.
- Context is threaded through all blocking calls.
