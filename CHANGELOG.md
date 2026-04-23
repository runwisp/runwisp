# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Memory & binary size optimization.** Daemon idle RSS reduced ~19% (27.0 MB → 21.9 MB) and stripped binary size reduced ~22% (23.1 MB → 17.9 MB):
  - Replaced GORM with `database/sql` + `modernc.org/sqlite` driver directly in the storage layer. Dropped `gorm.io/gorm`, `glebarez/sqlite`, `jinzhu/inflection`, `jinzhu/now`, and a large generic/reflection tax.
  - Replaced `charmbracelet/log` with stdlib `log/slog` across the daemon. Dropped `charmbracelet/log` and `go-logfmt`.
  - Docker client is now lazily initialized (`NewLazyContainerBackend`), so users who never run container tasks pay no idle cost for it.
  - EventBus now publishes synchronously; removed goroutine-per-handler-per-publish churn.
  - Persistence queue switched from `chan func()` to a typed `chan persistTask`, eliminating per-run closure allocations; channel buffer right-sized from 10000 → 1024.
  - Default `GOMEMLIMIT` of 128 MiB applied when not set via env, letting the GC trade CPU for RSS at steady state.
  - `debug.FreeOSMemory()` called after retention cleanup to return freed pages to the kernel.

## [0.1.1] - 2026-04-22

### Added

- WebUI shows a "Connection Lost" panel with live downtime, retry countdown, and a manual retry button when the daemon is unreachable.
- Sidebar now shows a live connection status indicator; clicking it when offline triggers an immediate reconnect attempt.

### Fixed

- Port conflict on daemon startup now shows a clear error identifying what is blocking the port, instead of silently timing out.
- Daemon startup failures now always print the log tail, regardless of how the process failed.
- Daemon process exit during startup is now distinguished from a runtime exit in error messages.

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

[Unreleased]: https://github.com/runwisp/runwisp/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/runwisp/runwisp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/runwisp/runwisp/releases/tag/v0.1.0
