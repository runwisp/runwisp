# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Full-text log search across a task's runs.** Press <kbd>/</kbd> in the TUI, or click **Search** in the task header (or <kbd>⌘K</kbd> / <kbd>Ctrl K</kbd>) in the Web UI, to grep every captured run of a task for a substring or RE2 regex, case-sensitive or not. Hits are grouped by run with the run's start time as the heading; click one and the existing log viewer jumps to the line and pulses a highlight. The scan is on-demand — no background index, no extra disk usage — and walks runs newest-first in parallel, with an opaque cursor for paging through long histories. Exposed at `GET /api/tasks/{name}/log/search`. See [Logs / Search](https://docs.runwisp.com/concepts/logs/#full-text-search).

## [0.6.0] - 2026-05-22

### Added

- **Email (SMTP) notifications.** A new `type = "smtp"` notifier sends alerts to any inbox through any SMTP server — Gmail, Amazon SES, Mailgun, Postmark, SendGrid, or a self-hosted relay. STARTTLS and implicit TLS (port 465) are both supported; auth is optional for local relays. Inline recipient overrides — `notify_on_failure = ["email-ops:alerts@example.com"]` — reuse one credential set across many tasks. Messages ship as `multipart/alternative` so rich email clients render the HTML layout while terminal MUAs see a plain-text fallback. See [Notifications / Email](https://docs.runwisp.com/notifications/providers/smtp/).

- **`runwisp service install` wires up systemd or launchd autostart.** One command installs a user-level service unit (systemd on Linux/WSL, LaunchAgent on macOS) so the daemon comes back up after reboot. Companions: `service uninstall`, `service status`, and `--print` to emit the unit for Ansible / Nix. WSL prints a `Register-ScheduledTask` PowerShell recipe alongside the systemd path.

- **Per-task environment variables via `env` and `env_file`.** Set `env = { PORT = "8080" }` on a `[tasks.*]` / `[services.*]` (or `[defaults]`) table to overlay inline values; `env_file = "secrets.env"` reads dotenv from disk. Task-level entries override `[defaults]` on key collision. Values loaded from `env_file` stay on the daemon — the API and Web UI only show the file path.

- **Prometheus-compatible `/metrics` endpoint (opt-in).** OpenMetrics 1.0 text covering run counters, last-failure timestamp, per-task active-run gauges, and daemon CPU / memory / uptime. Off by default; enable with `[daemon] metrics_enabled = true`, and bind it to loopback with `[daemon] metrics_listen = "127.0.0.1:9478"` when the main `--host` is public. See [Operations / Metrics](https://docs.runwisp.com/operations/metrics/).

- **Bulk run actions in the Web UI with undo.** Master checkbox in the heading plus per-row checkboxes, with inline Re-run, Cancel, and Delete. Deleted rows return for 5s via a toast (survives reload).

- **Delete a run from the Web UI and TUI.** Trash button on the run detail panel; the TUI exec view binds <kbd>D</kbd>. Removes the metadata row and the captured log segment.

- **`[daemon] external_url` for notification deep-links.** Set `external_url = "https://your-host.example.com"` and Slack / Telegram messages include a direct link to the run in the Web UI.

- **The Web UI sidebar collapses on narrow viewports.** The navigation lives behind a hamburger menu on phones and tablets — no more horizontal scroll.

- **Trigger-type icons next to every task.** Server icon for services, clock for cron, hand for manual — in the sidebar and dashboard. Hover for the cron expression or instance count.

- **Notification "View run #abc1234 →" chip.** Run-scoped in-app notifications now carry a chip with the short run ID so you can jump straight to it.

### Changed

- **Web UI design refresh.** Every panel snaps to the same border radius, tone, and shadow; sidebar/page hue drift is gone. A topbar connection pip surfaces daemon reachability on every page. The runs list is now **virtualized and server-paginated** — search, status filter, and sort order route through the server instead of clipping at a 200-row client cap.
- **Telegram and Slack notifications got a major facelift.** A one-line headline with emoji + verb, exit code and duration, trigger and timestamp, plus the **last 3 lines of captured output** for failures and timeouts and a deep-link when [`[daemon] external_url`](https://docs.runwisp.com/configuration/daemon/#external_url) is set. The flat single-paragraph form is gone.
- **Default data directory is now `./.runwisp/` (was `./data/`).** Keeps RunWisp's state out of the way when `runwisp.toml` lives next to a framework project that already has its own `data/`. To stay on the old path, pass `--data data`. Absolute `--data` paths are unaffected.
- **System resources panel shows live memory bytes inline.** The Memory sparkline header now displays `used / total` (e.g. `1.2 GB / 8.0 GB`) next to the percentage.
- **OpenAPI exposes `EndReason` as a named schema.** Run `end_reason` values (`success`, `failed`, `timeout`, …) reference a single `components/schemas/EndReason` enum instead of inline enums per call site — generated clients pick it up as one shared type.

### Fixed

- **Service runs no longer claim they were triggered by the API.** Supervisor-driven instances (boot, crash, slot refill) now show **Service** ("Service auto-started" in notifications) in the dashboard, activity feed, and Slack / Telegram. Operator-initiated REST restarts still show **API**.

- **Graceful shutdown is now instant when there's nothing running.** The notification retention ticker used to keep the daemon alive for the full `[daemon] shutdown_timeout` on every exit; it now cancels with the rest of shutdown, so the daemon quits as soon as in-flight work drains.

## [0.5.0] - 2026-05-13

### Added

- **Local CLI/TUI connects over a Unix socket — no password needed.** The daemon exposes a `0600` socket in the data dir, so local `runwisp`, `tui`, `list`, and `exec` skip the password/JWT flow entirely. Remote network clients keep the password + JWT path unchanged.
- **`runwisp password` prints the ephemeral password for a second-device login.** Surfaces the daemon's fresh boot password so you can log into the Web UI from another browser, without it ever appearing in a log line or banner. Unix-socket only; operator-supplied passwords are never disclosed.
- **Download a run's full log from the Web UI and the TUI.** Run detail panels gain a Download log button (and a `d` TUI keybinding) that delivers the rotated + current segments as one `text/plain` file — works over SSH and tmux too.
- **`runwisp validate <path>` now uses the same loader as the daemon.** A file the validator accepts is a file the daemon will accept — same parser, same errors, same `did you mean…?` hints.
- **TUI startup and Web UI header show the resolved scheduler timezone.** The active IANA zone (and whether it came from config or the host) is now visible in both UIs and on the health endpoint.
- **Inline notification targets in `notify_on_failure` / `notify_on_success`.** A `"<id>:<target>"` token overrides a notifier's channel or `chat_id` for that route only — reuse one Slack or Telegram notifier across many destinations.

### Removed

- **Password and JWT secret are no longer written to disk (BREAKING).** The password is supplied via `RUNWISP_PASSWORD` or freshly generated every boot, and the JWT key is derived from it via HKDF — no secrets persisted under the data dir.
- **`data/password` file removed.** Previously stored under the data dir, now never written.
- **`runwisp init` is gone.** Running `runwisp` in an empty directory now offers to scaffold a starter file and drops you into the TUI in one step.
- **`runwisp run-task` is gone.** `runwisp exec <task>` handles both modes — dispatching through a running daemon or running in-process when none is up.
- **`runwisp.example.toml` is gone.** The starter `runwisp.toml` and the docs site are the single reference.

### Changed

- **`replica_index` is now `instance_index` everywhere.** API field, SQL column, Web UI, and TUI all use the new name — matching `instances` on `[services.*]`.
- **`append_notifiers` is now `global_notifiers`.** Same precedence, but the name finally reflects what it does: the channels that fire for every failure.
- **`queue_size` is gone from `[notify]`.** Notify ingress and per-action worker buffers now use fixed internal defaults.
- **`history_keep` defaults to `1024` and `history_keep_for` defaults to `90d`.** The bell history is bounded out of the box; override either to lift or lower the limits.
- **Services can be stopped permanently from the Web UI and TUI.** A new Stop Service button cancels every instance and tells the supervisor not to refill the slots; Restart Service brings everything back.
- **Run / Stop / Restart buttons are contextual.** UIs show Stop while a service is up, Restart once stopped, and grey out Run Task when a task isn't launchable, with a tooltip.
- **`max_catch_up_runs` no longer capped at 10 000.** Pick any positive integer that suits your workload.
- **Run log file paths now use UTC timestamps.** Hosts that flip DST or relocate no longer shift where new runs land on disk.
- **`parallelism` → `max_concurrent` on `[tasks.*]` (BREAKING).** The new name matches what the field means, and it's removed entirely from `[services.*]` — replica count is `instances`.
- **`graceful_stop` governs every kill path.** New per-task field (default `"5s"`): the daemon sends SIGTERM, waits up to `graceful_stop`, then SIGKILL — across timeouts, overlap, manual stop, and shutdown.
- **Daemon shutdown is bounded by `[daemon] shutdown_timeout`.** New top-level field (default `"10s"`, matching Docker's stop grace). Unfinished runs are recorded with `end_reason = "daemon_stopped"`.
- **Service restart backoff is configurable.** New `[defaults] backoff_reset_after = "60s"` plus per-service override, replacing the previously hardcoded threshold.
- **DST fall-back days no longer double-fire.** The scheduler dedupes by wall-clock minute; the suppressed firing is recorded with `end_reason = "dst_skipped"`.
- **`[scheduler] timezone` defaults to the host's system timezone.** Falls back to `UTC` only when undetectable; the resolved zone is shown in both UIs.
- **Numeric config fields take literal values; no more keywords or sentinels (BREAKING).** Every numeric field accepts a plain integer / duration / size — `"unlimited"`, `"inherit"`, `0`, and `-1` are gone. Omit a field to inherit the default.
- **`log_on_full = "kill_task"` records killed runs as `log_overflow`.** A new end reason that names exactly what happened, instead of generic `failed` or `stopped`.
- **Log index sidecars (`.idx`, `.tidx`) are no longer created for short runs.** Runs under 1 024 lines leave no sidecar files on disk.
- **Schema and CLI tidy-up before 1.0 (BREAKING).** A coordinated rename pass that removes accumulated naming inconsistencies — same capabilities, cleaner spelling.
  - `retry_backoff` and `restart_backoff` share one enum: `constant` / `linear` / `exponential`.
  - One verb for ad-hoc runs: `runwisp exec <task>` replaces `trigger`, `run`, and `run-task`.
  - `[notify] disable_inapp` is replaced by `[notify] append_notifiers`, which can also redirect the default catch-all.
- **Log streaming redesigned around absolute line numbers (BREAKING).** REST and SSE now speak lines, not bytes — with line-numbered events, native `Last-Event-ID` resume, and tail-from-end via `?from=-1000`.
  - `GET /log` paginates `{n, ts, stream, text}` entries.
  - `GET /log/raw` concatenates rotated + current segments for download.
  - `GET /log/stream` emits `line | rotated | dropped | done` SSE events.

### Fixed

- **Outbound notifiers (Slack, Telegram) now coalesce by default.** A flapping task is one Slack message plus a window-close summary instead of one per failure; set `[notify] coalesce_outbound = false` to opt out.
- **Skipped runs are now recorded as `end_reason = "skipped"`.** `on_overlap = "skip"` rejections no longer pose as real failures to dashboards, retries, or Slack/Telegram.
- **`RUNWISP_PASSWORD` is no longer written back to `data/password`.** Passing the password via env var or systemd `LoadCredential=` finally keeps credentials off disk as intended.
- **`notify.history_keep_for` now accepts day and week units.** The field finally honours `"30d"` / `"4w"` like the docs and other retention windows.
- **`min_free_space` no longer silently overrides `log_on_full = "kill_task"`.** Disk pressure now honours the task's overflow policy and fires a `log.disk_pressure` notification once per run.
- **SSE log stream no longer cuts off large outputs.** Runs producing more than 1 MB of un-emitted log data now drain fully before signalling completion.
- **TUI and Web UI open long logs at the tail.** Both UIs land at the tail in a single round-trip and lazily load older content on scroll-up.

## [0.4.0] - 2026-05-06

### Added

- **Notifications.** RunWisp now pushes failures at you — in-app notifications light up the Web UI bell and TUI footer for every failed, timed-out, or crashed run, with bursts coalesced into one rhythm-aware row. Add a `[[notifier]]` block to route the same events to Slack or Telegram.

### Changed

- **Default HTTP port moved from `8080` to `9477`.** Port `8080` collides with too many tools by default; pass `--port 8080` to keep the old behaviour.

### Fixed

- **Version is now reported correctly everywhere.** The CLI, TUI info pane, and web dashboard no longer show `0.0.0-dev` on released builds.
- **Duration and size fields in API responses now serialise as integers.** `timeout`, `restart_delay`, `retry_delay`, and `keep_for` are nanoseconds (int64); `log_max_size` is bytes. Malformed values are rejected at startup with a clear error.

## [0.3.0] - 2026-04-29

### Added

- **Always-on services with `[services.NAME]`.** A new top-level section for long-lived processes — RunWisp keeps `instances` replicas alive and restarts each one with exponential backoff on exit.
- **Restart backoff is now configurable.** New per-service `restart_delay` and `restart_backoff` tune the curve; a replica that runs at least 60 seconds resets its backoff counter.
- **`POST /api/tasks/{name}/restart`.** Cancels every active replica; the supervisor refills each freed slot. The Web UI and TUI surface a Restart Service button on service detail views.
- **`replica_index` on every run.** Persisted on the run record and surfaced in the API; both UIs suffix multi-replica services with `#N` at a glance.

### Changed

- **`restart = "always"` on `[tasks.NAME]` is now rejected.** There's exactly one canonical way to express always-on: `[services.NAME]`. Cron-driven jobs are unaffected.
- **`runwisp list` shows `(service xN)`** in the SCHEDULE column for services, alongside the cron expression for scheduled tasks and `(manual)` for the rest.

### Fixed

- **Services now self-heal after a manual stop or service-restart.** Stopping a single replica or hitting Restart Service used to leak slots; the supervisor now correctly refills each freed slot at its original replica index.
- **Hardened defaults for public deployments.** Data-dir files use owner-only permissions and symlink-safe writes; sessions carry explicit issuer/audience claims; the `Secure` cookie flag is set only on real TLS or a proxy you've explicitly trusted. Open `RUNWISP_TRUST_PROXY` ranges (`0.0.0.0/0`, `::/0`) are now a startup error.
- **A noisy client can no longer starve the daemon.** API request bodies cap at 1 MiB, headers at 64 KiB, and concurrent log streams limit to 64 globally / 8 per IP — enforced against the real TCP peer.

## [0.2.0] - 2026-04-25

### Improved

- **Config format moved from YAML to TOML.** `runwisp.yaml` is no longer read; per-task fields are flattened and renamed for a much gentler learning curve. `runwisp add` / `runwisp edit` are gone — edit the TOML file in your editor.
- **Launch tickets are shorter without losing security.** One-time launch-ticket URLs are now 43 base62 characters instead of 64 hex — same ~256 bits of entropy, noticeably shorter URL.

### Added

- **Fullscreen log streaming in the TUI.** Press `f` while viewing a task's logs to expand fullscreen — line numbers disappear and you can select and copy text directly with your mouse.

### Fixed

- **TUI sidebar navigation while viewing a run.** Focusing the sidebar and pressing Enter on a sidebar item now consistently takes you to the selected view instead of snapping back.

## [0.1.2] - 2026-04-23
### Improved

- **Friendlier error messages on the CLI.** Login rejections, retry exhaustion, port conflicts, and login rate-limits now print a clearly formatted, coloured error with concrete next steps — no more single-line log dumps.
- **Significantly reduced memory and disk footprint.** ~20% less memory at idle (27 MB → 22 MB) and a ~22% smaller binary (23 MB → 18 MB), with a 128 MiB `GOMEMLIMIT` cap by default.
- **Daemon startup is faster and more reliable.** Internal coordination between health poller, log tailer, and PID watcher is simplified; fatal log lines abort startup immediately.
- **`runwisp add` / `runwisp edit` share the same save logic.** Both commands go through one code path so bug fixes and new features apply to both.
- **Log streaming reconnects are more consistent.** SSE reconnect and back-off now use the same code as every other SSE stream in the UI.

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

[0.6.0]: https://github.com/runwisp/runwisp/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/runwisp/runwisp/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/runwisp/runwisp/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/runwisp/runwisp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/runwisp/runwisp/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/runwisp/runwisp/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/runwisp/runwisp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/runwisp/runwisp/releases/tag/v0.1.0
