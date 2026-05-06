# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.0] - 2026-05-06

### Added

- **Notifications.** RunWisp now pushes failures at you instead of waiting for you to notice. In-app notifications are on by default - every failed, timed-out, or crashed run lights up a bell in the Web UI and the footer in the TUI, with zero config. Repeats are coalesced into a single row with a sparkline and a rhythm-aware summary ("12× in the last hour, latest 30s ago"), so a flapping job is one line, not a wall of noise. Want it in Slack or Telegram too? Add a `[[notifier]]` block and a route, and the same failures flow out as messages — and if your webhook breaks, that failure shows up in-app, so the alerting never silently dies.

### Changed

- **Default HTTP port moved from `8080` to `9477`.** Port `8080` collides with too many other tools by default (Tomcat, dev proxies, k8s port-forwards). RunWisp now listens on `9477` out of the box; pass `--port 8080` (or set it in `runwisp.toml`) to keep the old behaviour.

### Fixed

- **Version is now reported correctly everywhere.** The CLI (`runwisp version`), the TUI info pane, and the web dashboard previously all showed `0.0.0-dev` even on released builds. The build now reads the version from the top of `CHANGELOG.md`, so what you see in the UI matches the release you installed.
- **Duration and size fields in API responses now serialise as integers.** `timeout`, `restart_delay`, `retry_delay`, and `keep_for` are emitted as nanoseconds (int64); `log_max_size` is emitted as bytes. The TOML config surface is unchanged, but values are parsed once at config load time, so a malformed duration or byte size is now rejected at startup with a clear error.

## [0.3.0] - 2026-04-29

### Added

- **Always-on services with `[services.NAME]`.** A new top-level config section for long-lived processes. RunWisp keeps the configured number of `instances` alive at all times and restarts each replica with exponential backoff (default 1s → 60s cap) on exit.
- **Restart backoff is now configurable.** New per-service `restart_delay` (default `"1s"`) and `restart_backoff` (`exponential` or `none`, default `exponential`) tune the curve. A replica that runs at least 60 seconds resets its backoff counter, so a service that finally stabilises returns to fast-restart behaviour on its next failure.
- **`POST /api/tasks/{name}/restart`.** Cancels every active replica of a service; the supervisor refills each freed slot via the normal exit-handler path. The web dashboard and TUI both surface a **Restart Service** button (in place of "Run Now") on a service's detail view, so a one-key/one-click restart is available without using the API directly.
- **`replica_index` on every run.** Persisted on the run record and surfaced in the API. The TUI and the web dashboard both suffix the task name with `#N` when index > 0 (in the run lists, run header, and recent-activity panels) so multi-replica services are visible at a glance.

### Changed

- **`restart = "always"` on `[tasks.NAME]` is now rejected.** There is exactly one canonical way to express always-on: `[services.NAME]`. Migrate existing tasks by moving them under `[services.*]` (cron-driven jobs are unaffected — they continue to use `[tasks.*]`).
- **`runwisp list` shows `(service xN)`** in the SCHEDULE column for services, alongside the cron expression for scheduled tasks and `(manual)` for tasks without a schedule.

### Fixed

- **Services now self-heal after a manual stop or service-restart.** Stopping a single service replica from the TUI or web dashboard left the slot permanently empty, and **Restart Service** killed every replica without bringing any back up. Both actions now correctly trigger the supervisor to refill each freed slot at its original replica index.
- **Hardened defaults for public deployments.** The data directory and its internal files (PID, password, JWT secret) are now created with owner-only permissions and symlink-safe writes. Sessions are validated with explicit issuer/audience claims, and the `Secure` cookie flag is set only on real TLS or a proxy you've explicitly trusted via `RUNWISP_TRUST_PROXY` — not based on a spoofable header. Starting on a non-loopback address prints a reminder to put a reverse proxy in front; setting `RUNWISP_TRUST_PROXY` to an open range (`0.0.0.0/0`, `::/0`) is now a startup error.
- **A noisy client can no longer starve the daemon.** API request bodies are capped at 1 MiB, headers at 64 KiB, and concurrent log streams are limited to 64 globally / 8 per IP — enforced against the real TCP peer, not a forwarded header.

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

[Unreleased]: https://github.com/runwisp/runwisp/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/runwisp/runwisp/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/runwisp/runwisp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/runwisp/runwisp/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/runwisp/runwisp/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/runwisp/runwisp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/runwisp/runwisp/releases/tag/v0.1.0
