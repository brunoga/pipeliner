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

Each plugin lives in its own package under `plugins/<role>/<name>/` (e.g. `plugins/filter/regexp/`) and registers itself via `init()`. To add one:

1. Create the package directory and implement one of the three role interfaces from `internal/plugin/plugin.go`: `SourcePlugin`, `ProcessorPlugin`, or `SinkPlugin`.
2. Call `plugin.Register` in an `init()` function with a `plugin.Descriptor` that sets `Role`, `Produces`, and `Requires`.
3. Add a blank import of your new package in `cmd/pipeliner/main.go`.
4. Write tests alongside the plugin code.
5. Add a `README.md` in the plugin directory documenting config options and the DAG role table.
6. Update the role-group index (`plugins/<role>/README.md`) to include the new plugin.

See any existing plugin (e.g. `plugins/filter/regexp/`) as a template.

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
