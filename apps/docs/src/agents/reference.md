<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

# RunWisp agent reference

Dense reference for agents authoring/operating RunWisp. Human-readable prose lives at https://docs.runwisp.com (each page also at `<url>.md`). This file is the schema/CLI/REST surface only.

Notation in schema blocks: `key: type =default — note`. `=default` omitted means no default (unset). `dur` = Go duration string (`300ms`,`5s`,`10m`,`1h`); retention also accepts `d`/`w`. `size` = byte size (`100mb`,`2gb`). `req` = required.

## Model

- `runwisp.toml` is the ONLY source of task definitions. REST/UI/TUI can read + trigger/stop/restart runs, never create or edit definitions.
- Config reload is explicit: `runwisp reload` / `SIGHUP` / `POST /api/reload` re-read the whole TOML and reconcile the live task set (add/change/remove tasks, services, `[defaults]`). Validate-first/atomic — a parse/validation failure, or a change to a restart-only setting (`[daemon]`, `[scheduler] timezone`, `[storage]`, `[notify]`, bind host/port), is rejected and leaves the running set untouched. Reload is NOT a restart: added tasks get no `run_on_start`/catch-up, in-flight runs finish under their old definition. The daemon never auto-watches the file. Restart-only settings (and re-firing `run_on_start`/catch-up) need `runwisp restart`.
- Two unit kinds: `[tasks.<name>]` run-to-exit (cron or manual); `[services.<name>]` long-running, `restart=always` forced. Names must be unique across both tables. `name` validated by RunWisp's task-name rules.
- `run =` is shell, executed from disk only — never from an HTTP/WS body.
- Inheritance: `[defaults]` → each task/service → per-key override. `env` merges (task wins); `[compose.<alias>.<svc>]` overrides per imported service.
- IDs are ULIDs. Logs are per-task files on disk; SQLite holds run metadata only.

## runwisp.toml

