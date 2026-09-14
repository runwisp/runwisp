// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Layout + cross-cutting prose for the interactive config reference
// (ConfigReference.svelte). The JSON Schema stays the source of truth for the
// KEYS (types, defaults, enums, ranges); this file decides how those keys are
// grouped into clusters and slots in the narrative that belongs to a *group* of
// keys rather than any single one — fail-fast (run + shell), the env merge order
// (the four env keys), omit-vs-empty (all of params.*), and so on.
//
// A Topic renders as a first-class row in the same searchable, collapsible,
// deep-linkable list as the keys — never stuffed inside a key's body. A Topic
// with `lead: true` renders as an always-open intro under the cluster header
// instead.
//
// Voice: talk to the operator like a colleague (apps/docs AGENTS.md rule 5).
// Keep prose in sync with content/docs/configuration/tasks.mdx — the goal is for
// this page to eventually absorb it.

/** One block of topic prose: a paragraph or a bullet list. Both support inline
 *  `code` and `[label](#tasks.other_key)` links (parsed in ConfigReference). */
export type Prose = { p: string } | { ul: string[] };

export interface Topic {
    /** Slug — becomes the element id `tasks.<id>` and the hash link target.
     *  MUST NOT collide with a task key name (use hyphens, e.g. `fail-fast`). */
    id: string;
    title: string;
    /** One-liner shown collapsed and as the lead line when expanded. */
    summary: string;
    body?: Prose[];
    /** Optional TOML snippet, rendered like a key example. */
    example?: string;
    /** Render as an always-open intro under the cluster header, not a row. */
    lead?: boolean;
}

export interface Cluster {
    id: string;
    /** Sub-heading shown above the cluster's rows. */
    title: string;
    /** Keys (by name) and topics, in display order. A string is a task key
     *  name resolved against the schema-built fields; an object is a Topic. */
    entries: (string | Topic)[];
}

const NAMING: Topic = {
    id: "naming",
    lead: true,
    title: "Task names",
    summary:
        "The table key — `backup-db` in `[tasks.backup-db]` — is the task name, and it shows up in the CLI, the API, the Web UI, and the on-disk log path.",
    body: [
        {
            p: 'Names use letters, digits, and `. _ - :` (up to 100 characters) and must be unique across both `[tasks.*]` and `[services.*]`. Anything beyond TOML\'s bare-key set needs quotes around the table key — `[tasks."db:backup"]`. On disk, log directories flatten `:` and `.` to `_`, so `db:backup` logs land under `db_backup/`.',
        },
        {
            p: "Really only two things are required: the table itself and a `run`. Everything else inherits from `[defaults]` or comes with a sensible built-in default.",
        },
    ],
};

const FAIL_FAST: Topic = {
    id: "fail-fast",
    title: "Fail-fast",
    summary:
        "A multi-line `run` executes under `<shell> -e -c`, so it stops at the first command that fails — and that command's exit code becomes the run's.",
    body: [
        {
            p: "Without `-e`, a shell runs a script to the end and reports only the last command's status, so a three-line script whose middle line blows up finishes green. RunWisp arms `-e` for you — you don't have to write `set -e`.",
        },
        { p: "Two things it deliberately doesn't do:" },
        {
            ul: [
                "`-u` (error on unset variables) is a lint, not a failure detector, and it breaks scripts that legitimately read optional env vars. Write `set -u` yourself if you want it.",
                "`-o pipefail` isn't POSIX — dash rejects it outright. To catch a failing upstream in a pipe, set [shell](#tasks.shell) to `/bin/bash` and write `set -o pipefail` yourself.",
            ],
        },
        {
            p: "To turn fail-fast off for one task, make `set +e` the first line of its `run`. For a single command that's expected to exit non-zero, end it with `|| true`.",
        },
    ],
};

const FAILURE_MODEL: Topic = {
    id: "failure-model",
    title: "What counts as a failure",
    summary:
        "Every run ends with a reason; [failures](#tasks.failures) decides which reasons (and exit codes) turn a run red and page you.",
    body: [
        {
            ul: [
                'Each token is a reason (`failed`, `timeout`, `crashed`, `log_overflow`, `start_failed`, `missed`, `stopped`, `daemon_stopped`) or an exit code / inclusive range (`"42"`, `"1-23"`; codes are 1–255, exit 0 is always success).',
                'List `"failed"` and every non-zero exit is a failure. Omit it and only the codes you list fail — every other non-zero exit stays neutral: recorded and visible, but not red and not paged.',
                "A bare list replaces the inherited set; prefix every token with `+`/`-` to adjust it instead. You can't mix the two styles in one list.",
            ],
        },
        {
            p: "It's purely an observability choice — it never changes retries, which always fire on `failed`, `timeout`, `crashed`, `log_overflow`, and `start_failed`, whatever you list here.",
        },
    ],
};

