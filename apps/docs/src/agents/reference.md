<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# RunWisp agent reference

Dense reference for agents authoring/operating RunWisp. Human-readable prose lives at https://docs.runwisp.com (each page also at `<url>.md`). This file is the schema/CLI/REST surface only.

Notation in schema blocks: `key: type =default — note`. `=default` omitted means no default (unset). `dur` = Go duration string (`300ms`,`5s`,`10m`,`1h`); retention also accepts `d`/`w`. `size` = byte size (`100mb`,`2gb`). `req` = required.

## Model

- `runwisp.toml` is the ONLY source of task definitions. REST/UI/TUI can read + trigger/stop/restart runs, never create or edit definitions.
- Config reload is restart-only: a running daemon never re-reads TOML. No SIGHUP, no watcher, no reload command. Change TOML → restart daemon.
- Two unit kinds: `[tasks.<name>]` run-to-exit (cron or manual); `[services.<name>]` long-running, `restart=always` forced. Names must be unique across both tables. `name` validated by RunWisp's task-name rules.
- `run =` is shell, executed from disk only — never from an HTTP/WS body.
- Inheritance: `[defaults]` → each task/service → per-key override. `env` merges (task wins); `[compose.<alias>.<svc>]` overrides per imported service.
- IDs are ULIDs. Logs are per-task files on disk; SQLite holds run metadata only.

## runwisp.toml

### [scheduler]

```
timezone: IANA string =host system zone — TZ for cron eval when a task pins none
```

### [storage] (daemon-wide log disk safeguards)

```
max_size:       size =0(no cap)   — hard cap on total log bytes across all tasks
min_free_space: size =0(no check) — stop accepting log lines when partition free < this
```

### [daemon]

```
allow_cloud_dispatch: bool =false — accept peer-dispatched ad-hoc runs (opt-in; one-shot, never edits TOML)
shutdown_timeout:     dur  =10s   — SIGTERM→SIGKILL drain budget for in-flight runs on shutdown
external_url:         string      — public Web UI base for notification deep-links; absolute http(s) w/ host
metrics_enabled:      bool =false — master switch for /metrics
metrics_listen:       host:port   — dedicated metrics listener; REQUIRES metrics_enabled=true
```

### [defaults] (inherited by every task & service)

```
timeout:             dur          — per-attempt wall-clock cap (tasks); unset = no timeout
log_max_size:        size =100mb  — per-run log cap (effective task default)
log_on_full:         enum =drop_old — drop_new | drop_old | kill_task
keep_runs:           int          — row-count retention; 1..1000000
keep_for:            dur          — age retention; positive
backoff_reset_after: dur  =60s    — service uptime that resets the restart counter
env:                 map<str,str> — inline env merged into every task; key ^[A-Za-z_][A-Za-z0-9_]*$, <=256 entries, value <=32KiB, no NUL
env_file:            path         — dotenv file merged into every task; relative to runwisp.toml dir
```

### [tasks.&lt;name&gt;] (run-to-exit)

Required: the table + `run` (unless `compose_file`). `restart="always"` and `instances` are rejected on tasks (use `[services.*]`).

```
group:             string =Tasks   — UI grouping label
description:        string          — human description
cron:              string          — 5-field cron; also @hourly, @every 1h30m; omit => manual-only
timezone:          IANA string      — per-task TZ override (else [scheduler] timezone)
api_trigger:       bool =true       — allow CLI/API/UI trigger; false = cron-only
catch_up:          enum =latest     — missed-firing policy: latest | all | skip
max_catch_up_runs: int  =100        — cap when catch_up=all; >=1
timeout:           dur              — per-attempt cap (inherits [defaults])
graceful_stop:     dur  =5s         — SIGTERM→SIGKILL grace on stop
restart:           enum             — never | on_failure   (always => rejected on tasks)
max_concurrent:    int  =1          — concurrent run cap; 1..1024
queue_max:         int  =100        — queued-run depth; 0..10000
on_overlap:        enum =queue      — queue | skip | terminate
retry_attempts:    int  =0          — retries after a failed attempt; 0..100
retry_delay:       dur  =0          — delay between retries
retry_backoff:     enum             — constant | linear | exponential
log_max_size:      size =100mb      — per-run log cap
log_on_full:       enum =drop_old   — drop_new | drop_old | kill_task
keep_runs:         int              — row retention (inherits [defaults]); 1..1000000
keep_for:          dur              — age retention (inherits [defaults])
run:               string (req)     — shell command; mutually exclusive with compose_file
compose_file:      path             — run a compose service instead of run=
compose_service:   string =taskname — which compose service; requires compose_file
env:               map<str,str>     — inline env (merged over defaults.env)
env_file:          path             — dotenv file
notify_on_failure: []string         — sugar → route on run.failed/timeout/crashed; notifier ids, "id:override", or "inapp"
notify_on_success: []string         — sugar → route on run.succeeded
```

