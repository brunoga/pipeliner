# Security Policy

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Use [GitHub private vulnerability reporting](https://github.com/brunoga/pipeliner/security/advisories/new) to report the issue confidentially. You will receive an acknowledgement within 48 hours and a resolution timeline within 7 days.

Please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce or a proof-of-concept
- Any suggested fixes if you have them

## Scope

This project runs as a local daemon with filesystem and network access configured explicitly by the user. The primary security considerations are:

- **Config file injection** — variables and templates are substituted before parsing; ensure config files are not world-writable.
- **Exec plugin** — the `exec` plugin runs arbitrary shell commands; only use it with commands you control.
- **Credential storage** — API keys (Trakt, TMDb, TheTVDB) are read from the config file or environment variables; protect them accordingly.

## Web UI threat model

The bundled web UI is designed for a **single user** running pipeliner on a trusted network (typically the same host or LAN). Specifically:

- A successful login invalidates every other active session — only one browser can be authenticated at a time. Open the UI on a second device and the first device is signed out on its next request.
- There is no per-user permissioning, no rate limiting on login attempts, and no CSRF tokens (cookies are `SameSite=Strict`, which mitigates the cross-site form-submit class).
- Credentials (`--web-user` / `--web-password` or the `PIPELINER_WEB_USER` / `PIPELINER_WEB_PASSWORD` env vars) are hashed with SHA-256 in memory for comparison; they are never persisted.

If you need to expose the UI to the public internet, terminate TLS at a reverse proxy and add a network-level access control (VPN, IP allow-list, or a stronger auth proxy such as Authelia or oauth2-proxy) in front of pipeliner.
