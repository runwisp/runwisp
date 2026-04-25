<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="packages/ui/static/runwisp_c.svg" width="120">
  <source media="(prefers-color-scheme: light)" srcset="packages/ui/static/runwisp_c.svg" width="120">
  <img alt="RunWisp logo" src="packages/ui/static/runwisp_c.svg" width="120">
</picture>

# RunWisp

### Stop babysitting cron jobs. Start shipping.

**The open-source cron replacement and process supervisor with a built-in web dashboard.**

One binary. One TOML file. Zero dependencies. Full visibility.

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](apps/runwisp/)
[![GitHub Stars](https://img.shields.io/github/stars/runwisp/runwisp?style=social)](https://github.com/runwisp/runwisp)

[Get Started](#quick-start) · [Features](#why-runwisp) · [Compare](#comparison-with-crond-systemd-timers-and-supervisord) · [Docs](#documentation)

</div>

---

> **TL;DR** — RunWisp replaces `crond`, `crontab`, and `supervisord` with a single small Go binary. Define tasks in TOML, get a web dashboard, terminal UI, REST API, real-time log streaming, and execution history out of the box. No Python, no Node.js, no external database.

---

## Why RunWisp?

If you've ever SSH'd into a server to figure out _why_ a cron job silently failed at 3 AM, RunWisp is for you.

- **`systemd` timers are tedious and OS-locked.** Writing `.timer` and `.service` files for every job is painful, and `journalctl` doesn't work in Docker or macOS. RunWisp provides scheduling, concurrency control (queue/skip/terminate), and logging in a single cross-platform `runwisp.toml`.
- **Containerized cron lacks observability.** Lightweight runners (like `supercronic`) fix the container execution problem but dump everything to `stdout`. RunWisp gives you segregated, per-task log retention, persistent execution history, and one-click re-triggering.
- **End the DevOps translation game.** Developers define their task schedules, retries, and limits in a single `runwisp.toml` file that lives in the repo. Because an identical binary handles scheduling across a local MacBook, integration branches, and production, dev environments match prod exactly. DevOps teams never have to manually provision, sync, or troubleshoot OS-level timers across varying infrastructures again.

---

## Web Dashboard

A clean, modern Svelte dashboard ships inside the binary. Task status, execution history, live logs, one-click triggering, and dark mode — all without installing anything.

<div align="center">
<picture>
  <img alt="RunWisp web dashboard showing task list, execution history, and live log streaming" src="packages/assets/webui-screenshot.png" width="720">
</picture>
<p><em>Web dashboard — task overview, execution history, and live log streaming</em></p>
</div>

## Terminal UI

For headless servers or when you just prefer the terminal. A full interactive TUI built with Bubbletea — browse tasks, view live logs, trigger runs, all from SSH.

<div align="center">
<picture>
  <img alt="RunWisp terminal UI showing task sidebar, live log output, and execution controls" src="packages/assets/tui-screenshot.png" width="720">
</picture>
<p><em>Terminal UI — full task management from your terminal</em></p>
</div>

---

## Quick Start

**1. Install** — download the latest binary from [Releases](https://github.com/runwisp/runwisp/releases):

```bash
# Linux (amd64)
curl -fsSL https://github.com/runwisp/runwisp/releases/latest/download/runwisp-linux-amd64 -o runwisp
chmod +x runwisp
```

**2. Define your tasks** in `runwisp.toml`:

```toml
[tasks.backup-db]
cron       = "0 2 * * *"  # every night at 2 AM
on_overlap = "skip"       # don't stack if the previous run is still going
keep_runs  = 30
run = "pg_dump mydb | gzip > /backups/mydb-$(date +%F).sql.gz"

[tasks.health-check]
cron = "*/5 * * * *"       # every 5 minutes
run  = "curl -sf https://myapp.example.com/health || exit 1"

[tasks.worker]
restart    = "always"      # auto-restart on crash
on_overlap = "skip"
run        = "node /app/worker.js"  # long-running process
```

**3. Start RunWisp:**

```bash
./runwisp
```

**4. Open the web dashboard** at `http://localhost:8080`.

The auto-generated login password is printed on first start (or set `RUNWISP_PASSWORD` to use a fixed one).

That's it. Your tasks are running, monitored, and accessible through the web UI, TUI, and REST API.

---

## Features

### Scheduling & Execution

- **Cron scheduling** — standard cron expressions, per-task concurrency policies (`queue`, `skip`, `terminate`)
- **Process supervision** — long-running daemons with `restart = "always"`, crash recovery, and graceful shutdown
- **Retries with backoff** — configurable `retry_attempts`, `retry_delay`, and `retry_backoff` (linear or exponential)
- **Timeouts** — per-task `timeout` enforcement, automatic kill on deadline
- **Catchup policies** — configurable missed-run behaviour (`latest`, `all`, `skip`) via `catch_up`

### Observability

- **Real-time log streaming** — live stdout/stderr over SSE, viewable in the web UI and TUI
- **Execution history** — every run is recorded in SQLite with exit code, duration, start/end timestamps
- **Log rotation** — per-task `log_max_size` with overflow policies (`drop_new`, `drop_old`, `kill_task`)

### Interfaces

- **Web dashboard** — Svelte SPA with task status, run history, log viewer, one-click triggering, dark mode
- **Terminal UI** — interactive Bubbletea TUI for headless servers and SSH sessions
- **REST API** — authenticated endpoints for triggering, listing, and managing tasks programmatically

### Operations

- **Single binary, zero dependencies** — compiled Go with embedded SQLite and embedded web UI
- **~15 MB RAM at idle** — designed for VPS, Raspberry Pi, and edge deployments
- **TOML configuration** — one file, version-controllable, reviewable in pull requests
- **Disk safeguards** — `storage.max_size` and `storage.min_free_space` limits to prevent runaway log growth

---

## Comparison with crond, systemd timers, and supervisord

|                         | crond                | systemd timers     | supervisord | **RunWisp**                 |
| ----------------------- | -------------------- | ------------------ | ----------- | --------------------------- |
| **Cron scheduling**     | Yes                  | Yes                | No          | **Yes**                     |
| **Process supervision** | No                   | Yes (via units)    | Yes         | **Yes**                     |
| **Web dashboard**       | No                   | No                 | Basic HTML  | **Yes** (Svelte SPA)        |
| **Terminal UI**         | No                   | No                 | No          | **Yes** (Bubbletea)         |
| **REST API**            | No                   | D-Bus              | XML-RPC     | **REST + JWT**              |
| **Live log streaming**  | No                   | `journalctl -f`    | Tail only   | **SSE**                     |
| **Concurrency control** | No                   | Overlap prevention | No          | **Queue, skip, terminate**  |
| **Log rotation**        | External (logrotate) | journald           | Built-in    | **Built-in, per-task**      |
| **Execution history**   | No                   | `journalctl`       | No          | **SQLite, browsable in UI** |
| **Single binary**       | Yes                  | Part of systemd    | No (Python) | **Yes**                     |
| **Config format**       | `crontab` syntax     | INI unit files     | INI files   | **Single TOML file**        |

---

## Alternatives

RunWisp targets the gap between "just use cron" and heavyweight workflow orchestrators. Here's how it compares to other tools in the space:

| Project                                                   | Language | Web UI  | TUI     | Cron    | Supervision | Notes                                                        |
| --------------------------------------------------------- | -------- | ------- | ------- | ------- | ----------- | ------------------------------------------------------------ |
| **crond**                                                 | C        | No      | No      | Yes     | No          | The classic. No visibility into task history or output.      |
| **systemd timers**                                        | C        | No      | No      | Yes     | Yes         | Powerful, but OS-locked and requires two root files per task.|
| **supervisord**                                           | Python   | Basic   | No      | No      | Yes         | Mature process supervisor. No cron scheduling.               |
| **[Ofelia](https://github.com/mcuadros/ofelia)**          | Go       | No      | No      | Yes     | No          | Docker-focused cron. No web UI.                              |
| **[Dagu](https://github.com/dagu-org/dagu)**              | Go       | Yes     | No      | Yes     | No          | DAG-based workflow engine. More complex, aimed at pipelines. |
| **[Cronicle](https://github.com/jhuckaby/Cronicle)**      | Node.js  | Yes     | No      | Yes     | No          | Feature-rich, multi-server. Heavier footprint (Node.js).     |
| **[Supercronic](https://github.com/aptible/supercronic)** | Go       | No      | No      | Yes     | No          | Crontab for Docker, but only logs to stdout (no history).    |
| **RunWisp**                                               | Go       | **Yes** | **Yes** | **Yes** | **Yes**     | Single binary, embedded SQLite, web + TUI, ~15 MB RAM.       |

If you need DAG pipelines or enterprise-level orchestration, tools like Dagu or Cronicle may be a better fit. If you want a single binary that replaces both crond and supervisord with modern observability, RunWisp is what you're looking for.

---

## Configuration Reference

RunWisp is configured through a single `runwisp.toml` file. Here's a complete example:

```toml
# Disk-usage safeguards
[storage]
max_size       = "5gb"
min_free_space = "500mb"

# Global defaults (applied to every task unless overridden)
[defaults]
timeout      = "1h"
log_max_size = "100mb"
log_on_full  = "drop_old"
keep_runs    = 50
keep_for     = "30d"

[tasks.backup-db]
group       = "Backups"
description = "Nightly database backup"
cron        = "0 2 * * *"
timeout     = "30m"
on_overlap  = "skip"
keep_runs   = 30
run = "pg_dump mydb | gzip > /backups/mydb-$(date +%F).sql.gz"

[tasks.process-event-queue]
description    = "Worker that retries with exponential backoff"
cron           = "*/10 * * * *"
on_overlap     = "queue"
retry_attempts = 3
retry_delay    = "2s"
retry_backoff  = "exponential"
run = "/usr/local/bin/process-queue"

[tasks.metrics-daemon]
description = "Always-on metrics collector"
restart     = "always"
on_overlap  = "skip"
run         = "/usr/local/bin/metrics-agent"
```

---

## Documentation

> Full-on fledged documentation is coming soon. In the meantime, check out these resources:

|                                                     |                                    |
| --------------------------------------------------- | ---------------------------------- |
| [Example Config](apps/runwisp/runwisp.example.toml) | Annotated example with all options |
| [Contributing](CONTRIBUTING.md)                     | How to contribute                  |
| [Security Policy](SECURITY.md)                      | Reporting vulnerabilities          |

---

## Build from Source

```bash
git clone https://github.com/runwisp/runwisp
cd runwisp
bun install
bun run build
bun run test
bun run check
```

Requires Go 1.22+ and Bun.

---

## License

RunWisp is licensed under the [Apache License 2.0](LICENSE). Use it however you want — in personal projects, startups, enterprises. No CLA, no dual-licensing, no strings attached.

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## Support

- [GitHub Issues](https://github.com/runwisp/runwisp/issues) — Bug reports and feature requests
- [Security Policy](SECURITY.md) — Responsible disclosure

---

<div align="center">

**RunWisp** — cron and supervisord, replaced.

[Get Started](#quick-start) · [GitHub](https://github.com/runwisp/runwisp) · [Releases](https://github.com/runwisp/runwisp/releases)

</div>