### [services.&lt;name&gt;] (long-running)

`restart=always` is forced. Not allowed: `cron`, `catch_up`, `max_concurrent`, `queue_max`, `retry_*`. Shares all other task keys (`group` default `Services`, `on_overlap` default `skip`). Service-only:

```
instances:           int  =1           — parallel instances; 1..64
restart_delay:       dur  =1s          — delay before a restart
restart_backoff:     enum =exponential — constant | linear | exponential
backoff_reset_after: dur  =60s         — uptime that resets the restart counter
```

### [compose.&lt;alias&gt;] (import docker-compose services)

Expands to one observable service-task per imported compose service (or one task for the whole stack). Imported tasks default `restart=on_failure`, `instances=1`. Reserved scalar keys (any other key = a per-service override sub-table `[compose.<alias>.<svc>]`):

```
file:         path =auto-discover  — compose.yaml/.yml/docker-compose.yaml/.yml
include:      []string             — services to import; mutually exclusive with exclude
exclude:      []string             — services to skip
mode:         enum =services       — services (per-service tasks) | stack (one task)
group:        string =alias        — UI group
project_name: string =alias        — compose project name
profiles:     []string             — compose profiles
env_file:     []string             — compose env files
working_dir:  string =dir of file  — CLI working dir
with_deps:    bool =false          — start dependencies
pull:         enum =missing        — missing | always | never
name_format:  string ={alias}.{service} — generated task name; must contain {service} in services mode
```

Per-service override `[compose.<alias>.<svc>]` accepts: `group`, `description`, `api_trigger`, `timeout`, `graceful_stop`, `on_overlap`, `restart`, `instances`, `restart_delay`, `restart_backoff`, `backoff_reset_after`, `log_max_size`, `log_on_full`, `keep_runs`, `keep_for`, `env`, `env_file`. Not allowed: `run`/`compose_file`/`compose_service`. `mode="stack"` forbids overrides and include/exclude. Caveat: per-service `notify_on_*` is currently parsed but NOT wired to notifications for compose imports.

### [notify] (global notification settings)

```
global_notifiers:  []string =["inapp"] — channels added to every notify list + catch-all on failures; [] opts out
default_timeout:   dur                  — total retry budget per delivery
history_keep:      int  =1024           — in-app bell row cap
history_keep_for:  dur  =90d            — max bell row age
coalesce_window:   dur  =1h             — collapse repeat (kind+task) into one bell row
occurrence_ring:   int  =10             — recent timestamps kept per coalesced row
coalesce_outbound: bool =true           — coalesce outbound bursts too
```

### [[notifier]] (outbound channel; repeatable)

Common: `id` (req, non-empty, not "inapp", no ":"), `type` (req: `slack`|`telegram`|`smtp`), `template_path` (optional).

```
slack:    one of webhook_url | webhook_url_env | webhook_url_file (req); channel (starts # or @)
telegram: one of bot_token | bot_token_env | bot_token_file (req); chat_id (req); parse_mode (MarkdownV2 needs template_path)
smtp:     host(req); port 0..65535; tls starttls|implicit|none (default: 465→implicit else starttls);
          tls_skip_verify bool; from(req email); reply_to(email); to(req,>=1) + cc/bcc(emails);
          username + one of password|password_env|password_file (set together or all omitted); tls=none forbids credentials
```

