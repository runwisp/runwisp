<div align="center">

<img alt="RunWisp logo - open-source cron job manager and process supervisor with a web dashboard" src="packages/ui/static/runwisp_c.svg" width="120">

# RunWisp

**See what ran, when, why it failed, and what it printed.**

The open-source, self-hosted **cron job manager and process supervisor** (with a built-in web dashboard, terminal UI, and REST API). One static Go binary, zero runtime dependencies.

[runwisp.com](https://runwisp.com) · [Documentation](https://docs.runwisp.com) · [Why RunWisp](#why-runwisp) · [Install](#install) · [Quick Start](#quick-start)

[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/runwisp/runwisp?include_prereleases&sort=semver&color=00ADD8)](https://github.com/runwisp/runwisp/releases)
[![Coverage](https://sonarcloud.io/api/project_badges/measure?project=runwisp_runwisp&metric=coverage)](https://sonarcloud.io/component_measures?id=runwisp_runwisp&metric=coverage)
[![Security Rating](https://sonarcloud.io/api/project_badges/measure?project=runwisp_runwisp&metric=security_rating)](https://sonarcloud.io/summary/new_code?id=runwisp_runwisp)

</div>

---

**RunWisp** is a single-binary replacement for `crond` and `supervisord`: cron scheduling, process supervision, and run monitoring in one place. If you've ever SSH'd into a box at 3 AM to work out _why_ a scheduled job silently failed, RunWisp is for you (whether that job is a nightly backup, a queue worker, or a container service).

Define your scheduled jobs (backups, health checks, log rotation, ETL) and your long-running services (queue workers, background daemons) in one `runwisp.toml`. RunWisp records every run (exit code, duration, timestamps, full stdout/stderr) so you can browse it in the web dashboard, terminal UI, or REST API, stream logs live, and get alerted on Slack, email, or Telegram the moment something fails.

<div align="center">
<img alt="RunWisp web dashboard in action: opening a task, triggering a run and watching its log stream fill in real time, then inspecting exactly why a run failed, all in a self-hosted UI" src="apps/docs/src/assets/screenshots/runwisp-demo.webp" width="780">
<p><em>The web dashboard, live: trigger a run, watch its output stream in real time, then jump straight to when and why anything failed (all served by the daemon itself).</em></p>
</div>

**See all of this yourself in about a minute:** run `npx runwisp demo` (or `bunx runwisp demo`) with nothing installed. It boots a throwaway, fully-populated instance (a temp config, hundreds of seeded runs with real logs, live services, the dashboard open in your browser) and deletes everything on exit.

---

## Why RunWisp

`crond` runs your jobs but goes silent when they fail. `supervisord` keeps a process alive but has no scheduler and a threadbare UI. RunWisp does **both** (cron scheduling _and_ always-on service supervision) and makes every run visible: exit code, duration, timestamps, and captured output, browsable in the UI or streamed live, with failure alerts routed per task.

Already running something? You don't have to rewrite it to get observability:

- **Coming from crond**: `sudo runwisp takeover` finds your existing crontabs, imports every job, and takes over from cron in one command. Nothing to rewrite. See [Take over from cron](https://docs.runwisp.com/coming-from/cron/).
- **Running Docker Compose**: point `[compose.myapp]` at your `docker-compose.yml` and every service gains logs, restart policies, notifications, and trigger/stop, without touching the compose file. See [`[compose.*]`](https://docs.runwisp.com/configuration/compose/).
- **Replacing supervisord, systemd, or a crontab**: `runwisp import supervisord` / `import systemd` / `import cron` turns an existing config into an annotated `runwisp.toml`, with inline `# TODO`s for anything that needs a human. See [Converting configs](https://docs.runwisp.com/coming-from/crontabs/).

### How RunWisp compares to crond, systemd timers & supervisord

|                      | crond                | systemd timers     | supervisord    | **RunWisp**                                      |
| -------------------- | -------------------- | ------------------ | -------------- | ------------------------------------------------ |
| Cron scheduling      | Yes                  | Yes                | No             | **Yes**                                          |
| Process supervision  | No                   | Yes                | Yes            | **Yes**                                          |
| Web dashboard        | No                   | No                 | Basic HTML     | **Yes (Svelte SPA)**                             |
| Terminal UI          | No                   | No                 | No             | **Yes (Bubbletea)**                              |
| REST API             | No                   | D-Bus              | XML-RPC        | **REST + JWT**                                   |
| Live log streaming   | No                   | `journalctl -f`    | Tail only      | **SSE**                                          |
| Concurrency policies | No                   | Overlap prevention | No             | **Queue · skip · kill**                          |
| Failure alerts       | No                   | `OnFailure=` unit  | Event listener | **Slack · Discord · Telegram · email · webhook** |
| Log rotation         | External (logrotate) | journald           | Built-in       | **Built-in, per-task**                           |
| Execution history    | No                   | `journalctl`       | No             | **SQLite, browsable in UI**                      |
| Runtime dependencies | libc                 | systemd            | Python         | **None**                                         |
| Config               | crontab syntax       | INI unit files     | INI files      | **One TOML file**                                |

And versus the Docker/lightweight crowd:

- **supercronic / Ofelia**: same container-friendly footprint, but RunWisp adds resident-service supervision, persistent per-run history in SQLite, live streaming, and one-click re-trigger instead of stdout-only or in-memory logs.
- **Dagu / Cronicle**: also single-binary and DB-free, but built around DAG/workflow runs; RunWisp adds always-on service supervision (a supervisord replacement, not just a workflow runner) with no Node or Python.
- **Airflow**: no external metadata DB, no multi-process deployment, no ops team; one ~25 MB binary running in five minutes.

---

## Install

One-line installer that drops the `runwisp` binary on your `PATH`:

```bash
curl -fsSL https://get.runwisp.com | sh
```

Or via your favourite package manager:

```bash
npx runwisp             # try it without installing; runs the prebuilt Go binary
npm install -g runwisp  # or: bun add -g runwisp (bunx runwisp to try it)
```

Prefer manual? Grab a tarball from [GitHub Releases](https://github.com/runwisp/runwisp/releases).

Prefer a container? `runwisp/runwisp` on Docker Hub ships the same binary (Alpine by default, `-debian` variant, amd64 + arm64):

```bash
docker run -d --name runwisp -p 9477:9477 \
  -e RUNWISP_PASSWORD=change-me \
  -v ./runwisp.toml:/etc/runwisp/runwisp.toml:ro \
  -v runwisp-data:/var/lib/runwisp \
  runwisp/runwisp:latest
```

See [Docker](https://docs.runwisp.com/getting-started/docker/) for image variants, required env vars, and volumes.

> **1.0 and stable.** Scheduling, supervision, live logs, and persistent run history all ship in the one binary. RunWisp follows semver: breaking changes only land on a major bump, so upgrades within a major line are safe. Skim [CHANGELOG.md](CHANGELOG.md) before you do. Found a rough edge? Tell us.

---

## Quick Start

**1. Run it:**

```bash
runwisp
```

With no `runwisp.toml` next to you yet, RunWisp doesn't guess: it asks to create a starter config, writes one, starts the daemon in the background, and drops you into the **terminal UI**: task list, live logs, run history, one-click triggering. The Home page shows the web dashboard URL and an auto-generated password (highlight the row and press `Enter` to copy).

Want your own password? Set `RUNWISP_PASSWORD`. Running purely locally and the login wall's in the way? Set [`RUNWISP_AUTH=off`](https://docs.runwisp.com/operations/auth/#running-without-a-password). On a headless box (cron, systemd, Docker, CI), run `runwisp daemon` instead (it skips the prompt and fails loudly on a missing config). To survive reboots, `runwisp service install` wires the daemon into systemd or launchd.

**2. Make it yours:**

Swap the example `hello` task out for what you actually came here to run:

```toml
[tasks.backup-db]
cron       = "0 2 * * *"   # every night at 2 AM
jitter     = "30m"         # if the 2 AM crowd piles up, take turns: slip up to 30 min, never stampede
on_overlap = "skip"        # don't stack if the previous run is still going
keep_runs  = 30
run = "pg_dump mydb | gzip > /backups/mydb-$(date +%F).sql.gz"

[tasks.health-check]
cron = "*/5 * * * *"
run  = "curl -sf https://myapp.example.com/health || exit 1"

[services.worker]
instances    = 3              # keep three copies running
env          = { NODE_ENV = "production" }   # visible in the UI
secrets_file = "/etc/runwisp/worker.env"     # never shown in the API/UI
run          = "node /app/worker.js"
```

`[tasks.*]` are scheduled or manually triggered jobs. `[services.*]` are always-on processes that RunWisp keeps alive with exponential restart backoff; each instance is its own visible run with its own exit code, duration, and captured logs. Pick up your edits with `runwisp reload` (or `SIGHUP`); validate-first, so a bad edit leaves the running task set untouched, no restart needed.

Full configuration reference, REST API docs, and operational guides live at **[docs.runwisp.com](https://docs.runwisp.com)**.

---

## Features

**Scheduling & execution**

- Standard cron expressions with per-task concurrency policies (`queue` · `skip` · `kill`)
- Long-running services with one or more `instances`, exponential restart backoff, crash recovery, graceful shutdown
- Retries with configurable backoff (`constant` · `linear` · `exponential`)
- Per-task timeouts with automatic kill on deadline
- Catchup policies for missed runs (`latest` · `all` · `skip`)
- Per-execution parameters: declare env vars, args, options, and flags a task accepts, then supply values at trigger time from the UI, TUI, or API, passed as inert argv, never spliced into the shell

**Observability & alerting**

- Real-time stdout/stderr streaming over SSE, viewable in the web UI and TUI
- Every run recorded in SQLite with exit code, duration, and timestamps
- Built-in per-task log rotation with overflow policies (`drop_new` · `drop_old` · `kill`)
- Failure alerting to Slack, Discord, Telegram, email (SMTP), generic webhooks, or the in-app inbox, routed per task with `notify` (or a `[[route]]` for non-failure outcomes), with `failures` declaring which outcomes count

**Interfaces**

- **Web dashboard**: Svelte 5 SPA with dark mode, embedded in the binary
- **Terminal UI**: full Bubbletea TUI for headless servers and SSH sessions
- **REST API**: authenticated endpoints for triggering, listing, and managing tasks

**Operations**

- Single Go binary, zero runtime dependencies. No Python, no Node.js, no external database.
- Optional self-signed HTTPS: set `tls = "auto"` and RunWisp generates a cert and serves TLS as soon as you bind beyond `127.0.0.1`. Or bring your own cert (`tls_cert`/`tls_key`), or put it behind a reverse proxy ([`[daemon]`](https://docs.runwisp.com/configuration/daemon/#tls-tls_cert-tls_key))
- ~25 MB RAM idle, happy on a $5 VPS, a Raspberry Pi, or alongside your real workload
- Crash-safe: `kill -9` and power loss are recoverable; in-flight runs are marked **interrupted** on restart, never silently lost
- Local-first, offline-complete. No signup, no telemetry, no account required.
- TOML configuration: one file, version-controllable, reviewable in pull requests
- Live config reload via `runwisp reload` or `SIGHUP`: pick up `runwisp.toml` edits without a restart; validate-first, so a bad edit leaves the running task set untouched

<div align="center">
<img alt="RunWisp terminal UI in action: browsing recent runs on the homepage, opening one to scroll its log, then triggering a scheduled backup from the sidebar and watching its progress bars stream to completion, all over SSH" src="apps/docs/src/assets/screenshots/tui-demo.webp" width="780">
<p><em>The terminal UI, live: browse recent runs, scroll a log, and trigger a task, without leaving the session.</em></p>
</div>

---

## Documentation

Full user and operator documentation lives at **[docs.runwisp.com](https://docs.runwisp.com)**: installation, the complete `runwisp.toml` schema, scheduling and concurrency policies, retries, log rotation, the REST API, and operational guides.

- [Changelog](CHANGELOG.md) - recent changes and version history
- [Contributing](CONTRIBUTING.md) - development setup and contribution guidelines
- [Security Policy](SECURITY.md) - responsible disclosure
- [Issue tracker](https://github.com/runwisp/runwisp/issues) - bug reports and feature requests

---

## License

The RunWisp daemon and web UI are GPL-3.0-or-later: use it however you want, and if you distribute a modified version, keep it open. No CLA, no dual-licensing, no strings beyond that. See [LICENSE](LICENSE).

The shared libraries under `packages/` are Apache-2.0 instead. See [LICENSE-APACHE](LICENSE-APACHE) and each package's own `LICENSE`.

---

<div align="center">

**RunWisp** - cron and supervisord, replaced.

[runwisp.com](https://runwisp.com) · [Documentation](https://docs.runwisp.com) · [Releases](https://github.com/runwisp/runwisp/releases) · [Report a bug](https://github.com/runwisp/runwisp/issues)

</div>
