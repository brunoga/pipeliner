# pipeliner

A media-automation tool that pulls entries from RSS feeds, active searches, or local filesystems, filters them against configurable rules, enriches them with metadata, and hands them off to download clients, notification services, or arbitrary shell commands.

Heavily inspired by [FlexGet](https://flexget.com). Pipelines are written in [Starlark](https://github.com/bazelbuild/starlark) — a simple, readable Python-like scripting language. Sources produce entries, processors filter and enrich them, sinks act on the accepted ones. The scheduler runs pipelines on cron or interval schedules.

## Installation

**Go (recommended):**

```sh
go install github.com/brunoga/pipeliner/cmd/pipeliner@latest
```

**Build from source:**

```sh
git clone https://github.com/brunoga/pipeliner
cd pipeliner
go build -o pipeliner ./cmd/pipeliner
```

**Pre-built binaries** for Linux, Windows, and macOS are available on the [releases page](https://github.com/brunoga/pipeliner/releases):

| OS | Architectures |
|----|---------------|
| Linux | `amd64`, `arm64`, `arm/v7` |
| Windows | `amd64`, `arm64` |
| macOS | `amd64` (Intel), `arm64` (Apple Silicon) |

**Docker:**

```sh
docker run -d \
  -p 8080:8080 \
  -v pipeliner-data:/config \
  -e PIPELINER_WEB_USER=admin \
  -e PIPELINER_WEB_PASSWORD=secret \
  ghcr.io/brunoga/pipeliner:latest
```

Images are published to the GitHub Container Registry (`ghcr.io/brunoga/pipeliner`) on every release tag, for `linux/amd64`, `linux/arm64`, and `linux/arm/v7`.

| Environment variable | Default | Description |
|----------------------|---------|-------------|
| `PIPELINER_WEB_USER` | — | Web UI username **(required)** |
| `PIPELINER_WEB_PASSWORD` | — | Web UI password **(required)** |
| `PIPELINER_WEB_ADDR` | `:8080` | Listen address |
| `PIPELINER_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `PIPELINER_CONFIG` | `/config/config.star` | Config file path |

The `/config` volume holds both `config.star` and `pipeliner.db` (the state database). Mount a named volume or bind-mount to persist state across container restarts. The config can be edited live through the web UI's **Edit Config** tab.

## Quick start

```python
# config.star
src    = input("rss", url="https://example.com/rss")
seen   = process("seen",       upstream=src)
series = process("series",     upstream=seen, static=["Breaking Bad"])
fmt    = process("pathfmt",    upstream=series,
                 path="/media/tv/{title}/Season {series_season:02d}",
                 field="download_path")
output("transmission", upstream=fmt, host="localhost", port=9091,
                        path="{download_path}")
pipeline("breaking-bad", schedule="1h")
```

```sh
pipeliner run                       # run all pipelines once
pipeliner daemon                    # run pipelines on their schedules
pipeliner daemon --web :8080        # daemon + enable the web UI on port 8080
pipeliner check                     # validate config without running
pipeliner list-plugins              # print all registered plugins
pipeliner auth trakt --client-id ID --client-secret SECRET  # Trakt OAuth
pipeliner version                   # print version
```

## Plugins

Pipeliner is built entirely from plugins. Each plugin has one of three roles:

| Role | Used as | Purpose |
|------|---------|---------|
| **source** | `input(…)` | Produce entries from RSS, files, indexers |
| **processor** | `process(…, upstream=…)` | Filter, enrich, or transform entries |
| **sink** | `output(…, upstream=…)` | Act on accepted entries (download, notify, exec) |

Connect multiple sources with `merge(src1, src2)`. Fan out to multiple sinks by calling `output()` more than once from the same upstream. Route entries to mutually exclusive branches with `route(upstream, tv="expr", movies="expr")`.

The web UI (enabled with `daemon --web :8080`) includes a visual pipeline editor, a live config editor, and a run history view with a per-entry run inspector. A built-in **User Guide** — the same comprehensive reference shipped as `docs/user-guide.html` in the release archives — is reachable from the header link in-app (or at `/guide`), covering every plugin, config key, and feature in depth.

See [`plugins/`](plugins/README.md) for the full plugin listing.

## Configuration

See [`configs/`](configs/README.md) for the config format reference, field documentation, and annotated example pipelines.
