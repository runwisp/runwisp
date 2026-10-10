// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { isService, type Task } from "@runwisp/common";
import { formatBytes, humanizeCron } from "@runwisp/ui";
import { TASK_DEFAULTS } from "./__task_defaults";
import { hasCron } from "./task";

export interface DetailRow {
    /** The runwisp.toml key. */
    key: string;
    /** As written in TOML: strings quoted, numbers bare. */
    value: string;
    /** A plain-words aside. */
    note?: string;
}

export interface DetailSection {
    title: string;
    /** The `run` command, shown as a block instead of rows. */
    command?: string;
    /** A file path, shown as plain text: it isn't a runwisp.toml key. */
    path?: string;
    rows: DetailRow[];
}

const q = (s: string) => JSON.stringify(s);

/** Nanoseconds as a duration: "1h30m", "250ms". */
function durationText(ns: number): string {
    const units: [string, number][] = [
        ["h", 3_600_000],
        ["m", 60_000],
        ["s", 1000],
        ["ms", 1],
    ];
    let rest = Math.round(ns / 1e6);
    let out = "";
    for (const [unit, size] of units) {
        const n = Math.floor(rest / size);
        if (n > 0) {
            out += String(n) + unit;
            rest -= n * size;
        }
    }
    return out || "0s";
}

// Absent means not set; 0 is a value.
const str = (v: string | undefined) => (v ? q(v) : undefined);
const num = (v: number | undefined) => (v === undefined ? undefined : String(v));
/** Nanoseconds as a TOML duration string. */
const dur = (v: number | undefined) => (v === undefined ? undefined : q(durationText(v)));
/** Bytes as a TOML size string: "100MiB". */
const size = (v: number | undefined) =>
    v === undefined ? undefined : q(formatBytes(v).replace(" ", ""));

/** Renders one key of a unit as written in TOML; undefined when unset. */
type Render = (t: Task) => string | undefined;
type Row = (key: string, render: Render, note?: string) => DetailRow[];

/**
 * A row builder for task. The API serves resolved values, so a key left out of
 * runwisp.toml arrives as its built-in default: a row that renders the same for
 * task and for the default unit of its kind is hidden, any other value (0
 * included) is shown. retry_attempts, priority and keep_for are omitted from
 * the API at 0, which is also their default, so a written 0 hides with them.
 */
function rowsOf(task: Task, defaults: Task): Row {
    return (key, render, note) => {
        const value = render(task);
        if (value === undefined || value === render(defaults)) return [];
        return [{ key, value, ...(note && { note }) }];
    };
}

function scheduleRows(task: Task, row: Row): DetailRow[] {
    if (!hasCron(task)) return [];
    return [
        ...row("cron", (t) => str(t.cron), humanizeCron(task.cron).humanized),
        ...row("timezone", (t) => str(t.timezone)),
        ...row("catch_up", (t) => num(t.catchUp)),
        ...row("jitter", (t) => dur(t.jitter)),
    ];
}

/** The retry curve and its first delay, in plain words. */
function retryNote(task: Task): string {
    const delay = task.retryDelay ?? TASK_DEFAULTS.task.retryDelay ?? 0;
    return `${task.retryBackoff ?? "constant"}, from ${durationText(delay)}`;
}

/** Keys only [tasks.*] accepts: a [services.*] entry rejects them. */
function taskOnlyRows(task: Task, row: Row): DetailRow[] {
    if (isService(task.kind)) return [];
    return [
        ...row("retry_attempts", (t) => num(t.retryAttempts), retryNote(task)),
        ...row("on_overlap", (t) => str(t.onOverlap)),
        ...row("max_concurrent", (t) => num(t.maxConcurrent)),
        ...row("max_queued", (t) => num(t.maxQueued)),
        ...row("run_on_start", (t) => (t.runOnStart ? q(t.runOnStartMode ?? "daemon") : undefined)),
        ...row("autostart", (t) => String(t.autostart), "the schedule starts paused"),
    ];
}

function executionRows(task: Task, row: Row): DetailRow[] {
    return [
        ...row("timeout", (t) => dur(t.timeout)),
        ...taskOnlyRows(task, row),
        ...row("user", (t) => str(t.user)),
        ...row("working_dir", (t) => str(t.workingDir)),
        ...row("umask", (t) => str(t.umask)),
        ...row("graceful_stop", (t) => dur(t.gracefulStop)),
        ...row("stop_signal", (t) => str(t.stopSignal)),
        ...row(
            "manual_trigger",
            (t) => String(t.manualTrigger),
            "the UI and API can't run or control it",
        ),
    ];
}

function serviceRows(task: Task, row: Row): DetailRow[] {
    if (!isService(task.kind)) return [];
    return [
        ...row("instances", (t) => num(t.instances)),
        ...row("restart", (t) => str(t.restart)),
        ...row(
            "restart_attempts",
            (t) => num(t.restartAttempts),
            "fast failures in a row, then it gives up",
        ),
        ...row("restart_backoff", (t) => str(t.restartBackoff)),
        ...row("restart_delay", (t) => dur(t.restartDelay)),
        ...row("healthy_after", (t) => dur(t.healthyAfter)),
        ...row("autostart", (t) => String(t.autostart), "it stays down until you start it"),
        ...row("priority", (t) => num(t.priority)),
    ];
}

function parameterRows(task: Task): DetailRow[] {
    return (task.parameters ?? []).map((p) => {
        const choices = p.choices ?? [];
        const value = [
            p.kind,
            p.required === true ? "required" : undefined,
            choices.length > 0 ? choices.join(" | ") : p.type,
            p.default ? `default ${p.default}` : undefined,
        ]
            .filter((part) => part !== undefined)
            .join(" · ");
        return { key: p.key, value, ...(p.description && { note: p.description }) };
    });
}

function environmentRows(task: Task, row: Row): DetailRow[] {
    const env = Object.entries(task.env ?? {}).sort(([a], [b]) => a.localeCompare(b));
    return [
        ...row("env_base", (t) => str(t.envBase)),
        ...env.map(([k, v]) => ({ key: `env.${k}`, value: q(v) })),
        ...row("env_file", (t) => str(t.envFile)),
        ...row("secrets_file", (t) => str(t.secretsFile), "values not shown"),
    ];
}

function outputRows(row: Row): DetailRow[] {
    return [
        ...row("log_max_size", (t) => size(t.logMaxSize)),
        ...row("log_on_full", (t) => str(t.logOnFull)),
        ...row("keep_runs", (t) => num(t.keepRuns)),
        ...row("keep_for", (t) => dur(t.keepFor)),
    ];
}

/** The task's configuration in runwisp.toml terms, the sections that apply. */
export function taskDetailSections(task: Task): DetailSection[] {
    const row = rowsOf(task, TASK_DEFAULTS[isService(task.kind) ? "service" : "task"]);
    const command: DetailSection[] = task.run
        ? [{ title: "Command", command: task.run, rows: row("shell", (t) => str(t.shell)) }]
        : [];
    const sections: DetailSection[] = [
        { title: "Schedule", rows: scheduleRows(task, row) },
        { title: "Service", rows: serviceRows(task, row) },
        { title: "Parameters", rows: parameterRows(task) },
        { title: "Execution", rows: executionRows(task, row) },
        { title: "Environment", rows: environmentRows(task, row) },
        { title: "Output and retention", rows: outputRows(row) },
    ];
    const source: DetailSection[] = task.sourceFile
        ? [{ title: "Defined in", path: task.sourceFile, rows: [] }]
        : [];
    return [...command, ...sections.filter((s) => s.rows.length > 0), ...source];
}
