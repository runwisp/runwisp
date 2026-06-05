# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Passwordless mode.** Opt-in `RUNWISP_NO_AUTH=1` disables the login boundary for local dev / trusted networks, with a startup banner and a persistent Web UI badge. See [Auth](https://docs.runwisp.com/operations/auth/#running-without-a-password).
- **Discord notifications.** New `type = "discord"` notifier — color-coded embeds posted to a Discord channel webhook. See [Discord provider](https://docs.runwisp.com/notifications/providers/discord/).
- **Webhook notifications.** New `type = "webhook"` notifier — POSTs structured JSON to any URL with optional custom headers. See [Webhook provider](https://docs.runwisp.com/notifications/providers/webhook/).
- **`${VAR}` / `${file:path}` substitution in `runwisp.toml`.** Any string value can pull from the daemon's environment or a file at config load; `run` is left to the shell. See [Substitution](https://docs.runwisp.com/configuration/substitution/).
- **`secrets` and `secrets_file` on `[tasks.*]` / `[services.*]` / `[defaults]`.** Same mechanics as `env`, but keys and values never leave the daemon. See [Environment & secrets](https://docs.runwisp.com/configuration/tasks/#environment--secrets).
- **`runwisp stop` and `runwisp restart`.** Service-aware: they delegate to systemd / launchd when the daemon runs under a managed unit, and fall back to SIGTERM + wait otherwise. See [Autostart / Stop & restart](https://docs.runwisp.com/operations/autostart/#stop--restart).
- **Stale-config notice.** When `runwisp.toml` (or an `env_file`) changes after boot, `runwisp status`, the TUI header, and a dismissible Web UI banner all point at `runwisp restart`.
- **Config errors that explain themselves.** Bad cron expressions fail at load with the expected grammar, unknown TOML keys suggest the closest valid one, and bad durations show the accepted syntax — same checks in `runwisp validate`, which now also prints advisory warnings. See [Tasks / Scheduling](https://docs.runwisp.com/configuration/tasks/#scheduling).
- **Plain-English schedules and status tooltips in the Web UI.** Cron renders humanized ("Every 5 minutes") with the raw expression in a tooltip, every run-status badge explains itself on hover, and empty states and the login modal say where to go next.
- **`?` help overlay in the TUI.** Every keyboard shortcut, grouped by context.
- **`runwisp demo`.** Boots a throwaway instance with a realistic config and hundreds of pre-seeded historical runs (with real on-disk logs) so you can explore the TUI and Web UI without writing a `runwisp.toml`. Everything lives in a temp directory that's deleted when the daemon stops; `--cloud` connects to the control plane instead. See [Quick start](https://docs.runwisp.com/getting-started/quick-start/).
- **Structured daemon logging.** `runwisp daemon` logs every run start, success, and failure with exit code, end reason, and duration. New `--log-level` / `--log-format` flags and `RUNWISP_LOG_LEVEL` / `RUNWISP_LOG_FORMAT` env vars; JSON output for log pipelines. See [Operations / Logging](https://docs.runwisp.com/operations/logging/).

### Changed

- **Task and service names may now contain `:`** (e.g. `[tasks."db:backup"]` — TOML needs the quotes). See [\[tasks.*\]](https://docs.runwisp.com/configuration/tasks/).
- **Log console matches the app theme.** ANSI colors in run output now map to the RunWisp palette, and the console chrome uses the design-system grays.
- **Breaking: `env_file` values are now visible in the API/UI** — use `secrets_file` for credentials.
- **Breaking: notifier `*_env` / `*_file` keys are gone.** Set `webhook_url`, `bot_token`, `password` directly, with `${...}` substitution for env vars and files.
- **CLI dead ends now point somewhere.** `runwisp exec <typo>` suggests matching task names, unreachable-daemon errors say how to start one, and first-run output links the docs.
- **Quieter daemon output.** HTTP access lines share the slog shape and destination; routine 200/401 traffic (health, SSE keepalives, anonymous polling) moves to `debug`; startup banner absorbs init warnings; redundant post-banner lines are gone; data-directory section collapses to one absolute `Data` line; Ctrl+C-triggered failures no longer surface as `WARN`/`ERROR`.

### Fixed

- **Run detail stats no longer overlap at narrow widths.** The Started / Duration / Exited box now reflows with the panel width instead of letting the date spill into the duration.
- **Refreshing the Web UI on a task whose name contains a dot no longer 404s.** The daemon mistook `/tasks/backup.daily` for a missing static asset instead of serving the dashboard.
- **`runwisp status` now talks to the daemon over the local Unix socket.** It works without a password and actually prints version/uptime — the old TCP path silently dropped the system-stats call as unauthorized.
- **Scroll-to-top in the log viewer no longer spins forever.** The first log page (`from=0`) was mistaken for an unset anchor and served the tail instead, so the top of a multi-thousand-line run never loaded.
- **The in-app notification stream no longer panics when notifications fail to initialize.** A nil hub leaked through as a non-nil interface and crashed `/api/notifications/stream`; it now falls back to a keepalive-only stream.

## [0.7.0] - 2026-05-27

### Added

- **Dark mode in the Web UI.** Header theme switch with Auto / Light / Dark; Auto follows the OS and updates live. Choice is remembered and applied before paint — no flash on load.
- **Full-text log search across a task's runs.** Press <kbd>/</kbd> in the TUI or <kbd>⌘K</kbd> / <kbd>Ctrl K</kbd> in the Web UI; also at `GET /api/tasks/{name}/log/search`. See [Logs / Search](https://docs.runwisp.com/concepts/logs/#full-text-search).
- **Docker Compose services.** Drop a `[compose.*]` table next to a `docker-compose.yml` and every service becomes a supervised, observable RunWisp service. See [`[compose.*]`](https://docs.runwisp.com/configuration/compose/) and the [migration guide](https://docs.runwisp.com/recipes/migrating-from-docker-compose/).

## [0.6.0] - 2026-05-22

### Added

- **Email (SMTP) notifications.** New `type = "smtp"` notifier with STARTTLS, implicit TLS, optional auth, and inline recipient overrides. See [Notifications / Email](https://docs.runwisp.com/notifications/providers/smtp/).
- **`runwisp service install` for systemd / launchd autostart.** Companions: `service uninstall`, `service status`, `--print` to emit the unit. See [Operations / Autostart](https://docs.runwisp.com/operations/autostart/).
- **Per-task `env` and `env_file`.** Inline values on `[tasks.*]` / `[services.*]` / `[defaults]` or dotenv from disk. `env_file` contents never leave the daemon — API and UI only show the path.
- **Prometheus `/metrics` endpoint (opt-in).** OpenMetrics 1.0 covering run counters, last-failure, active-run gauges, and daemon resource usage. See [Operations / Metrics](https://docs.runwisp.com/operations/metrics/).
- **Bulk run actions in the Web UI with undo.** Re-run, Cancel, Delete across selected rows; 5-second undo toast.
- **Delete a run from the Web UI and TUI.** Trash button on the run detail panel; <kbd>D</kbd> in the TUI exec view.
- **`[daemon] external_url` for notification deep-links.** Slack and Telegram messages include a direct link to the run.
- **Web UI sidebar collapses on narrow viewports.** Hamburger menu on phones and tablets.
- **Trigger-type icons next to every task.** Server (service), clock (cron), hand (manual).
- **Notification "View run →" chip.** Run-scoped in-app notifications carry a chip with the short run ID.

### Changed

- **Web UI design refresh.** Unified radius, tone, and shadow across panels; topbar connection pip on every page; runs list is server-paginated and virtualized.
- **Slack and Telegram messages got a facelift.** Headline with emoji + verb, exit code, duration, last 3 lines of output on failure, deep-link when [`[daemon] external_url`](https://docs.runwisp.com/configuration/daemon/#external_url) is set.
- **Default data directory is now `./.runwisp/`** (was `./data/`). Pass `--data data` to restore the old path; absolute paths are unaffected.
- **System resources panel shows live memory bytes.** `used / total` alongside the percentage.
- **OpenAPI exposes `EndReason` as a named schema.** Single shared enum instead of inline per call site.

### Fixed

- **Service runs no longer claim to be API-triggered.** Boot, crash, and slot-refill runs show **Service**; operator-initiated REST restarts still show **API**.
- **Graceful shutdown is instant when idle.** The retention ticker no longer holds the daemon open for the full `shutdown_timeout`.

## [0.5.0] - 2026-05-13

### Added

- **Local CLI/TUI connects over a Unix socket — no password needed.** `0600` socket in the data dir bypasses the password/JWT flow for local clients; remote clients still authenticate.
- **`runwisp password` prints the ephemeral boot password.** For a second-device Web UI login. Unix-socket only; operator-supplied passwords are never disclosed.
- **Download a run's full log from the Web UI and TUI.** Rotated + current segments delivered as one `text/plain` file. TUI keybinding: <kbd>d</kbd>.
- **`runwisp validate <path>` now uses the daemon's loader.** Same parser, same errors, same hints.
- **Resolved scheduler timezone visible in both UIs and `/health`.**
- **Inline notification targets in `notify_on_failure` / `notify_on_success`.** Use `"<id>:<target>"` to override a notifier's channel or `chat_id` per route.

### Removed

- **Password and JWT secret no longer written to disk (BREAKING).** Password comes from `RUNWISP_PASSWORD` or is regenerated each boot; the JWT key is derived via HKDF.
- **`data/password` file gone.**
- **`runwisp init` gone.** Running `runwisp` in an empty directory now scaffolds a starter file and launches the TUI in one step.
- **`runwisp run-task` gone.** `runwisp exec <task>` handles both daemon-dispatch and in-process modes.
- **`runwisp.example.toml` gone.** Starter `runwisp.toml` and the docs site are the single reference.

### Changed

- **`replica_index` → `instance_index` everywhere.** API, SQL, Web UI, TUI.
- **`append_notifiers` → `global_notifiers`.** Same precedence, clearer name.
- **`queue_size` removed from `[notify]`.** Fixed internal defaults.
- **`history_keep` defaults to `1024`; `history_keep_for` defaults to `90d`.**
- **Services can be permanently stopped from the Web UI and TUI.** Stop Service / Restart Service buttons control supervisor refill.
- **Run / Stop / Restart buttons are contextual** and disable when not applicable, with a tooltip.
- **`max_catch_up_runs` no longer capped at 10 000.**
- **Run log file paths use UTC timestamps.** DST flips no longer shift placement.
- **`parallelism` → `max_concurrent` on `[tasks.*]` (BREAKING).** Removed from `[services.*]`; replica count is `instances`.
- **`graceful_stop` governs every kill path.** Per-task field (default `"5s"`): SIGTERM, wait, SIGKILL — across timeouts, overlap, manual stop, and shutdown.
- **Daemon shutdown bounded by `[daemon] shutdown_timeout`** (default `"10s"`). Unfinished runs record `end_reason = "daemon_stopped"`.
- **Service restart backoff is configurable** via `[defaults] backoff_reset_after = "60s"` plus per-service override.
- **DST fall-back days no longer double-fire.** Suppressed firing records `end_reason = "dst_skipped"`.
- **`[scheduler] timezone` defaults to the host system timezone.** Falls back to `UTC` if undetectable.
- **Numeric config fields take literal values (BREAKING).** `"unlimited"`, `"inherit"`, `0`, and `-1` are gone; omit to inherit.
- **`log_on_full = "kill_task"` records killed runs as `log_overflow`.**
- **No log-index sidecars (`.idx`, `.tidx`) for runs under 1 024 lines.**
- **Pre-1.0 rename pass (BREAKING).** `retry_backoff` and `restart_backoff` share one enum (`constant` / `linear` / `exponential`); `runwisp exec` replaces `trigger`, `run`, and `run-task`; `[notify] disable_inapp` replaced by `[notify] append_notifiers`.
- **Log streaming redesigned around absolute line numbers (BREAKING).** `GET /log` paginates `{n, ts, stream, text}`; `GET /log/raw` concatenates segments; `GET /log/stream` emits `line | rotated | dropped | done` SSE events; native `Last-Event-ID` resume; tail-from-end via `?from=-1000`.

### Fixed

- **Outbound notifiers coalesce by default.** One Slack/Telegram message per failure burst plus a window-close summary. Opt out with `[notify] coalesce_outbound = false`.
- **Skipped runs record `end_reason = "skipped"`.** `on_overlap = "skip"` rejections no longer count as failures.
- **`RUNWISP_PASSWORD` no longer written back to `data/password`.**
- **`notify.history_keep_for` accepts day/week units** (`"30d"`, `"4w"`).
- **`min_free_space` honours `log_on_full = "kill_task"`** and fires a `log.disk_pressure` notification once per run.
- **SSE log stream drains large outputs.** >1 MB of un-emitted log data flushes fully before completion.
- **TUI and Web UI open long logs at the tail** in a single round-trip; older content loads on scroll-up.

## [0.4.0] - 2026-05-06

### Added

- **Notifications.** In-app alerts on every failed, timed-out, or crashed run; add a `[[notifier]]` block to route the same events to Slack or Telegram.

### Changed

- **Default HTTP port `8080` → `9477`.** Pass `--port 8080` to restore.

### Fixed

- **Version reported correctly everywhere.** CLI, TUI, and Web UI no longer show `0.0.0-dev` on released builds.
- **Durations and sizes serialise as integers.** `timeout`, `restart_delay`, `retry_delay`, `keep_for` are nanoseconds; `log_max_size` is bytes. Malformed values rejected at startup.

## [0.3.0] - 2026-04-29

### Added

- **Always-on services with `[services.NAME]`.** A new top-level section for long-lived processes; RunWisp keeps `instances` replicas alive and restarts each with exponential backoff.
- **Configurable restart backoff.** Per-service `restart_delay` and `restart_backoff`; replicas that run at least 60 s reset their backoff.
- **`POST /api/tasks/{name}/restart`.** Cancels every active replica; supervisor refills each slot. Restart Service buttons in Web UI and TUI.
- **`replica_index` on every run.** Persisted and surfaced in the API; multi-replica services suffix with `#N`.

### Changed

- **`restart = "always"` on `[tasks.NAME]` is now rejected.** Always-on belongs in `[services.NAME]`.
- **`runwisp list` shows `(service xN)`** alongside cron expressions and `(manual)`.

### Fixed

- **Services self-heal after manual stop or service-restart.** Supervisor refills each freed slot at its original replica index.
- **Hardened defaults for public deployments.** Owner-only data-dir perms, symlink-safe writes, explicit issuer/audience JWT claims, `Secure` cookie only on real TLS or trusted proxy; open `RUNWISP_TRUST_PROXY` ranges (`0.0.0.0/0`, `::/0`) are a startup error.
- **Noisy clients can't starve the daemon.** API request body cap 1 MiB, headers 64 KiB, log streams 64 global / 8 per IP, enforced against the real TCP peer.

## [0.2.0] - 2026-04-25

### Improved

- **Config format moved from YAML to TOML.** `runwisp.yaml` is no longer read; per-task fields flattened and renamed. `runwisp add` / `runwisp edit` are gone — edit the TOML file directly.
- **Shorter launch-ticket URLs.** 43 base62 chars instead of 64 hex — same ~256 bits of entropy.

### Added

- **Fullscreen log streaming in the TUI.** Press <kbd>f</kbd> to expand; line numbers disappear and text selection works with the mouse.

### Fixed

- **TUI sidebar navigation while viewing a run.** Enter on a sidebar item now consistently switches view.

## [0.1.2] - 2026-04-23

### Improved

- **Friendlier CLI error messages.** Login rejections, retry exhaustion, port conflicts, and rate-limits print formatted, coloured errors with concrete next steps.
- **Smaller footprint.** ~20% less idle memory (27 MB → 22 MB), ~22% smaller binary (23 MB → 18 MB), default `GOMEMLIMIT` of 128 MiB.
- **Faster, more reliable daemon startup.** Simplified coordination between health poller, log tailer, and PID watcher; fatal log lines abort startup immediately.
- **`runwisp add` / `runwisp edit` share one save path.**
- **Consistent SSE reconnect behaviour across UI streams.**

## [0.1.1] - 2026-04-22

### Added

- WebUI shows a "Connection Lost" panel with live downtime, retry countdown, and manual retry when the daemon is unreachable.
- Sidebar shows a live connection status indicator; clicking it when offline triggers an immediate reconnect.

### Fixed

- Port conflicts on daemon startup now name what's blocking the port instead of silently timing out.
- Daemon startup failures always print the log tail, regardless of how the process failed.
- Startup-time process exit is now distinguished from a runtime exit in error messages.

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

[Unreleased]: https://github.com/runwisp/runwisp/compare/v0.7.0...main
[0.7.0]: https://github.com/runwisp/runwisp/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/runwisp/runwisp/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/runwisp/runwisp/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/runwisp/runwisp/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/runwisp/runwisp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/runwisp/runwisp/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/runwisp/runwisp/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/runwisp/runwisp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/runwisp/runwisp/releases/tag/v0.1.0
