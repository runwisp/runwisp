# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed

- **`data/password` file removed.** The auto-generated daemon password now lives in SQLite under the `password` config row, protected by the data directory's `0700` perms. Backups of the data dir still need to be kept private (the DB itself contains the secret), but there is one less file to track. `RUNWISP_PASSWORD` still overrides the stored value and is still in-memory-only.
- **CLI reference, troubleshooting, upgrading, data directory, and debugging recipe docs removed.** The pages were drifting from the binary and out of scope for the MVP. The Web UI and TUI cover day-to-day operations; the auth page covers the one thing operators still need a write-up for.

- **`runwisp init` is gone.** Running `runwisp` in a directory without a `runwisp.toml` now offers to scaffold a small starter for you (`Create a starter with one example task? [Y/n]`) and continues straight into the TUI. The previous flow — a separate `runwisp init` command that wrote a 60-line file dominated by a commented schema reference, plus a "Generated password" that the daemon never actually used — is replaced by one prompt and a smaller, sharper starter file. Headless launches (`runwisp daemon`, Docker, systemd) skip the prompt and fall back to a built-in demo task with a warning, same as before; write `runwisp.toml` yourself (see `runwisp.example.toml`) to define real tasks.

### Added

- **Download a run's full log from the Web UI and the TUI.** The Web UI's run detail panel gains a **Download log** button next to the live-stream toggle; the TUI binds `d` to the same action on the exec view. The download is the rotated-out segment plus the current segment as one `text/plain` file — what you'd get from `cat *.log.prev *.log`, but in one click. The TUI download works over SSH and tmux, not just graphical sessions.

- **`runwisp validate <path>` now uses the same loader as the daemon.** A file the validator accepts is a file the daemon will accept — same parser, same rule set, same error messages. The success path prints `✓` and a summary (task / service / notifier counts plus the resolved scheduler timezone); the failure path prints each error with its TOML key and a `did you mean…?` hint where the migration is mechanical. Exits 0 on success, 1 on any error.

- **TUI startup and Web UI header show the resolved scheduler timezone.** Plain info, not a warning — `Europe/Bratislava (system)` if the daemon picked it up from the host, `(config)` if `[scheduler] timezone` was set explicitly. The same fields are surfaced on the health endpoint so a homebrewed reverse proxy or status check can read them.

- **Inline notification targets in `notify_on_failure` / `notify_on_success`.** A token of the form `"<id>:<target>"` overrides the parent notifier's channel (Slack) or chat_id (Telegram) for that route only — `notify_on_failure = ["slack-ops:#deploys"]` reuses the credentials of `slack-ops` but posts to `#deploys`. Bare ids and the literal `"inapp"` keep working unchanged. Notifier ids are now disallowed from containing `:` (the new separator); rename any existing colon-bearing ids before upgrading.

### Changed

- **`parallelism` → `max_concurrent` on `[tasks.*]` (BREAKING).** The new name matches what the field actually means: the maximum number of overlapping runs of a task. The field is **removed entirely from `[services.*]`** — replica count is `instances`, and there is no other concurrency knob on a service. Configs that still spell `parallelism` are rejected at config load with a `did you mean 'max_concurrent'?` hint.

- **`graceful_stop` governs every kill path.** New per-task field, default `"5s"`. When the daemon needs to stop a running task — `timeout`, `on_overlap = "terminate"`, manual stop, or daemon shutdown — it sends `SIGTERM` to the task's process group, waits up to `graceful_stop`, then `SIGKILL`s the survivors. Set `"0s"` to keep the previous abrupt-kill behaviour. The previous unconditional immediate-`SIGKILL` is gone; long-running tasks get a chance to flush their state.

