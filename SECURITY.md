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
