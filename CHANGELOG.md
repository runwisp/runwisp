# Changelog

All notable changes to RunWisp will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`sudo runwisp takeover` retires cron in one command** — imports your crontabs, installs RunWisp as a service, and disables cron, with `--dry-run` and `--yes` available. See [Retiring cron](https://docs.runwisp.com/replacing-cron/take-over-from-cron/).
- **First run on a root Linux box can now do the full cron cutover in one prompt.** See [Retiring cron](https://docs.runwisp.com/replacing-cron/take-over-from-cron/#the-first-run-prompt).
- **`[daemon] include_cron` turns your existing crontabs into live RunWisp tasks** — with captured output, history, and notifications, nothing rewritten. See [`include_cron`](https://docs.runwisp.com/configuration/daemon/#include_cron).
- **First run now detects an existing crontab and offers to import it**, alongside the existing docker-compose detection. See [Take over from cron](https://docs.runwisp.com/replacing-cron/take-over-from-cron/).
- **`sudo runwisp service install` now flags a still-running cron** and points you to `takeover`. See [Retiring cron](https://docs.runwisp.com/replacing-cron/take-over-from-cron/).
- **`runwisp import` now reports every job in the crontab** — mapped cleanly, changed, needs a fix, or already yours — with `--dry-run` to preview first. See [Converting crontabs](https://docs.runwisp.com/replacing-cron/converting-crontabs/#the-one-liner).
- **`runwisp import --write`** keeps imported jobs in their own file, separate from your hand-written config. See [Converting crontabs](https://docs.runwisp.com/replacing-cron/converting-crontabs/).
- **`runwisp promote`** moves an imported or crontab-sourced task into your own `runwisp.toml`. See [CLI](https://docs.runwisp.com/operations/cli/#take-ownership-of-a-task-with-promote).
- **Official Docker images** on Docker Hub: `runwisp/runwisp` (Alpine + Debian, amd64/arm64). See [Docker](https://docs.runwisp.com/getting-started/docker/).
- **`scripts/install.sh`**, the script behind `curl -fsSL https://get.runwisp.com | sh`: detects your OS/arch, verifies the download, and installs the binary. See [Quick start](https://docs.runwisp.com/getting-started/quick-start/).
- **New `sendmail` notifier** sends email through the machine's own mail server — no relay host or credentials needed. See [Email (local MTA)](https://docs.runwisp.com/notifications/providers/sendmail/).
- **Tasks now show where they came from** — your own TOML, an import, or a crontab — in `runwisp list`, `status --json`, the API, dashboard, and TUI.
- **Config warnings now show up live** in `runwisp reload`, `status`, `/api/info`, and the TUI, not just at boot. See [Reload](https://docs.runwisp.com/operations/reload/).
- **`compose_mode`**: run a task's command inside an already-running Compose service, or start a fresh one. See [`compose_mode`](https://docs.runwisp.com/configuration/compose/#running-a-command-in-a-service-compose_mode).
- **`RUNWISP_CONFIG`, `RUNWISP_DATA`, `RUNWISP_HOST`, `RUNWISP_PORT`, `RUNWISP_TLS` environment variables** for the matching CLI flags and TOML settings. See [`[daemon]`](https://docs.runwisp.com/configuration/daemon/).
- **`env_base = "clean"`** starts a task with a minimal environment instead of inheriting your shell's. See [Tasks](https://docs.runwisp.com/configuration/tasks/#working-directory--shell).
- **`working_dir = "~"`** resolves to the home directory of whoever runs the task. See [Tasks](https://docs.runwisp.com/configuration/tasks/#working-directory--shell).
- **Running as root now defaults to system paths** (`/etc/runwisp/runwisp.toml`, `/var/lib/runwisp`). See [CLI](https://docs.runwisp.com/operations/cli/#global-flags).

### Changed

- **Jobs already running under a live system cron are now held, not double-scheduled**, until cron is retired — visible in `status`, `validate`, the TUI, and the dashboard. See [Held jobs](https://docs.runwisp.com/replacing-cron/held-jobs/).
- **Multi-line `run` scripts now stop at their first failing command** instead of finishing and reporting success anyway. Opt out with `set +e`. See [Fail-fast](https://docs.runwisp.com/configuration/tasks/#fail-fast).
- **New "runbook" visual design across the web UI and docs**: monospace headings, hairline borders, teal accent, flat dark mode. No behavior changes.
- **Redesigned login screen**, with the RunWisp brand, a cleaner password field, and a "Where's my password?" popover.
- **`runwisp service install` now installs system-wide by default**; use `--local` for a per-user install. See [Autostart](https://docs.runwisp.com/operations/autostart/).
- **The daemon and web UI are now licensed GPL-3.0-or-later** (previously Apache-2.0); shared libraries stay Apache-2.0. See [LICENSE](LICENSE).
- **Removed most hover/focus/state animations** — the UI now responds instantly. Motion stays only where it's meaningful (spinners, live-run pulse, progress bars).
- **Geist Mono now ships embedded in the binary**, so identifiers (task names, run IDs, exit codes) render in the same monospace font everywhere.
- **`run` can now be combined with `compose_file`**, running inside the service's existing container. See [`compose_mode`](https://docs.runwisp.com/configuration/compose/#running-a-command-in-a-service-compose_mode).
- **`runwisp import cron` now scopes a crontab's settings to that crontab's own tasks**, instead of applying them globally. See [How cron maps to TOML](https://docs.runwisp.com/replacing-cron/cron-mapping/#the-mapping-table).
- **Imported jobs skip catch-up runs, like cron did** — a missed run is still recorded, just not re-fired. See [How cron maps to TOML](https://docs.runwisp.com/replacing-cron/cron-mapping/#the-mapping-table).
- **`runwisp import --quiet` now only silences clean imports** — jobs that need a fix are still listed. See [CLI](https://docs.runwisp.com/operations/cli/#migrate-an-existing-setup).
- **`[daemon] tls` now defaults to `"off"`.** A non-loopback bind used to self-sign a certificate and serve HTTPS automatically; it now serves plain HTTP unless you opt in with `tls = "auto"` (self-signed HTTPS) or supply `tls_cert`/`tls_key`. See [`[daemon]`](https://docs.runwisp.com/configuration/daemon/#tls-tls_cert-tls_key).

### Fixed

- **`runwisp exec` no longer reports success for a run that failed within milliseconds.** See [CLI](https://docs.runwisp.com/operations/cli/#run-a-task-now).
- **A cron day-of-week of `7` (Sunday) is now accepted**, fixing skipped weekly jobs on stock Debian/Ubuntu boxes. See [How cron maps to TOML](https://docs.runwisp.com/replacing-cron/cron-mapping/#the-mapping-table).
- **`runwisp status` and the TUI header now show the live task set**, not the one from when the daemon started.
- **`runwisp service install --dry-run` now runs its checks before reporting success.** See [Autostart](https://docs.runwisp.com/operations/autostart/#flag-reference).
- **`runwisp service` commands now respect `--config`/`--data` and their environment-variable equivalents.** See [Autostart](https://docs.runwisp.com/operations/autostart/#resolving-paths).

### Security

- **Compose task secrets are now passed via environment variables, never on the command line.** See [Compose](https://docs.runwisp.com/configuration/compose/).
- **A control-plane peer can no longer override a task's environment** unless `[daemon] allow_cloud_dispatch` is on, and can't overwrite a task defined in `runwisp.toml`.
- **`/api/instance` no longer responds to requests via a reverse proxy**, keeping local paths private. See [Reverse proxies](https://docs.runwisp.com/operations/auth/#reverse-proxies).

## [0.13.2] - 2026-07-26

### Fixed

- **`runwisp import cron` reads the user column off a system crontab's `@reboot` line.** A `@reboot root /usr/bin/warmup` in `/etc/crontab` imported the whole `root /usr/bin/warmup` as the command; the descriptor form now splits the user column the same way the five-field form does. See [How cron maps to TOML](https://docs.runwisp.com/replacing-cron/cron-mapping/#the-mapping-table).
- **`runwisp import cron` applies cron's `%` rules instead of copying the command verbatim.** An unescaped `%` ends a crontab command and pipes the rest to the job on stdin, and `\%` is a literal `%` — neither means that to a shell, so a verbatim import ran a command cron never ran. Commands are now imported as cron would run them; where that loses stdin input RunWisp can't express, the input is quoted in a `# TODO` on the `run` line. See [How cron maps to TOML](https://docs.runwisp.com/replacing-cron/cron-mapping/#the-mapping-table).
- **`runwisp import cron` reproduces cron's working directory.** Cron runs a job in the home directory of the user running it; RunWisp defaults to the daemon's, so a `run` script using relative paths silently wrote somewhere else. Every imported job now gets `working_dir = "~"`, which resolves to the home of whoever the job runs as — including a system crontab's user column. See [How cron maps to TOML](https://docs.runwisp.com/replacing-cron/cron-mapping/#the-mapping-table).
- **`runwisp import cron` no longer invents a user for a system crontab line that has none.** A `/etc/cron.d` line written without its user column split six ways anyway, so `/usr/bin/foo` landed in `user =` and the rest of the line in `run =`. The config loaded, the summary said the job was clean, and it then failed once per firing as `unknown user "/usr/bin/foo"`. Such a line is now skipped — as cron itself skips it — and reported as needing a fix. See [How cron maps to TOML](https://docs.runwisp.com/replacing-cron/cron-mapping/#what-needs-a-human).
- **`runwisp import` no longer turns a `${VAR}` in your old config into a RunWisp substitution.** Neither cron nor supervisord expands `${...}`, but RunWisp's config loader does — so a `${DB}` in a crontab comment or a `VAR=${OTHER}` line imported as a config the daemon refused to load, or, if that variable happened to exist in whatever shell launched the daemon, as a value the old scheduler would never have produced. Imported text is now escaped so it arrives verbatim. See [Substitution](https://docs.runwisp.com/configuration/substitution/).
- **`runwisp import cron` no longer writes a timezone it can't use.** A crontab whose `CRON_TZ` names a zone that doesn't exist produced a config that looked fine in the summary and then failed to load; the affected jobs are now flagged with a `# TODO` and reported as needing a fix.
- **A supervisord key the importer doesn't recognise is named in the summary** instead of being dropped without a word.
- **`runwisp import supervisord --write` dedupes against your config like `import cron` does.** Re-importing a supervisord config after promoting one of its programs into your root TOML now skips the program you already own instead of failing the whole import on a duplicate name. See [Converting crontabs](https://docs.runwisp.com/replacing-cron/converting-crontabs/).
- **An import into an already-broken config says so** instead of reporting it as a conflict with the incoming jobs. When nothing is written, the message points at the pre-existing problem rather than at a clash that doesn't exist; and when the import keeps its files because its own content carries a `# TODO`, the summary now names the config's separate breakage too — otherwise you'd fix every TODO, run `runwisp validate`, and be told about something you never touched.
- **Wiring `[daemon].include` is not fooled by a `[daemon]` line inside a multi-line `run` script**, and rewriting a config keeps the file's existing permissions rather than widening them to the default.
- **Reload no longer treats a task moving between config files as a change.** A task's provenance (whether it lives in the import staging file) is derived, not part of its definition, so a reload after `runwisp promote` reports no task changes, leaves a promoted service running instead of recycling it, and still refreshes the provenance the UI reports. See [Reload](https://docs.runwisp.com/operations/reload/#what-reloads-live-and-what-needs-a-restart).

- **A second daemon started against a data dir already owned by a live daemon refuses to start**, and PID-file cleanup removes the file only while it still holds that daemon's own PID, so a running daemon stays managable via `stop`/`restart`/`reload`.
- **`stop` and `restart` confirm the recorded PID still belongs to a RunWisp daemon before signalling it**, so a recycled stale PID leaves an unrelated process untouched.
- **The background daemon spawned by the launcher (and `runwisp demo`) inherits the launcher's `--host` and `--socket`**, binding where the operator probed instead of defaulting to loopback and the default socket path.
- **TUI log search opens the highlighted result on Enter** and scrolls the result list to keep the selected hit in view when there are more matches than fit the window.
- **The TUI notification panel keeps its cursor on the highlighted notification** when a newer one streams in above it, so open and mark-read act on the intended row; the collapsed summary truncates by display width so styled output stays intact on narrow terminals.
- **The web UI run count decrements once per deletion**, keeping the total accurate when the optimistic removal and the `run.deleted` event both fire for the same run.
- **The web UI overview merges snapshot fetches through the same phase-order guard as live updates**, so a finished run keeps its final status when a fetch resolves with an older view.
- **The web UI keeps the freshest data when overlapping fetches resolve out of order.**
- **The SSE log-stream drop counter is guarded against concurrent access** between the publishing goroutine and the stream handler.

## [0.13.1] - 2026-07-24

### Fixed

- **Compose per-service `notify_on_failure`/`notify_on_success` overrides now reach notifications.** A `[compose.<alias>.<svc>]` notify list was parsed and discarded; it now desugars into a notify route keyed by the imported service's task name, exactly like `[services.*]`. See [Compose](https://docs.runwisp.com/configuration/compose/#per-service-overrides).
- **Retention skips non-terminal runs.** Age- and count-based cleanup now only prunes runs that have ended, so a run still pending or running is never removed while in flight. See [Retention](https://docs.runwisp.com/configuration/tasks/#retention).
- **Catch-up honors a task's timezone.** Missed-tick detection at startup evaluates each task's schedule in its own timezone (or the scheduler default) instead of the host's local zone. See [Missed runs](https://docs.runwisp.com/configuration/scheduling/#missed-runs).
- **Non-service `restart` backoff now escalates.** Consecutive restarts of a non-service task apply the configured `restart_backoff` curve instead of repeating at the flat base delay. See [Tasks](https://docs.runwisp.com/configuration/tasks/).
- **Negative `timeout`, `retry_delay`, and `restart_delay` are rejected at config load** instead of being silently ignored. See [Tasks](https://docs.runwisp.com/configuration/tasks/).
- **`docker compose` availability is re-probed after a transient failure**, so a task using compose is no longer disabled for the daemon's lifetime when the first probe fails while Docker is still starting.
- **A malformed Docker build response stream now fails the build** rather than spinning on the undecodable bytes.
- **A container start cancelled mid-flight no longer leaks its container or image** — cleanup runs on a context detached from the cancelled run.
- **Terminal events for runs that never execute are published off the runtime lock**, removing a re-entrancy deadlock reachable on skip-overlap, queue-full, catch-up, and DST-fallback paths.
- **Assorted concurrency fixes** in run event publishing, the notification ingress path and outbound coalescer shutdown, and the cloud execution-update buffer and reconnect backoff.
- **Log search now handles non-ASCII task output correctly**, and the cursor-based result window advances past the first 50 runs.
- **Shell tasks no longer receive the daemon's own internal environment variables** (`RUNWISP_*`), matching the container and compose backends.
- **Cloud log-archive uploads now validate the destination URL** before connecting.
- **An unrecognized system timezone now falls back to UTC** instead of failing daemon startup.
- **Byte-size parsing now rejects non-finite and overflowing values.**
- **Rate-limited notification channels now clamp their `Retry-After` wait to the backoff budget.**
- **Concurrent notification publish and unsubscribe no longer race.**
- **Restoring soft-deleted runs by filter now returns only the rows it restored.**

## [0.13.0] - 2026-07-21

### Added

- **`--json` output for `runwisp status`, `list`, and `validate`.** A schema-versioned, machine-readable document on stdout for headless and agent-driven use; failures still exit non-zero and emit JSON. See [CLI](https://docs.runwisp.com/operations/cli/#machine-readable-output-json).
- **JSON Schema for `runwisp.toml`.** `runwisp schema` prints it (also published at `https://docs.runwisp.com/config.schema.json`); scaffolded and imported configs carry a `#:schema` line so editors validate and autocomplete them. See [Driving with an AI agent](https://docs.runwisp.com/operations/agents/).
- **`runwisp exec --json`** prints a run's outcome (run id, status, exit code, duration, failed) as one JSON document on stdout, diverting log lines to stderr; a run that can't start (unknown task, unreachable daemon) emits an `error` document instead of nothing. See [CLI](https://docs.runwisp.com/operations/cli/).
- **`runwisp agent-guide`** prints a paste-ready snippet for your project's `AGENTS.md`/`CLAUDE.md` so an AI coding agent knows how to drive RunWisp. See [Driving with an AI agent](https://docs.runwisp.com/operations/agents/).

### Changed

- **`runwisp validate --json` errors now carry a structured location** (`key`, `line`, `column`) for parse-time failures, so tooling can point at the offending site without parsing the message. See [CLI](https://docs.runwisp.com/operations/cli/#machine-readable-output-json).
- **Readable daemon logs.** Log lines now render as `2026-05-27 14:03:01 [INFO] message key=value` — colored on an interactive terminal, plain otherwise — instead of Go's terse `level=INFO msg=…` logfmt. `--log-format=json` is unchanged for pipelines. See [Logging](https://docs.runwisp.com/operations/logging/).
- **Colored, readable `--help` and errors.** Help pages are styled in the RunWisp brand palette, and every error — a typo like `runwisp install`, a bad config, a failed `runwisp password` — shares one branded `ERROR` block instead of a raw log line; plain text when piped or under `NO_COLOR`. See [CLI](https://docs.runwisp.com/operations/cli/).
- **`runwisp service install --data .` installs into the current directory.** A relative `--data` is resolved to its absolute path and baked into the unit instead of being rejected, and declining the suggested data location now offers the current directory rather than erroring out. See [Autostart](https://docs.runwisp.com/operations/autostart/).

### Fixed

- **Deep-linking to a deleted or unknown run now shows a "Run not found" state** instead of silently displaying a different run under the dead URL. See [Web UI tour](https://docs.runwisp.com/getting-started/web-ui-tour/).
- **The Web UI header no longer pushes the theme toggle and notification bell off-screen** on phone-width viewports; informational chips collapse and the controls stay reachable. See [Web UI tour](https://docs.runwisp.com/getting-started/web-ui-tour/).
- **The login screen distinguishes rate-limiting from a wrong password** — after too many attempts it tells you to wait instead of reporting "Invalid password". See [Auth](https://docs.runwisp.com/operations/auth/).
- **The TUI's quick status filter (`f`) now also reaches Skipped and Stopped**, matching the Web UI's status buckets. See [the TUI tour](https://docs.runwisp.com/getting-started/tui-tour/).
- **`runwisp demo` rejects `--config`/`--data` it can't honor** instead of silently discarding them; use `--seed-only` to seed your own paths. See [CLI](https://docs.runwisp.com/operations/cli/).
- **Task run-parameter form labels are wired to their inputs**, so clicking a label focuses its field and screen readers associate the two. See [Parameters](https://docs.runwisp.com/configuration/tasks/#parameters).
- **`runwisp exec` no longer exits before a just-triggered run's output appears.** A freshly triggered run is handed back before its record is durably persisted, so the log stream could momentarily find no run and close empty; `exec` treated that as "the run produced nothing" and exited without printing its output. It now retries until the run becomes streamable.
- **A plain `runwisp daemon` is no longer misreported as service-managed.** Detection dropped the unreliable systemd `INVOCATION_ID` heuristic (inherited by every process in a desktop terminal) and keys solely on the marker our generated units set, so the TUI quit dialog and cloud self-restart no longer treat a hand-launched daemon as init-managed. See [Autostart](https://docs.runwisp.com/operations/autostart/).
- **A cloud `service:apply` can no longer overwrite a non-service task's command**, and its instance count is capped like a TOML service — the control plane can't rewrite what a cron or one-shot task runs, nor start an unbounded fleet.
- **Cloud-dispatched runs and services are cleaned up once they finish.** Ad-hoc executions are reaped after they retire, and a new `service:remove` tears down a cloud-declared service, instead of stranding a supervisor goroutine per dispatched name.
- **Container tasks honor `graceful_stop` and `stop_signal` on stop.** A stopping container is sent its configured signal and given the grace window before being force-killed, matching shell and compose tasks. See [Tasks](https://docs.runwisp.com/configuration/tasks/).
- **Reloading to drop then re-add a task while a run is still draining no longer deletes the revived task or stalls its queue.** See [Reload](https://docs.runwisp.com/operations/reload/).
- **Untrusted values in the daemon log can't inject terminal escape sequences** — control bytes in a logged field (e.g. a task name from an HTTP body) render as visible escapes rather than raw bytes. See [Logging](https://docs.runwisp.com/operations/logging/).
- **A coalesced notification that fails at its window-close flush now raises an in-app alert**, matching uncoalesced failures, instead of failing silently. See [Notifications](https://docs.runwisp.com/notifications/model/).
- **The failed-run summary no longer omits `start_failed` runs**, so the metrics counter and the TUI "Failed" tally agree. See [Metrics](https://docs.runwisp.com/operations/metrics/).
- **Opening a data directory written by a newer RunWisp fails with a clear error** instead of silently running an older binary against a schema it doesn't understand.
- **`runwisp validate --json` reports every configuration problem in one pass**, each with its own location, instead of stopping at the first. See [CLI](https://docs.runwisp.com/operations/cli/#machine-readable-output-json).
- **`runwisp list --json` emits `tasks: []` (never `null`) on a config-load error**, matching `status` and `validate`. See [CLI](https://docs.runwisp.com/operations/cli/#machine-readable-output-json).
- **`runwisp exec --json` keeps a run's real exit code when the trailing status fetch fails** — the outcome captured during the follow is reused instead of being masked as a failure. See [CLI](https://docs.runwisp.com/operations/cli/).

## [0.12.0] - 2026-07-08

### Added

- **`runwisp tui --url` connects to a remote daemon over HTTP.** Attach the TUI to a daemon on another host or in a container — it logs in with the daemon's password (no `--password` flag; prompted without echo or read from `RUNWISP_PASSWORD`, then cached) and "Open Web UI" works against it too. See [the TUI tour](https://docs.runwisp.com/getting-started/tui-tour/#authentication-briefly).
- **HTTPS by default off loopback.** Binding beyond `127.0.0.1` now self-signs a certificate and serves TLS automatically — no setup, no proxy required; the CLI/TUI pin the cert on first use and the startup log prints its fingerprint. Bring your own cert with `tls_cert`/`tls_key`, or opt out with `tls = "off"`. See [`[daemon]`](https://docs.runwisp.com/configuration/daemon/#tls-tls_cert-tls_key).

### Changed

- **Restart a running service in one click.** A running service now shows a direct **Restart** button (alongside **Stop**) instead of only revealing it after a stop — restarting is one confirmed action, not stop-then-restart. See [Web UI tour](https://docs.runwisp.com/getting-started/web-ui-tour/).
- **Selecting a run scrolls its row into view.** Opening a run from a deep link or the detail panel now brings its row on screen in the (virtualized) run list, so the highlight is always visible; an already-visible selection doesn't move. See [Web UI tour](https://docs.runwisp.com/getting-started/web-ui-tour/).
- **Login hardened with PBKDF2.** The password challenge-response now derives its answer with PBKDF2-HMAC-SHA256 (600,000 rounds) instead of a single hash, making a captured login transcript far costlier to brute-force offline. See [Auth](https://docs.runwisp.com/operations/auth/#network-clients-web-ui-remote-rest).
- **Auth rate limiting keys off the real TCP peer.** The per-IP throttle on the login endpoints ignores client-supplied `X-Forwarded-For`/`X-Real-IP` headers, so it can't be sidestepped by rotating them; real client IPs behind a configured trusted proxy (`RUNWISP_TRUST_PROXY`) are still honored. See [Auth](https://docs.runwisp.com/operations/auth/).
- **HTTP-task SSRF guard covers Alibaba Cloud and Oracle Cloud metadata.** Alongside private, loopback, and link-local targets (including the `169.254.169.254` metadata IP shared by AWS/Azure/GCP), the guard now also rejects `100.100.100.200` and `192.0.0.192`, which sit in otherwise-routable ranges. See [HTTP tasks](https://docs.runwisp.com/configuration/tasks/).
- **Session key derivation is as costly as the login itself.** The JWT signing key is now derived from the password with PBKDF2-HMAC-SHA256 (600,000 rounds) instead of a single fast hash, so a captured session token is no cheaper to brute-force offline than a login transcript — closing a shortcut around the login's PBKDF2 hardening on TLS-less deployments. Upgrading rotates the key, so existing browser sessions must log in once more. See [Auth](https://docs.runwisp.com/operations/auth/).
- **Launch-ticket redirect rejects backslash open-redirects.** The optional post-login `redirect` target now drops paths containing a backslash (e.g. `/\evil.com`), which browsers normalize into a scheme-relative `//evil.com`; only genuine same-origin paths are honored.
- **A running execution's duration ticks every second.** The "Ran for" readout in the Web UI run detail now counts up live while a run is in-flight (optimistic, client-side) and freezes at the wall-clock total when it ends, instead of only updating on the next event. See [Web UI tour](https://docs.runwisp.com/getting-started/web-ui-tour/).
- **Web UI is push-driven, over one SSE connection shared across all tabs.** A single `/api/stream` feed (run lifecycle, system samples, config-staleness, notifications) replaces timer polling and the stream-per-concern model; an elected leader tab holds the one connection and rebroadcasts to the rest, so any number of open tabs can't exhaust the browser's per-origin connection limit. If live updates ever do stall, the UI flags it ("Updates paused") and recovers on its own.

## [0.11.0] - 2026-06-26

### Added

- **TUI task & run inspector (`i`).** An on-demand panel showing a task's definition and recent health (success rate, last failure), or a run's full facts (exit code, trigger, retry lineage) in a log view — surfaced on a keypress instead of crowding the header. See [the TUI tour](https://docs.runwisp.com/getting-started/tui-tour/).
- **Bulk run cleanup in the TUI.** Multi-select runs with `space` (or `a` to select every run matching the current filter), then delete, cancel, or re-run them at once; the delete is undoable.
- **Reload `runwisp.toml` from the TUI (`R`).** The same validate-first reload as `runwisp reload`, without leaving the TUI. See [Reload](https://docs.runwisp.com/operations/reload/).
- **Progress bars and live redraws render cleanly.** Carriage-return progress bars and multi-line ANSI redraws are now interpreted as a terminal would: the log keeps the finished frame instead of raw `\r`/escape soup, and live viewers (Web UI and TUI) watch the active region update in place. See [Logs](https://docs.runwisp.com/concepts/logs/#progress-bars--live-redraws).
- **Rewind a settled redraw's frames.** A finished progress bar or redraw keeps a sampled, best-effort history you can scrub back to — click the line in the Web UI, use `[`/`]` then `enter` in the TUI, or `GET …/log/line/{n}/history`. See [Logs](https://docs.runwisp.com/concepts/logs/#rewinding-the-frames).
- **`runwisp demo --no-tui`.** Leaves the demo daemon running in the background and prints its Web UI password to stdout instead of opening the TUI — usable over SSH or in scripts. See [CLI](https://docs.runwisp.com/operations/cli/#cloud-and-demo).
- **Linkable executions.** Picking a run in the Web UI now puts its ID in the URL path (`/tasks/<name>/<id>` and `/runs/<id>`) — on a task's page and the All Runs page — so an individual execution can be bookmarked or shared; a new task-agnostic `GET /api/runs/{runId}` restores a shared link to any run.
- **Filter runs in the Web UI.** A filter popover on the run list narrows by status (five outcome buckets — Running, Succeeded, Failed, Skipped, Stopped — with an Advanced expander for exact statuses), a From/To date range, task, trigger, an exit-code expression (`137`, `>100`, `>100 <150`), and retries — all applied server-side and mirrored as `GET /api/runs` query parameters. It opens over the run detail (a bottom sheet on phones), leaving the list visible. See [Web UI tour](https://docs.runwisp.com/getting-started/web-ui-tour/#filtering).

### Changed

- **Lower idle memory.** RSS now settles toward the daemon's working set instead of camping at its high-water mark after a spike.
- **TUI run list filters; the sidebar filters as you type.** `f` filters runs by outcome, with a banner under the column header naming the active filter so the narrowed view is obvious; `/` on the sidebar filters tasks by name (it's an explicit mode, so `q` types into the filter instead of quitting). The run list is always newest-first — page it with `Home`/`End`/`PgUp`/`PgDn`. See [the TUI tour](https://docs.runwisp.com/getting-started/tui-tour/).
- **Destructive TUI actions confirm; deletes are undoable.** Trigger, re-run, stop, and restart ask before they act; deleting runs (single or bulk) acts immediately and offers a `u` undo toast instead. Mark every notification read with `a` in the notifications panel.
- **Tidier log directories.** Each run's index, timestamp, rotation, and frame-history sidecars are now consolidated into a single hidden `.log.meta` container, so an `ls` of a log directory shows only the `.log` files. See [Logs](https://docs.runwisp.com/concepts/logs/#the-sidecar-container-logmeta).
- **Service instances are labelled 1-based.** A multi-instance service now shows every run as `name#1`, `name#2`, `name#3` in the Web UI and TUI instead of `name`, `name#1`, `name#2`; a single-instance service stays just `name`. See [Services](https://docs.runwisp.com/configuration/services/).
- **TUI Run Now dialog tells you how to toggle a flag.** A focused flag shows an inline cue, the key legend names the action for whatever field is focused, and `←/→` toggle a flag alongside `space`/`x`. See [Parameters](https://docs.runwisp.com/configuration/tasks/#parameters).
- **Port already taken by another RunWisp daemon.** Launching `runwisp` when a daemon from a _different_ data directory holds the port now names that daemon's datadir and config and offers to connect to it or stop it and launch here, instead of the generic "another process" error. A new local-only `GET /api/instance` (loopback/socket; 403 over the network) backs the discovery.
- **Responsive TUI execution table.** Column widths now flex with the terminal — columns grow proportionally up to a per-column maximum and shrink toward sensible floors, and the main panel is capped to a readable max width so it stays left-aligned instead of stretching edge-to-edge on wide terminals.
- **Web UI visual identity: teal brand, slate neutrals.** The dashboard now reads in a deep-teal brand accent over cool slate neutrals, with an instrument-style execution detail and a black-box console. Light and dark both retuned.
- **Task view rebuilt around the run history.** The history rail and run detail now fill the page edge-to-edge as one card-less surface, divided by a single spine. Run controls live in the detail header: a split Run button triggers with defaults (and queues at max concurrency) with a dropdown to re-run a selected run pre-filled with its inputs; services show Stop/Restart in the same spot. Search moved into a single field in the app header — centered between the breadcrumb and status chips (⌘K focuses it) — that searches run output on a task and filters the run list elsewhere, and the All Runs page adopts the same card-less surface, instrument-style detail, dense status-led rows, and hover-revealed row selection.

### Fixed

- **TUI help overlay (`?`) scrolls.** The keyboard-shortcut reference is taller than most terminals and was cut off at the bottom; it now scrolls with `↑/↓`, `PgUp`/`PgDn`, `g`/`G`, and the mouse wheel. See [the TUI tour](https://docs.runwisp.com/getting-started/tui-tour/).
- **Maximized log console showed nothing until you scrolled.** Expanding the console on a long run rendered a blank viewport until the first scroll event; it now re-syncs its scroll position on resize so lines appear immediately.
- **Run-list sort toggle had no effect.** `GET /api/runs?sort_direction=…` (and the per-task runs list) ignored the direction unless a `sort_field` was also given, so the Web UI's sort button never reordered anything; the default column now honors an explicit direction.
- **TUI run-detail Delete button.** Clicking 🗑 Delete with the mouse now opens the confirm dialog, and keyboard focus reaches the action button from both header rows.
- **Empty TUI run list no longer collapses.** A list with no runs (or a filter that matched nothing) now fills the panel with its footer pinned to the bottom, instead of bunching up under the header.
- **Fatal boot errors are no longer silent in TUI mode.** `runwisp` died with exit 1 and no output when startup failed before the TUI attached (e.g. a config parse error); the error now reaches stderr.
- **`runwisp exec` no longer drops output if its log stream blips.** When the live stream ended without a completion event (a transport hiccup under load), the command could exit having printed none of the run's captured output; it now reconnects from the last line it saw and prints the persisted tail.
- **A cancelled request no longer looks like a server error.** When a client disconnects mid-query (a browser aborting a superseded fetch), the interrupted read now returns `408` instead of a `500`, and genuine `500`s log their underlying cause instead of discarding it.

## [0.10.0] - 2026-06-18

### Added

- **`params` on `[tasks.*]`.** Let a task take inputs — env vars, arguments, options, and flags — that you fill in when you trigger it by hand from the UI, TUI, or REST; scheduled runs fall back to the defaults you declare. Clearing a value omits the parameter entirely (the default is not re-applied), and you can pass an explicit empty string when you mean one. Values are passed safely as arguments and env (never pasted into the shell) and saved with the run so you can see what it ran with. See [Parameters](https://docs.runwisp.com/configuration/tasks/#parameters).
- **Configurable control socket (`--socket` / `RUNWISP_SOCKET`).** Set the daemon's socket path explicitly so a bind-mounted `--data` can't break it and CLI commands can reach a daemon without restating `--data`. See [Daemon](https://docs.runwisp.com/configuration/daemon/#control-socket).
- **Bundled tzdata.** IANA time zones now resolve on slim images (Alpine/distroless) without installing `tzdata`.
- **`runwisp exec --url`.** Trigger a task on a remote daemon over the network: it logs in (CHAP), follows the live log stream, and exits with the task's exit code, caching the session token so repeated calls don't re-authenticate. Add `--detach` to fire-and-forget. See [Triggering a task remotely](https://docs.runwisp.com/recipes/remote-trigger/).
- **`POST /api/tasks/{name}/run?wait=true`.** Block the trigger request until the run finishes and get the completed run (with `exit_code`) back in one call — no trigger-then-poll loop. Bound the hold with `wait_timeout` (seconds). See [Triggering a task remotely](https://docs.runwisp.com/recipes/remote-trigger/).

### Changed

- **Runs are identified by when they ran.** The Web UI's run lists and detail header now lead with the run's start time, outcome, trigger, and a retry badge instead of an opaque ULID suffix (`Run #52TNPQK0`); the full run ID stays visible and copyable in the detail panel.
- **Daemon-owned compose containers.** Services-mode containers are labelled, reclaimed before launch, and removed on exit, so a daemon restart or `kill -9` no longer collides with its own leftover container and drives the service FATAL. See [Compose profiles](https://docs.runwisp.com/configuration/compose/#profiles).
- **Clearer startup/shutdown logs.** A fatal server error logs its real cause instead of a phantom `signal=terminated`, the stale-socket message says a daemon may already be running, and a slow listener bind no longer emits a spurious health-check warning.
- **Section-aware config errors.** A misplaced `on_overlap`/`timezone`/`host`/`port` now hints at the correct section in the unknown-key error.

### Fixed

- **Skipped runs now show up live.** A run dropped by an `on_overlap = "skip"` policy emits a terminal event, so it reaches the Web UI as `skipped` over SSE instead of appearing stuck in `pending` until a page reload.
- **Spring-forward cron ticks no longer vanish.** A cron like `0 2 * * *` whose time falls in the DST gap now fires once at the gap end (the next valid instant). See [DST behaviour](https://docs.runwisp.com/concepts/scheduling/#dst-behaviour).
- **Horizontal scrolling in the log viewer** has the line-number gutter pinned.
- **Control socket on bind mounts.** A socket whose filesystem rejects `chmod` (Docker Desktop bind mounts, some network FS) is tolerated with a warning now.

## [0.9.0] - 2026-06-12

### Added

- **`runwisp import cron` / `runwisp import supervisord`.** Convert an existing crontab or supervisord config into an annotated `runwisp.toml`, with inline `# TODO`s for anything that needs a human. See [Converting crontabs](https://docs.runwisp.com/replacing-cron/converting-crontabs/) and [Migrating from supervisord](https://docs.runwisp.com/recipes/migrating-from-supervisord/).
- **`depends_on` on `[services.*]`.** Gate a service's boot on other services becoming healthy, with reverse-order teardown on shutdown — boot ordering only, never a workflow DAG, and it never deadlocks (a dependency that won't come up starts the dependent anyway with a warning). See [Boot order with `depends_on`](https://docs.runwisp.com/configuration/services/#boot-order-with-depends_on).
- **`include` on `[daemon]`.** Glob extra TOML files (e.g. `conf.d/*.toml`) into the config so tasks can be split across files; collections accumulate, names stay unique across files, and singleton tables stay in the root. See [Splitting config across files](https://docs.runwisp.com/configuration/daemon/#include).
- **`runwisp reload` (and `SIGHUP`) for explicit, validated config reload.** Pick up `runwisp.toml` task add/change/remove edits in a running daemon without a restart; validate-first, so a bad edit or a restart-only change is rejected and the live set is untouched. See [Reload](https://docs.runwisp.com/operations/reload/).
- **`jitter` on `[tasks.*]` / `[defaults]`.** Pace cron tasks that share a fire time through a daemon-wide one-at-a-time gate so they take turns instead of stampeding — each runs as soon as the gate frees and slips up to its window only under contention. See [jitter](https://docs.runwisp.com/configuration/tasks/#jitter).
- **Missed-run alerts.** A scheduled run skipped because the daemon was down is detected on restart, recorded as a browsable `missed` run, and raised as a failure-level alert — silence it per task or globally with `notify_on_missed`. See [Missed ticks](https://docs.runwisp.com/concepts/scheduling/#missed-ticks-catchup).
- **`working_dir` and `shell` on `[tasks.*]` / `[services.*]`.** Run a command in a specific directory and under a chosen interpreter (e.g. `/bin/bash`); `shell` also takes a `[defaults]` value. See [Working directory & shell](https://docs.runwisp.com/configuration/tasks/#working-directory--shell).
- **`exit_codes` on `[tasks.*]` / `[services.*]` / `[defaults]`.** List the exit codes treated as success (default `[0]`) so a non-zero "nothing to do" code doesn't trip retries or alerts. See [Retries & timeout](https://docs.runwisp.com/configuration/tasks/#retries--timeout).
- **`stop_signal` on `[tasks.*]` / `[services.*]` / `[defaults]`.** Choose the signal that opens the stop ladder before SIGKILL (default `SIGTERM`); `SIGKILL` skips the grace window. See [Retries & timeout](https://docs.runwisp.com/configuration/tasks/#retries--timeout).
- **`priority` and `autostart` on `[services.*]`.** `priority` fixes the boot start order (lowest first, deterministic — not a dependency); `autostart = false` defines a service that boots stopped until you start it from the UI/API. See [Startup](https://docs.runwisp.com/configuration/services/#startup).
- **`umask` on `[tasks.*]` / `[services.*]`.** Set the octal file-creation mask for a run (e.g. `"027"`); applied per-process so concurrent runs never affect each other. See [Working directory & shell](https://docs.runwisp.com/configuration/tasks/#working-directory--shell).
- **`run_on_start` on `[tasks.*]`.** Fire a task once at daemon boot, on top of any `cron` — the `@reboot` equivalent, tagged `triggered_by = startup`. See [Scheduling](https://docs.runwisp.com/configuration/tasks/#scheduling).
- **`user` on `[tasks.*]` / `[services.*]`.** Run as another OS user (`user` or `user:group`, name or numeric id) when the daemon runs as root. See [Working directory & shell](https://docs.runwisp.com/configuration/tasks/#working-directory--shell).
- **`start_retries` on `[services.*]` / `[defaults]`, plus FATAL services.** A service that keeps fast-failing (exits below `healthy_after`) now tolerates `start_retries` failed starts in a row before going FATAL — it stops restarting, records a `start_failed` run, and rings the bell instead of flapping forever. See [When a service can't start](https://docs.runwisp.com/configuration/services/#when-a-service-cant-start-fatal).
- **Six-field cron with second precision.** `cron` now accepts an optional leading seconds field, so `*/30 * * * * *` fires every 30 seconds aligned to the wall clock. Existing 5-field specs are unchanged. See [Scheduling](https://docs.runwisp.com/configuration/tasks/#scheduling).

### Changed

- **`backoff_reset_after` renamed to `healthy_after`**, which now governs both the restart-backoff reset and the failed-start/FATAL threshold. See [Healthy threshold](https://docs.runwisp.com/concepts/retries/#healthy-threshold-healthy_after).
- **`keep_for` is now capped at ~100 years** to catch typos at config load. See [Logs & retention](https://docs.runwisp.com/configuration/tasks/#logs--retention).

### Fixed

- **Spurious "managed by systemd" quit prompt.** A daemon spawned by `runwisp` / `runwisp demo` from a systemd-scoped terminal no longer inherits `INVOCATION_ID` and falsely self-reports as service-managed, so the TUI's quit dialog offers "Shut Down" again.

## [0.8.0] - 2026-06-09

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
- **`runwisp demo`.** Boots a throwaway instance with a realistic config and hundreds of pre-seeded historical runs (with real on-disk logs) so you can explore the TUI and Web UI without writing a `runwisp.toml`. Everything lives in a temp directory that's deleted when the daemon stops. See [Quick start](https://docs.runwisp.com/getting-started/quick-start/).
- **Structured daemon logging.** `runwisp daemon` logs every run start, success, and failure with exit code, end reason, and duration. New `--log-level` / `--log-format` flags and `RUNWISP_LOG_LEVEL` / `RUNWISP_LOG_FORMAT` env vars; JSON output for log pipelines. See [Operations / Logging](https://docs.runwisp.com/operations/logging/).

### Changed

- **Task and service names may now contain `:`** (e.g. `[tasks."db:backup"]` — TOML needs the quotes). See [\[tasks.\*\]](https://docs.runwisp.com/configuration/tasks/).
- **Log console matches the app theme.** ANSI colors in run output now map to the RunWisp palette, and the console chrome uses the design-system grays.
- **Breaking: `env_file` values are now visible in the API/UI** — use `secrets_file` for credentials.
- **Breaking: notifier `*_env` / `*_file` keys are gone.** Set `webhook_url`, `bot_token`, `password` directly, with `${...}` substitution for env vars and files.
- **CLI dead ends now point somewhere.** `runwisp exec <typo>` suggests matching task names, unreachable-daemon errors say how to start one, and first-run output links the docs.
- **Quieter daemon output.** HTTP access lines share the slog shape and destination; routine 200/401 traffic (health, SSE keepalives, anonymous polling) moves to `debug`; startup banner absorbs init warnings; redundant post-banner lines are gone; data-directory section collapses to one absolute `Data` line; Ctrl+C-triggered failures no longer surface as `WARN`/`ERROR`.

### Fixed

- **Run detail stats no longer overlap at narrow widths.** The Started / Duration / Exited box now reflows with the panel width instead of letting the date spill into the duration.
- **Refreshing the Web UI on a task whose name contains a dot no longer 404s.** The daemon mistook `/tasks/backup.daily` for a missing static asset instead of serving the dashboard.
- **The dashboard's "Up next" panel no longer goes stale.** Next-run times refresh when a scheduled run fires, and relative timestamps keep ticking while the page sits open.
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
- CLI commands: `run`, `trigger`, `status`, `list`, `add`, `edit`, `validate`, `tui`, `daemon`, `openapi`.
- Bubbletea terminal UI (`tui` command) with home view, exec list, log pane, and dialogs.
- Task scheduler with concurrency, restart, missed-run, and retention policies.
- CHAP authentication for the HTTP API.
- Deterministic human-readable instance fingerprint based on machine-id and working directory.

[Unreleased]: https://github.com/runwisp/runwisp/compare/v0.13.2...main
[0.13.2]: https://github.com/runwisp/runwisp/compare/v0.13.1...v0.13.2
[0.13.1]: https://github.com/runwisp/runwisp/compare/v0.13.0...v0.13.1
[0.13.0]: https://github.com/runwisp/runwisp/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/runwisp/runwisp/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/runwisp/runwisp/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/runwisp/runwisp/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/runwisp/runwisp/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/runwisp/runwisp/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/runwisp/runwisp/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/runwisp/runwisp/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/runwisp/runwisp/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/runwisp/runwisp/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/runwisp/runwisp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/runwisp/runwisp/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/runwisp/runwisp/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/runwisp/runwisp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/runwisp/runwisp/releases/tag/v0.1.0