const ENV_MERGE: Topic = {
    id: "env-merge",
    title: "How env & secrets merge",
    summary:
        "All four layers merge on top of the daemon's own environment; on a key collision, later wins.",
    body: [
        {
            ul: [
                "The daemon process's own environment (the base).",
                "[env_file](#tasks.env_file), then [env](#tasks.env) — the file merges beneath the inline map, docker-compose style, so inline entries win.",
                "[secrets_file](#tasks.secrets_file), then [secrets](#tasks.secrets) — same rule.",
                "Secrets sit above plain env, so a secret always beats a same-named public value.",
            ],
        },
        {
            p: "The split is about visibility, not mechanics: `env`/`env_file` values sit in the open next to `cron` and `run` in the API, UI, and CLI, while `secrets`/`secrets_file` reach the process the same way but never leave the daemon — at most you'll see the `secrets_file` path.",
        },
        {
            p: "It's all validated at load: keys match `^[A-Za-z_][A-Za-z0-9_]*$`, no NUL bytes, no single value over 32 KiB, and at most 256 env + secrets entries per task.",
        },
    ],
};

const OMIT_VS_EMPTY: Topic = {
    id: "omit-vs-empty",
    title: "Omitting a value vs. passing an empty one",
    summary:
        "When you trigger a task by hand, clearing a field and passing an empty string are genuinely different.",
    body: [
        {
            ul: [
                "Leave it as-is and the value (the default, or whatever you typed) is passed.",
                "Clear it and the parameter is omitted — the option, arg, or env var isn't passed at all. Clearing does not fall back to the default; a `required` field can't be omitted.",
                'Pass an empty string when your program distinguishes `--note \'\'` from no `--note`: use the toggle under the field (in the TUI, ctrl+t) to flip it from "omitted" to "passing empty string".',
            ],
        },
        {
            p: "Flags have no ambiguity — off means the token isn't appended. Scheduled runs and retries don't go through a form: they use the declared defaults, and a retry replays exactly what the original run was given, including anything deliberately omitted.",
        },
    ],
};

const REJECTED: Topic = {
    id: "rejected",
    title: "What's rejected on tasks",
    summary: "The config loader turns these away at startup, so they can't quietly bite you later.",
    body: [
        {
            ul: [
                "`restart` / `restart_attempts` — services-only; a task re-runs a failed run with [retry_attempts](#tasks.retry_attempts) / [retry_delay](#tasks.retry_delay) / [retry_backoff](#tasks.retry_backoff), not restart.",
                "`instances`, `priority`, `autostart`, `depends_on` — services-only fields.",
                "A task name that's also taken by a `[services.*]` — they share one namespace.",
                "An empty or missing `run`.",
            ],
        },
    ],
};

// Keys grouped as they are on the narrative tasks page; topics slotted where the
// prose belongs. Every task key should appear in exactly one cluster — anything
// missed still renders under a trailing "Other" cluster (see ConfigReference).
export const clusters: Cluster[] = [
    {
        id: "identity",
        title: "Identity & metadata",
        entries: [NAMING, "run", "description", "group", "manual_trigger"],
    },
    {
        id: "scheduling",
        title: "Scheduling",
        entries: ["cron", "timezone", "jitter", "catch_up", "max_catch_up_runs", "run_on_start"],
    },
    {
        id: "concurrency",
        title: "Concurrency",
        entries: ["max_concurrent", "on_overlap", "max_queued"],
    },
    {
        id: "retries",
        title: "Retries & timeout",
        entries: [
            "retry_attempts",
            "retry_delay",
            "retry_backoff",
            "timeout",
            FAIL_FAST,
            "graceful_stop",
            "stop_signal",
        ],
    },
    {
        id: "failures",
        title: "What counts as a failure",
        entries: [FAILURE_MODEL, "failures"],
    },
    {
        id: "retention",
        title: "Logs & retention",
        entries: ["log_max_size", "log_on_full", "keep_runs", "keep_for"],
    },
    {
        id: "env",
        title: "Environment & secrets",
        entries: [ENV_MERGE, "env", "secrets", "env_file", "secrets_file"],
    },
    {
        id: "params",
        title: "Parameters",
        entries: ["params", OMIT_VS_EMPTY],
    },
    {
        id: "shell",
        title: "Working directory & shell",
        entries: ["working_dir", "shell", "umask", "env_base", "user"],
    },
    {
        id: "compose",
        title: "Compose-backed tasks",
        entries: ["compose_file", "compose_service", "compose_mode"],
    },
    {
        id: "notifications",
        title: "Notifications",
        entries: ["notify"],
    },
    {
        id: "constraints",
        title: "Constraints",
        entries: [REJECTED],
    },
];