- **Daemon shutdown is bounded by `[daemon] shutdown_timeout`.** New top-level field, default `"10s"` (matches Docker's stop grace period). On `SIGTERM` the daemon fans out per-task shutdown in parallel, waits up to `shutdown_timeout` for everything to settle, then `SIGKILL`s the rest and exits. Boot emits a warning if any task's `graceful_stop` exceeds `shutdown_timeout`, since that task can never finish its grace window during a daemon stop. Unfinished runs are recorded with `end_reason = "daemon_stopped"`.

- **Queue depth is bounded by `queue_max`.** New per-task field, default `100`. When `on_overlap = "queue"` and `max_concurrent` is saturated, the queue accepts new firings up to `queue_max`; further firings are recorded with `end_reason = "queue_full"` (and exit code `-1`) instead of the queue growing without bound. `queue_max = 0` is rejected — use `on_overlap = "skip"` if that's what you want.

- **Service restart backoff is configurable.** New `[defaults] backoff_reset_after = "60s"` plus per-service override. A replica that runs at least this long resets its restart counter, so a service that finally stabilises returns to fast-restart behaviour on its next failure. Replaces the previously hardcoded 60-second threshold.

- **DST fall-back days no longer double-fire (BREAKING).** On the autumn transition, `0 2 * * *` used to fire twice — once before the rewind, once after. The scheduler now dedupes by wall-clock minute: a firing whose minute matches the previous one is recorded with `end_reason = "dst_skipped"` and the underlying command is not run. Spring-forward firings continue to fire at the next valid local time.

- **`[scheduler] timezone` defaults to the host's system timezone.** When the field is omitted, the daemon picks up the host's IANA zone and falls back to `UTC` only if it can't be detected. The resolved zone is surfaced in the TUI banner and the Web UI header so the operator can see what's in effect. Per-task `timezone` overrides still work.

- **Numeric config fields take literal values; no more keywords or sentinels (BREAKING).** Every numeric field — `keep_runs`, `keep_for`, `log_max_size`, `max_concurrent`, `queue_max`, `max_catch_up_runs`, `retry_attempts`, `instances` — accepts an integer (or duration / size). The `"unlimited"` and `"inherit"` keywords are gone; the magic-int sentinels (`0` = inherit, `-1` = unlimited) are gone; **omit the field to inherit the default**. Each field has a hard internal cap (`max_concurrent` 1024; `queue_max` and `max_catch_up_runs` 10 000; `retry_attempts` 100; `keep_runs` 1 000 000; `instances` 1–64). Out-of-range values are rejected at config load with a `did you mean N?` hint.

- **`log_on_full = "kill_task"` records killed runs as `log_overflow`.** A new end reason that names exactly what happened, instead of generic `failed` or `stopped`. Retries, `notify_on_failure`, and dashboards still treat it as a failure.

- **Log index sidecars (`.idx`, `.tidx`) are no longer created for short runs.** A run that emits fewer than 1 024 lines now leaves no sidecar files on disk; the index is only opened when there's actually something to index. A `runwisp.toml` full of small tasks no longer creates 4 × N sidecar files for runs that don't need them.

- **Schema and CLI tidy-up before 1.0 (BREAKING).** A coordinated rename pass that removes accumulated naming inconsistencies. None of these change what the daemon can do — only how you spell it.
  - `retry_backoff` and `restart_backoff` now share the same enum: `constant` / `linear` / `exponential`. The old `""` (constant) and `"none"` (constant) values are rejected. Services gain `linear` as a valid restart curve.
  - The CLI is reorganised around two clear verbs: `runwisp exec <task>` runs in-process (and now refuses with an error when a daemon is already attached to the same data dir, instead of silently opening SQLite as a second writer); `runwisp run-task <task>` triggers via the running daemon's REST API. The old `runwisp trigger` and `runwisp run` are removed; `runwisp daemon` remains the headless launcher for systemd/Docker.
  - Notification model: `[notify] disable_inapp = true` is replaced by `[notify] append_notifiers = []`. The same setting now also lets you redirect the zero-config catch-all (e.g. `append_notifiers = ["slack-ops"]` to make every failure page Slack instead of, or alongside, the in-app bell). Per-task `notify_on_failure` / `notify_on_success` continue to work; the contents of `append_notifiers` are appended to each per-task list (deduped against the explicit ids), so the bell keeps lighting up unless you opt out.

- **Log streaming redesigned around absolute line numbers (BREAKING).** The REST and SSE log surface now speaks lines, not bytes. New endpoints:
  - `GET /api/tasks/{name}/runs/{id}/log` — JSON page of `{n, ts, stream, text}` entries with `from`/`limit` query parameters (`from` accepts negative values for tail-from-end).
  - `GET /api/tasks/{name}/runs/{id}/log/raw` — concatenates the rotated-away segment and the current segment as `text/plain` for download / `cat` / `grep`.
  - `GET /api/tasks/{name}/runs/{id}/log/stream` — line-numbered SSE with `event: line | rotated | dropped | done`. Each event carries an `id:` so EventSource's native `Last-Event-ID` resume works on reconnect.
  The previous `/log-stream` endpoint and the `start_line` / `end_line` / `tail` query shape on `/log` are removed. A single SSE call now serves "tail then follow" via `?from=-1000`; lines longer than 64 KB are split with `continued: true` on segments 2..N.

### Fixed

- **Outbound notifiers (Slack, Telegram) now coalesce by default.** A flapping task used to translate to one Slack message per failure — exactly the surface that pages humans and gets rate-limited by the provider. Outbound deliveries now share the in-app coalescer's fingerprint (task name + event kind + end reason): the first event in `coalesce_window` (default `1h`) is delivered immediately, repeats are suppressed, and either the Nth event (`occurrence_ring`, default `10`) or a window-close summary fires with `coalesced_count` so the operator still sees the rhythm. Set `[notify] coalesce_outbound = false` to opt out.
- **Skipped runs are now recorded as `end_reason = "skipped"`.** A run that `on_overlap = "skip"` rejects used to be persisted as `end_reason = "failed"`, which made a `* * * * *` health probe with chronic overlap pose as a real failure to dashboards, retries, and `notify_on_failure` (Slack/Telegram). The new `skipped` end-reason is terminal, distinct from `failed`/`stopped`/`crashed`, never triggers retries, and never fires failure notifications.
- **Slack and Telegram setup docs no longer require root.** The provider pages used to walk readers through `sudo install -d -o runwisp …` for a system-wide secrets directory. The recommended path is now a user-owned `~/.config/runwisp/` (or an env var managed by Docker / systemd), matching how a solo operator actually deploys RunWisp.
- **`RUNWISP_PASSWORD` is no longer written back to `data/password`.** Operators who pass the password via Docker secrets, systemd `LoadCredential=`, or sealed-secrets do so specifically to keep credentials off disk; the daemon used to mirror the env var into `data/password` on every start, defeating that intent. The env var is now in-memory only. The TUI and CLI must obtain the password the same way (env var or `--password`) when no `data/password` file is present — falling back to the file from a different shell would have been misleading anyway.
- **`notify.history_keep_for` now accepts day and week units.** The docs and the example config advertised `"30d"` for in-app notification retention, but the parser rejected anything beyond Go's stock duration syntax (`h`/`m`/`s`). The field now uses the same extended parser as `keep_for`, so `d` and `w` work everywhere a duration sets a retention window.
- **`min_free_space` no longer silently overrides `log_on_full = "kill_task"`.** When the disk-pressure threshold trips during a run, the daemon now honours the task's overflow policy: `kill_task` cancels the run (loud failure, as the operator chose); `drop_new` and `drop_old` keep running but stop logging. In all cases the daemon raises a new `log.disk_pressure` notification (severity `warn`, fired once per run, with `free_bytes` / `min_free_bytes` / `killed_task` in the payload) so the silenced output is never invisible.
- **SSE log stream no longer cuts off large outputs.** When a run produced more than 1 MB of un-emitted log data before finishing, the SSE stream would emit a single 1 MB chunk and immediately send `done`, discarding the rest. The stream now drains the full log file before signalling completion.
- **TUI and Web UI open long logs at the tail.** Opening a finished execution with a large log used to replay the entire file from the start, making the operator wait for the viewport to catch up. Both UIs now land at the tail in a single round-trip and lazily load older content when the user scrolls up.

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
