# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]



## [0.2.0] - 2026-04-25

### Improved

- **Config format moved from YAML to TOML.** `runwisp.yaml` is no longer read; configuration lives in `runwisp.toml`. Every per-task field has been flattened and renamed for a much gentler learning curve. The `runwisp add` and `runwisp edit` CLI commands are gone — edit the TOML file in your editor (it's now simple enough that interactive editors are unnecessary). 

- **Launch tickets are shorter without losing security.** One-time launch-ticket URLs now use 43 base62 characters instead of 64 hex characters, keeping the same ~256 bits of entropy while producing a noticeably shorter URL.

### Added

- **Fullscreen log streaming in the TUI.** Press `f` while viewing a task's logs to expand to fullscreen — the terminal cursor works normally (no hijacking), line numbers disappear, and you can select and copy text directly with your mouse. Press `f` or `esc` to return to the normal view.

### Fixed

- **TUI sidebar navigation while viewing a run.** When an execution detail was open, pressing `←` to focus the sidebar and then `Enter` on a sidebar item (like "Home") could either snap back to the previous task detail or do nothing at all. Sidebar keyboard navigation now consistently takes you to the selected view.

## [0.1.2] - 2026-04-23
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

[Unreleased]: https://github.com/runwisp/runwisp/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/runwisp/runwisp/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/runwisp/runwisp/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/runwisp/runwisp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/runwisp/runwisp/releases/tag/v0.1.0