Secrets: prefer `*_env` / `*_file` forms; never inline secrets you don't control. Secrets are never logged or sent over the cloud integration.

### [[notification_route]] (route events to channels; repeatable)

```
match.kind:     []string — run.started | run.succeeded | run.failed | run.timeout | run.stopped | run.crashed | notify.delivery_failed
match.severity: string   — info | warn | error (optional)
match.task:     string   — glob over task name (optional)
notify:         []string (req, non-empty) — notifier ids (or "inapp"); "id:#override" inline target (slack #/@, telegram chat_id, smtp email)
```

## CLI

Persistent flags: `-c/--config` (=`runwisp.toml`), `--data` (=`.runwisp`), `-p/--port` (=`9477`), `--host` (=`127.0.0.1`).
Env: `RUNWISP_PASSWORD` (else ephemeral per-boot), `RUNWISP_TRUST_PROXY` (CIDRs), `RUNWISP_CLOUD_TOKEN`, `RUNWISP_CLOUD_URL`.

```
runwisp                      — no subcommand: attach TUI to running daemon, else scaffold toml + spawn daemon + attach
runwisp daemon               — start headless daemon (no TUI)
runwisp validate             — validate runwisp.toml without starting anything
runwisp status               — is the daemon alive?
runwisp list                 — list configured tasks and schedules
runwisp exec <task>          — run a task and stream output;  --daemon (via running daemon) | --standalone (in-process), mutually exclusive
runwisp tui                  — attach a TUI to a running daemon
runwisp cloud                — start in cloud mode; --token --url --env-file(=.env) --no-tui
runwisp password             — print the daemon's ephemeral password (local socket)
runwisp openapi              — print the OpenAPI 3.1 spec (JSON) to stdout
runwisp service install      — install autostart (systemd/launchd); -y --print --dry-run --force --system --binary <path>
runwisp service uninstall    — remove autostart; -y --purge (also data dir) --force
runwisp service status       — show autostart status
```

## REST API

Base `/api`. Auth (enforced at middleware, not declared in the OpenAPI doc): Web/TCP clients log in via CHAP → JWT session; the local Unix socket bypasses auth for the CLI/TUI. `/api/local/credentials` is Unix-socket-only. SSE endpoints stream `text/event-stream`. None of these mutate TOML task definitions.

Read (GET):

```
/api/info                                       daemon info
/api/system                                     system stats
/api/system/history                             historical system metrics
/api/daemon/log-stream                          daemon log (SSE)
/api/tasks                                       list tasks
/api/tasks/{task}/runs                           list runs for task
/api/tasks/{task}/runs/{runId}                   one run
/api/tasks/{task}/runs/{runId}/log               log-lines page
/api/tasks/{task}/runs/{runId}/log/raw           full log download (text/plain)
/api/tasks/{task}/runs/{runId}/log/stream        run log (SSE)
/api/tasks/{task}/log/search                     search log lines across runs
/api/runs                                        list all runs
/api/runs/summary                               aggregate run stats
/api/runs/stream                                run lifecycle events (SSE)
/api/notifications                              in-app notifications
/api/notifications/unread-count                 unread count
/api/notifications/stream                       notification events (SSE)
/api/local/credentials                          ephemeral password (Unix socket only)
```

Trigger / stop / mutate runs (POST/DELETE — never touches definitions):

```
POST   /api/tasks/{task}/run                     trigger a new run
POST   /api/tasks/{task}/stop                    stop service (for daemon lifetime)
POST   /api/tasks/{task}/restart                 restart all service instances
POST   /api/tasks/{task}/runs/{runId}/stop       stop a running task
DELETE /api/tasks/{task}/runs/{runId}            delete a run
POST   /api/runs/bulk/cancel                     cancel runs by selector
POST   /api/runs/bulk/delete                     soft-delete runs by selector
POST   /api/runs/bulk/restore                    restore soft-deleted runs
POST   /api/runs/bulk/rerun                      re-run tasks behind selector
POST   /api/notifications/read                   mark all read
POST   /api/notifications/{id}/read              mark one read
POST   /api/notifications/{id}/unread            mark one unread
```

Full machine-readable schema: https://docs.runwisp.com/openapi.json