Machine-readable JSON Schema (draft 2020-12): `runwisp schema` (offline) or https://docs.runwisp.com/config.schema.json. Scaffolded/imported configs carry a `#:schema` directive so editors validate them. After editing, `runwisp validate --json` reports errors with structured `key`/`line`/`column`.

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
allow_cloud_dispatch: bool =false — accept peer-dispatched ad-hoc shell/container/compose runs (opt-in; one-shot, never edits TOML; HTTP & existing-task triggers always allowed)
shutdown_timeout:     dur  =10s   — SIGTERM→SIGKILL drain budget for in-flight runs on shutdown
external_url:         string      — public Web UI base for notification deep-links; absolute http(s) w/ host
metrics_enabled:      bool =false — master switch for /metrics
metrics_listen:       host:port   — dedicated metrics listener; REQUIRES metrics_enabled=true
include:              []string    — glob(s) of extra TOML files merged at load; root config only, no nesting
include_cron:         []string    — glob(s) of REAL crontabs read as live task defs at every load/reload; root
                                    config only. Format is PATH-DERIVED, never flagged: /etc/crontab + **/cron.d/*
                                    = system format (6th field = user, job runs as them);
                                    /var/spool/cron/crontabs/<u> and /var/spool/cron/<u> = per-user spool, jobs run
                                    as <u> taken from the FILENAME (requires a root daemon unless <u> IS the
                                    daemon's account; the file must also be owned by <u>, which is what makes the
                                    filename safe to trust); any other path = `crontab -l` dump, runs as the daemon.
                                    Glob hits are filtered to what crond itself reads, and the rule depends on
                                    the dir: in /etc/cron.d-style dirs, regular files named [A-Za-z0-9_-]+ only, so
                                    *.dpkg-old / *.disabled / README / subdirs are skipped and reported; in a spool
                                    dir, any plausible account name (letters, digits, - _ . $) EXCEPT tmp.* (the
                                    temp file `crontab -e` writes). A LITERAL path (no glob metachars) is never
                                    filtered.
                                    Hard errors: unreadable file, a file also matched by [daemon].include,
                                    world-writable file/dir, group-writable file (or non-sticky dir — a sticky
                                    group-writable dir is accepted, that's what a real cron spool is), owner
                                    neither root nor the daemon's euid nor the run-as account, unresolvable run-as
                                    account, spool file the daemon can't become. A job RunWisp can't reproduce is
                                    SKIPPED (rest of the file still runs) and reported via config.Warnings by
                                    file:line; derived name collisions rename to <base>-<crontab basename> and are
                                    reported too. File-level notes are reported the same way: MAILTO= (suppressed
                                    once a sendmail/smtp notifier exists) and a non-absolute SHELL=.
                                    Emitted per job for crond parity: env_base="clean", working_dir="~",
                                    catch_up="skip" (crond never re-fires a missed tick; the missed row is still
                                    recorded), on_overlap="queue" (deliberately NOT crond's unbounded overlap).
                                    No ${...} expansion on cron text. /etc/anacrontab is not read.
                                    Tasks report "source": "cron" + "source_file" in list/status --json;
                                    `runwisp promote` graduates them.
                                    HELD GATE: while a system cron daemon is live (systemctl is-active OR
                                    is-enabled on cron.service/crond.service/cronie.service, else a live pidfile
                                    naming a process that is actually alive), every task
                                    whose source_file is a path cron itself reads (/etc/crontab, **/cron.d/*,
                                    /var/spool/cron[/crontabs]/*) is HELD: loaded, listed, schedule shown, but
                                    NOT registered with the scheduler — no cron firing, no jitter plan, no
                                    catch-up, no run_on_start/@reboot, no missed-run rows, no FirstSeenAt anchor.
                                    Reported as "held_by": "cron" on each task in list/status --json and
                                    /api/info. Manual triggers (exec, API, TUI r) STILL RUN a held task.
                                    The gate is self-healing: the daemon re-probes cron liveness every 60s (only
                                    when include_cron is set) and releases or re-takes holds on its own, so
                                    retiring cron needs NO reload. `runwisp reload`/SIGHUP re-probes too. Either
                                    way catch-up is re-anchored to the moment of release, so a hold window leaves
                                    no history and no missed-tick alerts.
                                    A crontab-format file OUTSIDE cron's own paths is NOT held (nothing to hand
                                    over) and keeps a fire-twice warning instead. `sudo runwisp takeover` is the
                                    one-command way out.
```

### [defaults] (inherited by every task & service)

```
timeout:             dur          — per-attempt wall-clock cap; unset = no timeout (TASKS only)
jitter:              dur          — start-spread window inherited by cron tasks; off when unset (TASKS only)
shell:               path =/bin/sh — interpreter for run scripts (absolute path); see FAIL-FAST
stop_signal:         enum =SIGTERM — stop-ladder signal: SIGTERM|SIGINT|SIGQUIT|SIGHUP|SIGKILL|SIGUSR1|SIGUSR2
exit_codes:          []int =[0]   — exit codes treated as success (0..255)
log_max_size:        size =100mb  — per-run log cap (effective task default)
log_on_full:         enum =drop_old — drop_new | drop_old | kill_task
keep_runs:           int          — row-count retention; 1..1000000
keep_for:            dur          — age retention; positive
healthy_after:       dur  =60s    — service uptime that counts as healthy: resets the restart counter and clears the failed-start streak (SERVICES only)
start_retries:       int  =3      — consecutive fast failures before an instance goes FATAL (SERVICES only)
notify_on_missed:    bool =true   — alert on missed scheduled runs; false silences daemon-wide (per-task still wins)
env:                 map<str,str> — inline env merged into every task; key ^[A-Za-z_][A-Za-z0-9_]*$, <=256 entries, value <=32KiB, no NUL
env_file:            path         — dotenv file merged into every task; relative to runwisp.toml dir
secrets:             map<str,str> — inline secrets merged into every task; never shown in API/UI
secrets_file:        path         — dotenv file merged beneath secrets; only the path is visible
```

### [tasks.&lt;name&gt;] (run-to-exit)

Required: the table + `run` (unless `compose_file`, where `run` is optional and selects `compose_mode`). `restart="always"` and `instances` are rejected on tasks (use `[services.*]`).

```
group:             string =Tasks   — UI grouping label
description:        string          — human description
cron:              string          — 5- or 6-field cron (optional leading seconds); also @hourly, @every 1h30m; omit => manual-only
timezone:          IANA string      — per-task TZ override (else [scheduler] timezone)
jitter:            dur              — cap how far this cron task's start may slip; needs cron (inherits [defaults])
run_on_start:      bool =false      — fire once at daemon start, on top of any cron (the @reboot equivalent)
api_trigger:       bool =true       — allow CLI/API/UI trigger; false = cron-only
catch_up:          enum =latest     — missed-firing policy: latest | all | skip
max_catch_up_runs: int  =100        — cap when catch_up=all; >=1
timeout:           dur              — per-attempt cap (inherits [defaults])
graceful_stop:     dur  =5s         — grace before SIGKILL on stop
stop_signal:       enum =SIGTERM    — stop-ladder signal (inherits [defaults]); SIGTERM|SIGINT|SIGQUIT|SIGHUP|SIGKILL|SIGUSR1|SIGUSR2
restart:           enum             — never | on_failure   (always => rejected on tasks)
max_concurrent:    int  =1          — concurrent run cap; 1..1024
queue_max:         int  =100        — queued-run depth; 0..10000
on_overlap:        enum =queue      — queue | skip | terminate
retry_attempts:    int  =0          — retries after a failed attempt; 0..100
retry_delay:       dur  =5s         — delay between retries (<=0 floors to 5s)
retry_backoff:     enum             — constant | linear | exponential
exit_codes:        []int =[0]       — exit codes treated as success (inherits [defaults]); 0..255
working_dir:       path             — process cwd; relative to runwisp.toml dir; ~ = home of whoever the
                                     task runs as (daemon's, or `user`'s — resolved at run time). Default: daemon cwd
shell:             path =/bin/sh    — interpreter for run (absolute path); invoked as <shell> -e -c <script>; see FAIL-FAST
umask:             string           — octal file-creation mask, e.g. "027" (else daemon umask)
env_base:          enum =inherit    — inherit (daemon's env, minus RUNWISP_*) | clean (PATH=/usr/bin:/bin,
                                      SHELL, HOME, USER/LOGNAME only, as crond gives a job). Host shell runs
                                      only — rejected on compose. env/secrets/params layer over either.
user:              string           — run as user or user:group (name/numeric id); needs daemon as root
log_max_size:      size =100mb      — per-run log cap
log_on_full:       enum =drop_old   — drop_new | drop_old | kill_task
keep_runs:         int              — row retention (inherits [defaults]); 1..1000000
keep_for:          dur              — age retention (inherits [defaults])
run:               string (req)     — shell command; with compose_file it is the command run in the service (see compose_mode)
compose_file:      path             — run a compose service instead of run=
compose_service:   string =taskname — which compose service; requires compose_file
compose_mode:      enum             — exec | run. exec = `docker compose exec` into the service's RUNNING container (requires run);
                                      run = `docker compose run --rm` fresh container (runs `run` if set, else the service's own
                                      command). Default: exec when run is set, else run. Command runs as `/bin/sh -e -c` inside the
                                      target; `shell` is rejected on compose units. exec ignores pull/with_deps/instances naming and
                                      never removes the container. Docker cannot cancel an exec: `timeout` kills only the local
                                      client, so bound long work inside (`timeout N …`); exec on a [services.*] unit emits a
                                      startup warning
env:               map<str,str>     — inline env (merged over defaults.env)
env_file:          path             — dotenv file
secrets:           map<str,str>     — inline secrets (merged over defaults.secrets); never shown in API/UI
secrets_file:      path             — dotenv file merged beneath secrets; only the path is visible
notify_on_failure: []string         — sugar → route on run.failed/timeout/crashed/missed; notifier ids, "id:override", or "inapp"
notify_on_success: []string         — sugar → route on run.succeeded
notify_on_missed:  bool =true       — alert on missed scheduled runs (inherits [defaults]); false silences
```

### [services.&lt;name&gt;] (long-running)

`restart=always` is forced. Not allowed (rejected by the strict loader): `cron`, `timezone`, `jitter`, `run_on_start`, `catch_up`, `max_catch_up_runs`, `restart`, `max_concurrent`, `queue_max`, `retry_*`. Shares the core task keys: `group` (default `Services`), `description`, `api_trigger`, `on_overlap` (default `skip`), `graceful_stop`, `stop_signal`, `working_dir`, `shell`, `umask`, `env_base`, `user`, `exit_codes`, `log_max_size`, `log_on_full`, `keep_runs`, `keep_for`, `run`/`compose_*`, `env`/`env_file`, `secrets`/`secrets_file`, `notify_on_failure`/`notify_on_success`/`notify_on_missed`. Service-only:

```
instances:           int  =1           — parallel instances; 1..64
restart_delay:       dur  =1s          — delay before a restart
restart_backoff:     enum =exponential — constant | linear | exponential
healthy_after:       dur  =60s         — uptime that counts as healthy: resets the restart counter and clears the failed-start streak
start_retries:       int  =3           — consecutive fast failures (exit below healthy_after) before an instance goes FATAL and stops restarting
priority:            int  =0           — boot start order across services; lower starts first, ties break on name
autostart:           bool =true        — start at boot; false boots it stopped until started from UI/API
depends_on:          []string          — services that must be healthy before this one starts at boot (order only)
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

Per-service override `[compose.<alias>.<svc>]` accepts: `group`, `description`, `api_trigger`, `timeout`, `graceful_stop`, `stop_signal`, `on_overlap`, `restart`, `instances`, `restart_delay`, `restart_backoff`, `healthy_after`, `start_retries`, `priority`, `autostart`, `exit_codes`, `log_max_size`, `log_on_full`, `keep_runs`, `keep_for`, `env`, `env_file`, `secrets`, `secrets_file`, `notify_on_failure`, `notify_on_success`. Not allowed: `run`/`compose_file`/`compose_service` (the parent block owns the backend), and the host-process keys `shell`/`umask`/`env_base`/`user`. `mode="stack"` forbids overrides and include/exclude. Per-service `notify_on_failure`/`notify_on_success` desugar into notify routes keyed by the generated task name, exactly like `[services.*]`. The reserved sub-table `[compose.<alias>.defaults]` accepts the same keys and applies them to every imported service before the per-service override wins (precedence: compose-import default → `defaults` → `<svc>`); its `notify_on_*` add routes to all services. A compose service literally named `defaults` is rejected (rename hint); `mode="stack"` forbids `defaults` too.

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

Common: `id` (req, non-empty, not "inapp", no ":"), `type` (req: `slack`|`discord`|`telegram`|`smtp`|`sendmail`|`webhook`), `template_path` (optional).

```
slack:    webhook_url (req); channel (optional; starts # or @)
discord:  webhook_url (req; http/https)
telegram: bot_token (req); chat_id (req); parse_mode (MarkdownV2 needs template_path)
smtp:     host(req); port 0..65535; tls starttls|implicit|none (default: 465→implicit else starttls);
          tls_skip_verify bool; from(req email); reply_to(email); to(req,>=1) + cc/bcc(emails);
          username + password (set together or both omitted); tls=none forbids credentials
sendmail: from(req email); to(req,>=1) + cc/bcc(emails); reply_to(email);
          sendmail_path (optional, must be absolute; default: /usr/sbin/sendmail,
          /usr/lib/sendmail, /usr/bin/sendmail, then $PATH — resolved at send time, not load).
          Pipes a text/plain RFC 5322 message to `<bin> -t -i`; no address on argv.
          Retries exit 75 (EX_TEMPFAIL) only; every other exit is permanent.
webhook:  url (req; http/https); headers (optional map<str,str>)
```

Secret-bearing values (`webhook_url`, `bot_token`, `password`, …) arrive final — use `${VAR}` / `${file:...}` substitution for indirection; never inline a secret you don't control. Secrets are never logged or sent over the cloud integration.

### [[notification_route]] (route events to channels; repeatable)

```
match.kind:     []string — run.started | run.succeeded | run.failed | run.timeout | run.stopped | run.crashed | run.missed | service.fatal | notify.delivery_failed
match.severity: string   — info | warn | error (optional)
match.task:     string   — glob over task name (optional)
notify:         []string (req, non-empty) — notifier ids (or "inapp"); "id:#override" inline target (slack #/@, telegram chat_id, smtp/sendmail email)
```

### FAIL-FAST (run script execution)

```
invocation      <shell> -e -c <script>   — POSIX shells (sh bash dash ash busybox ksh mksh zsh yash ...)
                <shell> -c <script>      — anything else (python3, perl, fish, ...); warned at boot + validate
effect          multi-line run stops at the first failing command; that command's exit code is the run's
NOT set         -u (write `set -u` yourself); -o pipefail (not POSIX — needs shell="/bin/bash")
opt out         `set +e` as the script's first line; no TOML key exists for this
```

## CLI

Persistent flags: `-c/--config` (=`runwisp.toml`, or `/etc/runwisp/runwisp.toml` at euid 0), `--data` (=`.runwisp`, or `/var/lib/runwisp` at euid 0), `-p/--port` (=`9477`), `--host` (=`127.0.0.1`), `--log-level` (debug|info|warn|error), `--log-format` (auto|text|json). Each has an env fallback the flag wins over: `RUNWISP_CONFIG`, `RUNWISP_DATA`, `RUNWISP_PORT`, `RUNWISP_HOST`, `RUNWISP_LOG_LEVEL`, `RUNWISP_LOG_FORMAT`.
Precedence for `-c`/`--data`: explicit flag > `RUNWISP_CONFIG`/`RUNWISP_DATA` env var > euid-derived default.
Env: `RUNWISP_PASSWORD` (else ephemeral per-boot), `RUNWISP_NO_AUTH` (1/true disables auth; mutually exclusive with RUNWISP_PASSWORD), `RUNWISP_TLS` (auto|off; overrides `[daemon] tls`, default `off`, applied on every load incl. reload), `RUNWISP_TRUST_PROXY` (CIDRs), `RUNWISP_CLOUD_TOKEN`, `RUNWISP_CLOUD_URL`.

Official Docker image: `runwisp/runwisp` (alpine default + `-debian` variant, amd64/arm64). Binds `0.0.0.0`, plain HTTP (daemon's `[daemon] tls` default of `off`; set `-e RUNWISP_TLS=auto` to opt into self-signed HTTPS), requires `RUNWISP_PASSWORD` or `RUNWISP_NO_AUTH=1` set or the entrypoint refuses to start; mount config at `/etc/runwisp/runwisp.toml` and data at `/var/lib/runwisp`. See https://docs.runwisp.com/getting-started/docker/.

```
runwisp                      — no subcommand: attach TUI to running daemon, else scaffold toml + spawn daemon + attach
                             — scaffold prompt (TTY only) also detects an adjacent compose file and/or a readable
                               crontab (DefaultCronPatterns: root gets /etc/crontab + /etc/cron.d/* + every spool,
                               others get only their own spool file) and offers to wire either/both into the starter;
                               a Blocked cron source falls back to the plain starter instead
                             — CRON CUTOVER: when the box also has a live cron unit and the install would be
                               system-scoped root-on-a-TTY, that one prompt does all three (scaffold include_cron,
                               install the system service, mask cron) and the caller attaches to the systemd
                               daemon instead of spawning its own. When the take-over isn't possible the prompt
                               says so and offers the plain scaffold; those jobs are then held (see include_cron)
runwisp daemon               — start headless daemon (no TUI)
runwisp tui                  — attach a TUI to a running daemon
runwisp validate             — validate runwisp.toml without starting anything
runwisp list                 — list configured tasks and schedules
runwisp status               — is the daemon alive?
runwisp exec <task>          — run a task and stream output;  --daemon (via running daemon) | --standalone (in-process), mutually exclusive
runwisp reload               — re-read runwisp.toml + reconcile live (== SIGHUP); validate-first, no run_on_start/catch-up
                             — prints the diff, then the newly-live config's warnings on `!` lines
                               (ReloadResult.warnings). Same set as boot / `validate` / status /
                               GET /api/info's config_warnings, re-derived per request so a reload's
                               replace boot's. A crontab job include_cron skipped is NOT a task
                               change — it only appears here.
runwisp restart              — stop + fresh start (applies restart-only settings, re-fires run_on_start/catch-up); delegates to systemd/launchd if service-installed; --local to pin the per-user unit
runwisp stop                 — shut the daemon down (delegates to systemd/launchd if service-installed); --local to pin the per-user unit
runwisp import cron [FILE]   — convert a crontab to runwisp.toml; -o/--output --write --force --dry-run --quiet --system
runwisp import supervisord [FILE...] — convert supervisord config to runwisp.toml; -o/--output --write --force --dry-run --quiet
                             — -o writes one standalone file; --write installs the two-tier layout
                               (tasks → machine-owned runwisp.d/imported.toml, root runwisp.toml's
                               [daemon].include wired to load it; both written atomically or rolled back).
                               Tasks from the staging file report "source": "staged" in list/status --json.
                             — stderr summary gives every source job one row (name, schedule, the full command,
                               wrapped not truncated) marked ✓ clean / ~ changed / ! needs a fix / - skipped,
                               plus file-level notes and a verdict line. A job that emitted no TOML still gets
                               a row. --quiet keeps only the ! rows; --quiet with a clean import prints nothing.
                             — --dry-run: same summary + the files a real run would touch, writes nothing.
                               Rejects --quiet (contradiction). No-op in stdout mode (already writes nothing).
                               Proves the generated content loads + reports a root that already
                               fails to load; prints that the merge is unchecked (needs the files
                               on disk). Two-tier plan comes from configedit.PlanStage.
                             — both crontab formats: working_dir = "~" (crond runs a job in the running
                               user's home). ~ resolves against whoever the task runs as — the daemon's
                               account when no `user` is emitted, the user column's when one is. A ~ on a
                               task with `user` stays literal through config.Load and is joined in
                               executor.resolveWorkingDir from the resolved credential's home.
                             — every imported task gets env_base = "clean"; the crontab's own PATH= lands in
                               the task's env and layers over it.
                             — cron's '%' rules are applied, not copied: an unescaped % ends the command
                               (rest = stdin, further % = newline), \% is a literal %. run is emitted as
                               crond would run it; lost stdin input is quoted in a # TODO on the run line
                               and blocks the row (!). A \%-only translation is a ~ row, no TODO.
                             — import COPIES; it never disables the source. Every summary ends with a "!" warning
                               that cron/supervisord still runs these jobs and each will run twice until the
                               operator turns the old one off. Suppressed only when there is no duplication yet
                               (nothing emitted, or the generated config doesn't validate).
runwisp promote [TASK...]    — put a derived task's block in the root runwisp.toml; --all --reload --dry-run
                             — acts on Task.Source.Promotable(): staged (MOVED out of
                               runwisp.d/imported.toml, file deleted when emptied) and cron (COPIED from
                               config.CronBlockTOML — the crontab is the definition and is never written).
                               A promoted cron job then dedupes against its crontab line via sameEntry,
                               so the line can stay indefinitely.
                             — surgical text move: the block's comments/formatting/# TODOs travel byte-for-byte.
                               Both files written as one transaction gated on the merged load, else neither changes.
                               Refuses (writing nothing) an unknown name, an already-native name, a compose-generated
                               task, or a config that doesn't load. --all with nothing staged exits 0.
                               Changes no behaviour — only which file defines the task — so a following reload
                               reports no task changes and a promoted service is not restarted; the reload only
                               refreshes the "staged" flag. Emptied staging file is deleted.
runwisp password             — print the daemon's ephemeral password (local socket; exit 5 under RUNWISP_NO_AUTH, refuses if RUNWISP_PASSWORD set)
runwisp openapi              — print the OpenAPI 3.1 spec (JSON) to stdout
runwisp schema               — print the runwisp.toml JSON Schema (draft 2020-12) to stdout; published at https://docs.runwisp.com/config.schema.json
runwisp agent-guide          — print a paste-ready AGENTS.md/CLAUDE.md snippet for driving RunWisp from an agent
runwisp cloud                — start in cloud mode; --token --url --env-file(=.env) --no-tui
runwisp demo                 — boot a throwaway, fully-populated instance; --cloud --token --url --env-file
runwisp service install      — install autostart; -y --print --dry-run --force --local --binary <path>
                             — DEFAULT scope is the system-wide singleton /etc/systemd/system/runwisp.service
                               (Linux, root, no fingerprint in the name). Refuses without root, naming
                               `sudo` and `--local`. macOS has no system scope yet: --local is required.
                             — --local installs ~/.config/systemd/user/runwisp-<fingerprint>.service (Linux,
                               + loginctl enable-linger) or ~/Library/LaunchAgents/com.runwisp.daemon.<fp>.plist.
                               Refused as root on Linux (systemctl --user has no bus for root under sudo).
                             — --data/--config override paths but never the scope; a system install also
                               requires the resolved config to be root-owned and not group/world-writable.
                             — unit lifecycle ONLY: it never touches cron. When a `takeover` would do something,
                               a successful install prints a note naming the live cron unit, its job count, and
                               `sudo runwisp takeover`. There is no --take-over-cron flag.
runwisp takeover             — retire cron in one step; --dry-run --force -y --allow-skipped-cron-jobs --binary
                             — works from NOTHING: no runwisp.toml needed. Computes one plan (internal/cutover),
                               prints it, asks once, executes. Steps: write runwisp.toml reading the crontabs it
                               found (or insert [daemon] include_cron into an existing config, surgically) ->
                               install the system service -> stop+mask cron -> start RunWisp -> reload a daemon
                               that was already running (so tasks the cron gate held go live immediately;
                               `enable --now` on an already-active unit is a no-op). Needs root + systemd.
                             — a failed start unmasks and restarts cron rather than leaving no scheduler.
                             — --dry-run ALWAYS prints a plan, blocked or not, then exits non-zero if blocked.
                               A finished take-over re-runs as a no-op (exit 0), so it is script-safe.
                             — blocked by (all reported at once, nothing written): not Linux/systemd; not root;
                               no cron jobs on the box; the config does not load; the config is not trusted for a
                               system-wide install; an operator-maintained include_cron that misses crontabs
                               (never rewritten — it prints the array to paste); a cron source that failed to
                               load (--allow-skipped-cron-jobs overrides only this one).
                             — a port held by RunWisp's OWN service is not a conflict (that daemon is the one being
                               handed the jobs); a hand-started daemon on that data dir must be stopped first.
                             — re-running it repairs a cron that came back: unit already installed -> re-mask only.
                             — off Linux it is blocked and names the manual route (`runwisp import cron
                               --write`, then `crontab -r`); masking cron needs systemd.
runwisp service uninstall    — remove autostart; -y --purge (also data dir) --force --local
runwisp service status       — show autostart status; --local
                             — install/status/uninstall/stop/restart all detect the installed scope; --local is
                               only needed to disambiguate when a system AND a per-user unit both exist
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
POST   /api/reload                               re-read runwisp.toml + reconcile live task set (validate-first; reads from disk, never edits definitions)
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
