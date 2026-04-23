# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Improved
- **Friendlier error messages on the CLI.** When the daemon rejects a login, runs out of retries after too many failed attempts, or another process is blocking the port, `runwisp` and `runwisp tui` now print a clearly formatted, coloured error with concrete next steps — instead of a single long line buried in a log message. Login rate-limit responses from the daemon are also detected and explained, so you no longer get a generic "auth failed" when you just need to wait a few minutes.
- **Significantly reduced memory and disk footprint.** The daemon now uses ~20% less memory at idle (27 MB → 22 MB) and the binary is ~22% smaller (23 MB → 18 MB). On memory-constrained machines this means more headroom for your actual workloads. The daemon also returns memory to the OS more aggressively after cleanup runs, and caps its own memory usage at 128 MiB by default (overridable via `GOMEMLIMIT`).
- **Daemon startup is faster and more reliable.** The internal coordination between the health poller, log tailer, and PID watcher during `runwisp daemon` startup has been simplified; fatal log lines now abort startup immediately with no extra delay.
- **`runwisp add` / `runwisp edit` share the same save logic.** Both commands now go through a single code path for writing the config file, so any future bug fix or new feature (e.g. pre-save validation) automatically applies to both.
- **Log streaming reconnects are more consistent.** The SSE reconnect and back-off strategy for live log tails in the web UI is now handled by the same code that governs all other SSE streams, eliminating a separate implementation that could drift from the main one.

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
