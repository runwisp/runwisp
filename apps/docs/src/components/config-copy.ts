// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Human-facing copy for the interactive config reference (ConfigReference.svelte).
//
// The JSON Schema (config.schema.json) stays the source of truth for STRUCTURE —
// types, defaults, enums, ranges — and can't drift from the daemon. But its
// descriptions are deliberately terse and agent-facing. This file supplies the
// friendly explanation and a worked example per key, keyed by the same dotted id
// the component builds (`tasks.cron`, `tasks.params.option`, …). Missing keys
// fall back to the schema description, so nothing ever renders blank.
//
// Voice: talk to the operator like a colleague (see apps/docs AGENTS.md rule 5).
// Keep prose in sync with the narrative pages under content/docs/configuration/.

export interface ValueExample {
    /** A concrete valid value, rendered as a code chip. */
    v: string;
    /** What it means, in plain English. */
    hint: string;
}

export interface KeyCopy {
    /** Friendly explanation shown in the row and its expanded panel. */
    summary: string;
    /** Short TOML snippet showing the key in use. Column-0 indentation is intentional. */
    example?: string;
    /**
     * Sample valid values with their meaning, shown as a designed "Examples" grid.
     * Set for keys where a spread of real values teaches more than a type name —
     * cron, durations with special units, etc. Duration/size fields without an
     * explicit list fall back to generic format samples (see ConfigReference.svelte).
     */
    values?: ValueExample[];
}

