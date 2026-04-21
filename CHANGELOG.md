# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Port conflict on daemon startup now surfaces a clear, actionable error message instead of silently timing out. If the configured port is held by a non-RunWisp process, RunWisp will tell you exactly what's blocking it and how to resolve it (stop the other process, pick a different port, or identify it with `ss`/`lsof`). ([#portcheck](apps/runwisp/cmd/runwisp/portcheck.go))
- Daemon startup failure log tail is now always printed on every failure path (fatal log line, process exit, or health check timeout), not only on timeout. Previously a fatal log line would abort startup but produce no log output.
- Error messages for daemon process exit during startup now include `"during startup"` to distinguish them from runtime exits.

## [0.1.0] - 2026-04-20

### Added

- Initial public release of RunWisp — open-source cron replacement and process supervisor.
- Single binary daemon with embedded SQLite, REST API, and SSE log streaming.
- Embedded Svelte 5 web dashboard served directly by the daemon.
- CLI commands: `run`, `trigger`, `status`, `list`, `add`, `edit`, `validate`, `tui`, `daemon`, `cloud`, `openapi`.
- Bubbletea terminal UI (`tui` command) with home view, exec list, log pane, and dialogs.
- Task scheduler with concurrency, restart, missed-run, and retention policies.
- CHAP authentication for the HTTP API.
- Deterministic human-readable instance fingerprint based on machine-id and working directory.

[Unreleased]: https://github.com/runwisp/runwisp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/runwisp/runwisp/releases/tag/v0.1.0