export const configCopy: Record<string, KeyCopy> = {
    // ── Identity & metadata ───────────────────────────────────────────────
    "tasks.run": {
        summary:
            "The shell command this task runs — the one thing every task needs. It runs under `/bin/sh -e -c`, so it stops at the first command that fails, and that command's exit code becomes the run's. Point `shell` at bash when you want arrays or pipefail.",
        example: `[tasks.heartbeat]
cron = "*/5 * * * *"
run  = "curl -fsS https://example.com/health"`,
        values: [
            { v: "/usr/local/bin/backup.sh", hint: "a script on disk" },
            { v: "pg_dump app | gzip > /backups/app.sql.gz", hint: "a shell pipeline" },
            { v: "docker compose -f /srv/app.yml pull", hint: "any command on your PATH" },
            { v: "make -C /srv/app deploy", hint: "run a make target" },
        ],
    },
    "tasks.description": {
        summary:
            "A human-readable line shown next to the task in the Web UI and TUI. Purely cosmetic — it never affects scheduling or execution, it just tells you and your teammates what the task is for.",
        example: `[tasks.backup]
description = "Nightly Postgres dump to S3"`,
    },
    "tasks.group": {
        summary:
            'The section this task is filed under in the Web UI and TUI sidebar. Give related tasks the same group to cluster them together; leave it out and the task lands under a default "Tasks" heading.',
        example: `[tasks.nightly-report]
group = "Reports"`,
    },

    // ── Scheduling ────────────────────────────────────────────────────────
    "tasks.cron": {
        summary:
            "When the task runs, written as cron. If you don't specify `cron`, you can still trigger this task manually.",
        example: `[tasks.healthcheck]
cron = "*/30 * * * * *"  # every 30s, aligned to :00 and :30
run  = "curl -fsS http://localhost:8080/healthz"`,
        values: [
            { v: "*/5 * * * *", hint: "every 5 minutes" },
            { v: "0 * * * *", hint: "top of every hour" },
            { v: "0 3 * * *", hint: "every day at 03:00" },
            { v: "0 9 * * 1-5", hint: "09:00 Monday–Friday" },
            { v: "*/15 9-17 * * *", hint: "every 15 min, 9am–5pm" },
            { v: "0 0 1 * *", hint: "midnight on the 1st of each month" },
            { v: "30 2 * * 0", hint: "02:30 every Sunday" },
            { v: "*/30 * * * * *", hint: "every 30 seconds (6-field, leading seconds)" },
            { v: "@hourly", hint: "shorthand for 0 * * * *" },
            { v: "@daily", hint: "shorthand for midnight every day" },
            { v: "@every 1h30m", hint: "fixed interval, ignores wall-clock alignment" },
        ],
    },
    "tasks.timezone": {
        summary:
            "The IANA zone this task's cron is evaluated in, e.g. `Europe/Bratislava`. Unset, it falls back to `[scheduler] timezone`, and then to the host's system zone. Reach for it when one job must run in a different region's local time than the rest.",
        example: `[tasks.eu-market-open]
cron     = "0 9 * * 1-5"
timezone = "Europe/Bratislava"`,
    },
    "tasks.jitter": {
        summary:
            "A ceiling on how far this cron task's start may slip, so a pile of jobs sharing a fire time (the classic 3am batch) take turns instead of spiking CPU and IO all at once. It's a deadline, not a fixed offset — on an idle box the task starts right on time, and the window only bites when jittered runs actually contend. Needs a `cron`; set it once in `[defaults]` to pace your whole schedule with one line.",
        example: `[tasks.nightly-report]
cron   = "0 3 * * *"
jitter = "30m"  # start may slip up to 30m while other jittered runs are busy`,
    },
    "tasks.run_on_start": {
        summary:
            "Run the task once, right after the daemon starts. On a task that also has a `cron`, it fires at startup *and* keeps its normal schedule; on a task with no `cron`, it runs only at startup and never again. It's the cron `@reboot` idea — reach for it to warm a cache or reconcile state on boot instead of waiting for the next scheduled tick.",
        example: `[tasks.warm-cache]
run_on_start = true
run          = "/usr/local/bin/warm-cache"
# no cron, so this runs once at every daemon start`,
    },
    "tasks.manual_trigger": {
        summary:
            "Whether operators can start the task by hand — from the CLI (`runwisp run <name>`), the REST API, the Web UI's Run button, or the TUI's `r` key. Set `false` to make the task cron-only: the \"Run Now\" button disappears from the Web UI and TUI, and the API returns 403, while cron firings, retries, and overlap handling are untouched. Reach for it on jobs that are genuinely risky to double-fire, like an overnight merge.",
        example: `[tasks.nightly-merge]
cron           = "0 4 * * *"
manual_trigger = false  # only the scheduler may start it`,
    },
    "tasks.catch_up": {
        summary:
            "What to do about scheduled runs that were missed while RunWisp (or the whole server) wasn't running, once it starts back up.",
        example: `[tasks.hourly-roll]
cron       = "0 * * * *"
catch_up   = "all"
on_overlap = "queue"`,
        values: [
            {
                v: "latest",
                hint: "run the most recent missed firing once, then resume as normal (the default)",
            },
            {
                v: "all",
                hint: 'replay every missed firing back-to-back — needs on_overlap = "queue"',
            },
            { v: "skip", hint: "forget the backlog and wait for the next scheduled run" },
        ],
    },
    "tasks.max_catch_up_runs": {
        summary:
            'A safety cap on how many missed firings [catch_up](#tasks.catch_up) = "all" replays, so a daemon that was down for a week doesn\'t unleash thousands of runs at once. Only consulted when catch_up is "all".',
        example: `[tasks.hourly-roll]
catch_up          = "all"
on_overlap        = "queue"
max_catch_up_runs = 24  # at most a day's worth`,
    },

    // ── Concurrency ───────────────────────────────────────────────────────
    "tasks.max_concurrent": {
        summary:
            "How many runs of this task may be going at once. When the limit is reached, [on_overlap](#tasks.on_overlap) decides what happens to the next trigger. Most cron-style work is happiest at `1`.",
        example: `[tasks.thumbnailer]
max_concurrent = 4`,
    },
    "tasks.on_overlap": {
        summary:
            "What happens when the task is triggered while it's already at its [max_concurrent](#tasks.max_concurrent) limit — which is `1` by default, so by default: while a previous run is still going.",
        example: `[tasks.queue-drain]
cron       = "*/10 * * * *"
on_overlap = "skip"  # never two drainers at once`,
        values: [
            {
                v: "queue",
                hint: "the new run waits in line and starts once the current run finishes (the default)",
            },
            {
                v: "skip",
                hint: "the new run is dropped and recorded as skipped — good for work that's pointless to start again while the last one is still going",
            },
            { v: "kill", hint: "the running run is stopped so the new one can take over" },
        ],
    },
    "tasks.max_queued": {
        summary:
            'Use with [on_overlap](#tasks.on_overlap) = "queue" to cap how many runs may wait in line at once. When the cap is reached, further triggers fail loudly with a "Queue Full" status instead of growing an unbounded backlog.',
        example: `[tasks.ingest]
on_overlap = "queue"
max_queued = 500`,
    },

    // ── Retries & timeout ─────────────────────────────────────────────────
    "tasks.retry_attempts": {
        summary:
            "Extra attempts after the first one fails — `0` (the default) means no retries. Retries fire on `failed`, `timeout`, `crashed`, `log_overflow`, and `start_failed`; a manual stop or a kill ends the chain. See [retry_delay](#tasks.retry_delay) and [retry_backoff](#tasks.retry_backoff) for the timing.",
        example: `[tasks.flaky-fetch]
retry_attempts = 3
retry_delay    = "2s"`,
    },
    "tasks.retry_delay": {
        summary:
            "The base wait between retry attempts. Only used when [retry_attempts](#tasks.retry_attempts) is above 0; [retry_backoff](#tasks.retry_backoff) decides how this delay grows from one attempt to the next.",
        example: `[tasks.flaky-fetch]
retry_attempts = 3
retry_delay    = "5s"`,
    },
    "tasks.retry_backoff": {
        summary: "How [retry_delay](#tasks.retry_delay) grows across attempts.",
        example: `[tasks.flaky-fetch]
retry_attempts = 4
retry_delay    = "2s"
retry_backoff  = "exponential"  # 2s, 4s, 8s, 16s`,
        values: [
            { v: "constant", hint: "wait the same retry_delay before every attempt (the default)" },
            { v: "linear", hint: "retry_delay × attempt number — 2s, 4s, 6s, 8s…" },
            { v: "exponential", hint: "double each time — 2s, 4s, 8s, 16s…" },
        ],
    },
    "tasks.timeout": {
        summary:
            "A per-attempt wall-clock cap. When a run runs longer, RunWisp stops it through the [stop_signal](#tasks.stop_signal) → [graceful_stop](#tasks.graceful_stop) → SIGKILL ladder and records `timeout`. Unset here it inherits `[defaults]`, and unset there means no limit at all.",
        example: `[tasks.queue-drain]
cron    = "*/10 * * * *"
timeout = "9m"  # die before the next firing`,
    },
    "tasks.graceful_stop": {
        summary:
            'How long a stopping run gets to exit cleanly after it receives the [stop_signal](#tasks.stop_signal), before RunWisp SIGKILLs it. It applies on timeout, an `on_overlap = "kill"`, a manual stop, and daemon shutdown, and covers the whole process group. Set `"0s"` to skip the grace and kill immediately.',
        example: `[tasks.queue-drain]
graceful_stop = "30s"  # this drainer needs more than the 5s default`,
    },
    "tasks.stop_signal": {
        summary:
            "The signal that opens the stop ladder (the `SIG` prefix is optional). Default `SIGTERM`; pick `SIGINT` for tools that treat it like Ctrl-C, or `SIGKILL` to skip the grace window entirely. Whatever you pick, anything still alive after [graceful_stop](#tasks.graceful_stop) is SIGKILLed.",
        example: `[tasks.worker]
stop_signal   = "SIGINT"
graceful_stop = "10s"`,
    },

    // ── What counts as a failure ──────────────────────────────────────────
    "tasks.failures": {
        summary:
            'Which run outcomes count as a failure — the thing that turns a run red, bumps the "failed" stat, and fires [notify](#tasks.notify). Each token is a reason name (`failed`, `timeout`, `crashed`, `missed`, `stopped`, …) or an exit code / inclusive range (`"42"`, `"1-23"`). A bare list replaces the inherited set; prefix every token with `+`/`-` to adjust it instead (you can\'t mix the two styles). It\'s purely an observability choice — it never changes whether a task retries (see [retry_attempts](#tasks.retry_attempts)).',
        example: `[tasks.web]
failures = ["-missed"]              # stop paging on a missed tick

[tasks.rsync]
failures = ["timeout", "23", "24"]  # only these exits fail; other non-zero exits stay neutral`,
    },

    // ── Logs & retention ──────────────────────────────────────────────────
    "tasks.log_max_size": {
        summary:
            "The cap on one run's captured stdout/stderr on disk. Units are `b`/`kb`/`mb`/`gb`/`tb`; the default is 100mb. When a run hits the cap, [log_on_full](#tasks.log_on_full) decides what happens. Bare `0`, negatives, and malformed strings are rejected at load.",
        example: `[tasks.chatty-job]
log_max_size = "500mb"`,
    },
    "tasks.log_on_full": {
        summary: "What to do when a run's log reaches [log_max_size](#tasks.log_max_size).",
        example: `[tasks.verbose-import]
log_max_size = "50mb"
log_on_full  = "drop_old"`,
        values: [
            { v: "drop_old", hint: "keep the newest output, discard the oldest (the default)" },
            { v: "drop_new", hint: "freeze the log at the earliest output, ignore the rest" },
            { v: "kill", hint: "stop the run and record log_overflow" },
        ],
    },
    "tasks.keep_runs": {
        summary:
            "Keep only the N most recent completed runs of this task; older rows and their logs are trimmed on the hourly cleanup. Omit to inherit `[defaults]`; `0` keeps no completed runs at all. Set alongside [keep_for](#tasks.keep_for) and whichever one trims harder is the one you'll feel.",
        example: `[tasks.noisy-poller]
keep_runs = 200`,
    },
    "tasks.keep_for": {
        summary:
            'Delete runs older than this age. Unlike other durations, `keep_for` also accepts `d` (days) and `w` (weeks), so `"30d"` and `"2w"` work here (and nowhere else). Omit to inherit `[defaults]`; combine with [keep_runs](#tasks.keep_runs) and the stricter limit wins.',
        example: `[tasks.audit]
keep_for = "90d"`,
        values: [
            { v: "48h", hint: "2 days" },
            { v: "14d", hint: "14 days (d is retention-only)" },
            { v: "2w", hint: "2 weeks (w is retention-only)" },
            { v: "90d", hint: "about 3 months" },
            { v: "1h30m", hint: "mixed units work too" },
        ],
    },

    // ── Working directory & shell ─────────────────────────────────────────
    "tasks.working_dir": {
        summary:
            "The directory the process runs in. Relative paths resolve against the folder `runwisp.toml` lives in, and `~` is the home of whoever the task runs as. Existence is checked at run time, not config load. Unset, the run inherits whatever directory the daemon was started from.",
        example: `[tasks.build]
working_dir = "/srv/app"
run         = "./bin/build"`,
    },
    "tasks.shell": {
        summary:
            "The interpreter for `run`, as an absolute path. Default `/bin/sh`; set `/bin/bash` when you want arrays, `[[ ]]`, or `set -o pipefail`. It's invoked as `<shell> -e -c <script>`, so fail-fast is armed for POSIX shells (RunWisp tells you at boot when it can't arm it). Can also be set once in `[defaults]`.",
        example: `[tasks.report]
shell = "/bin/bash"
run   = "set -o pipefail; make report | tee out.log"`,
    },
    "tasks.umask": {
        summary:
            'The octal file-creation mask applied to the run — `"027"` makes new files `rw-r-----` and directories `rwxr-x---`, handy when a job writes dumps that shouldn\'t be world-readable. Write at least three digits (`"022"`, not the ambiguous `"22"`). It\'s applied inside the run\'s own process, so concurrent runs never affect each other. Empty inherits the daemon\'s umask.',
        example: `[tasks.dump]
umask = "027"`,
    },
    "tasks.env_base": {
        summary:
            "What the run's environment starts from, before `env`/`secrets` layer on top. Host-shell only — it's an error on a compose-backed task.",
        example: `[tasks.legacy-nightly]
env_base = "clean"
run      = "/usr/local/bin/nightly"`,
        values: [
            {
                v: "inherit",
                hint: "start from the daemon's own environment (the default) — handy, but the run then depends on how the daemon was launched",
            },
            {
                v: "clean",
                hint: "start from just PATH, SHELL, HOME, and USER/LOGNAME — exactly what cron gives a job, so runs stay reproducible; declare the rest in env",
            },
        ],
    },
    "tasks.user": {
        summary:
            "Drop the run to another OS account, written as `user` or `user:group` (name or numeric id — the same shorthand `chown` and Docker Compose use). RunWisp resolves the account when the run starts, seeds its `HOME`/`USER`/`LOGNAME`, and applies its groups. It only works when the daemon itself runs as root; otherwise the run fails loudly rather than quietly staying as the daemon's user. Empty keeps the daemon's identity.",
        example: `[tasks.report]
user = "reporter:reporter"`,
    },

    // ── Compose-backed tasks ──────────────────────────────────────────────
    "tasks.compose_file": {
        summary:
            "Point the task at a docker-compose file so a firing runs a compose service instead of a plain `run`. With only `compose_file` set, each firing does `docker compose run --rm <svc>` with the service's own command. Pair it with `run` and `compose_mode = \"exec\"` to run your command inside the service's already-running container instead.",
        example: `[tasks.nightly-backup]
cron            = "0 3 * * *"
compose_file    = "./docker-compose.yml"
compose_service = "backup"`,
    },
    "tasks.compose_service": {
        summary:
            "Which service inside the [compose_file](#tasks.compose_file) to run. Defaults to the task name when omitted. Requires `compose_file`.",
        example: `[tasks.nightly-backup]
compose_file    = "./docker-compose.yml"
compose_service = "backup"`,
    },
    "tasks.compose_mode": {
        summary:
            "How a [compose_file](#tasks.compose_file)-backed unit runs. Defaults to `exec` when you've set a `run` command, otherwise `run`.",
        example: `[tasks.migrate]
compose_file = "./docker-compose.yml"
compose_mode = "exec"
run          = "rails db:migrate"`,
        values: [
            { v: "run", hint: "start a fresh container each firing (docker compose run --rm)" },
            {
                v: "exec",
                hint: "run your command inside the service's already-running container (docker compose exec)",
            },
        ],
    },

    // ── Environment & secrets ─────────────────────────────────────────────
    "tasks.env": {
        summary:
            "Specify environment variables here to inject into the process. Only use it for **non-secret**, public values — **NOT passwords of any kind**; for those use [secrets](#tasks.secrets) instead. To read variables from a file, use [env_file](#tasks.env_file).",
        example: `[tasks.backup.env]
BACKUP_BUCKET = "s3://prod-backups"
DRY_RUN       = "0"`,
    },
    "tasks.env_file": {
        summary:
            "Path to a dotenv file merged beneath [env](#tasks.env) (inline entries win, docker-compose style). Its values are **visible** just like `env`. Paths may be absolute, `~/`-relative, or relative to the `runwisp.toml` directory. Plain `KEY=VALUE` lines read literally — no shell expansion.",
        example: `[tasks.backup]
env_file = "backup.env"`,
    },
    "tasks.secrets": {
        summary:
            "Specify secret environment variables here — passwords, tokens, API keys. They work exactly like [env](#tasks.env), except their keys and values **never leave the daemon**: the REST API and Web UI never show them. To read secrets from a file, use [secrets_file](#tasks.secrets_file).",
        example: `[tasks.backup.secrets]
RESTIC_PASSWORD = "\${file:~/.config/runwisp/restic.pass}"`,
    },
    "tasks.secrets_file": {
        summary:
            "Path to a dotenv file merged beneath [secrets](#tasks.secrets). Only the **path** is ever visible in the API/UI; the contents are not. Same path resolution and plain-dotenv format as [env_file](#tasks.env_file).",
        example: `[tasks.backup]
secrets_file = "/etc/runwisp/backup-secrets.env"`,
    },

    // ── Notifications ─────────────────────────────────────────────────────
    "tasks.notify": {
        summary:
            'Notifier ids to page whenever a run ends in something [failures](#tasks.failures) classifies as a failure. Entries are ids from your `[notifiers.<id>]` blocks (or `inapp`), optionally `"id:override"` to retarget a channel. Whatever is listed in `[notify] global_notifiers` (default `["inapp"]`) is added automatically. To alert on a non-failure outcome instead — a success ping, a timeout-only escalation — add an explicit `[[route]]` matching that kind.',
        example: `[tasks.deploy]
notify = ["slack-ops"]`,
    },

    // ── Parameters ────────────────────────────────────────────────────────
    "tasks.params": {
        summary:
            "If you need to pass variable options, arguments, or environment variables into your tasks or services, declare them with `params`. Each one you list turns into a field on the Web UI, TUI, and REST API's Run Task form, so an operator can fill it in per run. Every entry names exactly one of `env`/`arg`/`option`/`flag`, which decides how the value reaches your command. Values always arrive as real argv entries or env vars, never spliced into the shell string, so an operator typing `; rm -rf /` gets an inert string. Scheduled runs (cron, catch-up, retries) use the declared defaults.",
        example: `[tasks.backup]
run = "/usr/local/bin/backup.sh"
params = [
  { env = "PROJECT_ID", required = true },
  { arg = "source", required = true },
  { arg = "dest", default = "/backups" },
  { option = "--region", choices = ["us", "eu"] },
  { flag = "--force" },
]`,
    },
    "tasks.params.env": {
        summary:
            "Identity keyword: expose this parameter's value as an environment variable of the given name. Exactly one identity keyword (`env`/`arg`/`option`/`flag`) per entry. An `env` parameter can't collide with a static `env`/`secrets` key on the same task.",
        example: `{ env = "PROJECT_ID", required = true }`,
    },
    "tasks.params.arg": {
        summary:
            "Identity keyword: append this parameter's value as a positional argument to the final command, in declaration order. Exactly one identity keyword per entry.",
        example: `{ arg = "source", required = true }`,
    },
    "tasks.params.option": {
        summary:
            "Identity keyword: render this parameter as `--name value`. A name ending in `=` renders glued together (`--date=2026-01-01`) instead of two tokens. Exactly one identity keyword per entry.",
        example: `{ option = "--region", choices = ["us", "eu"] }`,
    },
    "tasks.params.flag": {
        summary:
            "Identity keyword: a boolean whose token is appended when it's on and omitted when off. Takes neither `choices` nor `type`. Exactly one identity keyword per entry.",
        example: `{ flag = "--force" }`,
    },
    "tasks.params.default": {
        summary:
            "The value scheduled runs use and manual forms pre-fill. For a `flag`, use `true`/`false`. A parameter that has a `default` is safe on a scheduled task, because a cron tick has no operator to prompt.",
        example: `{ arg = "dest", default = "/backups" }`,
    },
    "tasks.params.required": {
        summary:
            "The manual form won't submit without a value. Applies to `env`/`arg`/`option`. A required parameter can only sit on a scheduled task if it also has a `default` — otherwise a cron tick would have no one to ask.",
        example: `{ arg = "source", required = true }`,
    },
    "tasks.params.type": {
        summary:
            "The value's type hint: `string` (the default) or `number`; a `number` that doesn't parse is rejected before the run starts. Applies to `env`/`arg`/`option`.",
        example: `{ option = "--limit", type = "number", default = 100 }`,
    },
    "tasks.params.choices": {
        summary:
            "Restrict the value to a fixed list, rendered as a dropdown in the form; values outside the list are rejected. Applies to `env`/`arg`/`option`. Pair with `allow_custom` to also let operators type their own.",
        example: `{ option = "--region", choices = ["us", "eu"] }`,
    },
    "tasks.params.allow_custom": {
        summary:
            "With `choices`, also let the operator type a value that isn't in the list. It's meaningless — and rejected — without `choices`.",
        example: `{ option = "--tag", choices = ["stable", "beta"], allow_custom = true }`,
    },
    "tasks.params.description": {
        summary:
            "Help text shown under the field in the manual trigger form. Applies to any parameter.",
        example: `{ env = "PROJECT_ID", description = "GCP project to back up" }`,
    },
};

// Display order per section, roughly most-commonly-reached first, so the keys a
// typical operator wants sit at the top instead of in schema order. Keys not
// listed keep their schema order after the listed ones. The component sorts each
// section's fields by this; nested tables (params) stay in their schema order.
export const fieldOrder: Record<string, string[]> = {
    tasks: [
        "run",
        "cron",
        "description",
        "group",
        "timezone",
        "on_overlap",
        "max_concurrent",
        "timeout",
        "retry_attempts",
        "retry_delay",
        "retry_backoff",
        "env",
        "secrets",
        "env_file",
        "secrets_file",
        "params",
        "notify",
        "failures",
        "working_dir",
        "shell",
        "user",
        "run_on_start",
        "manual_trigger",
        "max_queued",
        "catch_up",
        "max_catch_up_runs",
        "jitter",
        "graceful_stop",
        "stop_signal",
        "log_max_size",
        "log_on_full",
        "keep_runs",
        "keep_for",
        "umask",
        "env_base",
        "compose_file",
        "compose_service",
        "compose_mode",
    ],
};
