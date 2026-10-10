// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { isService, type Task } from "@runwisp/common";
import { formatBytes, humanizeCron } from "@runwisp/ui";
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
    rows: DetailRow[];
}

const q = (s: string) => JSON.stringify(s);

/** Nanoseconds as a TOML duration string: "1h30m", "250ms". */
function duration(ns: number): string {
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
    return q(out || "0s");
}

function row(key: string, value: string | undefined, note?: string): DetailRow[] {
    if (value === undefined) return [];
    return [{ key, value, ...(note && { note }) }];
}

// The API serves resolved values, so a key left at its default looks the same
// as one set to it. Zero and empty mean "not set" here, and are hidden.
const isSet = (v: number | undefined): v is number => v !== undefined && v !== 0;
const str = (v: string | undefined) => (v ? q(v) : undefined);
const num = (v: number | undefined) => (isSet(v) ? String(v) : undefined);
const dur = (v: number | undefined) => (isSet(v) ? duration(v) : undefined);

function scheduleRows(task: Task): DetailRow[] {
    if (!hasCron(task)) return [];
    return [
        ...row("cron", q(task.cron), humanizeCron(task.cron).humanized),
        ...row("timezone", str(task.timezone)),
        ...row("catch_up", num(task.catchUp)),
        ...row("jitter", dur(task.jitter)),
    ];
}

function retryNote(task: Task): string | undefined {
    if (!task.retryBackoff) return undefined;
    const from = isSet(task.retryDelay) ? `, from ${duration(task.retryDelay).slice(1, -1)}` : "";
    return task.retryBackoff + from;
}

function executionRows(task: Task): DetailRow[] {
    return [
        ...row("timeout", dur(task.timeout)),
        ...row("retry_attempts", num(task.retryAttempts), retryNote(task)),
        ...row("on_overlap", isService(task.kind) ? undefined : str(task.onOverlap)),
        ...row("max_concurrent", num(task.maxConcurrent)),
        ...row("max_queued", num(task.maxQueued)),
        ...row("run_on_start", task.runOnStart ? q(task.runOnStartMode ?? "daemon") : undefined),
        ...row("user", str(task.user)),
        ...row("working_dir", str(task.workingDir)),
        ...row("umask", str(task.umask)),
        ...row("graceful_stop", dur(task.gracefulStop)),
        ...row(
            "manual_trigger",
            task.manualTrigger ? undefined : "false",
            "the UI and API can't run or control it",
        ),
    ];
}

function serviceRows(task: Task): DetailRow[] {
    if (!isService(task.kind)) return [];
    return [
        ...row("instances", num(task.instances)),
        ...row("restart", str(task.restart)),
        ...row(
            "restart_attempts",
            num(task.restartAttempts),
            "fast failures in a row, then it gives up",
        ),
        ...row("restart_backoff", str(task.restartBackoff)),
        ...row("restart_delay", dur(task.restartDelay)),
        ...row("healthy_after", dur(task.healthyAfter)),
        ...row("autostart", String(task.autostart)),
        ...row("stop_signal", str(task.stopSignal)),
        ...row("priority", num(task.priority)),
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

function environmentRows(task: Task): DetailRow[] {
    const env = Object.entries(task.env ?? {}).sort(([a], [b]) => a.localeCompare(b));
    return [
        ...row("env_base", str(task.envBase)),
        ...env.map(([k, v]) => ({ key: `env.${k}`, value: q(v) })),
        ...row("env_file", str(task.envFile)),
        ...row("secrets_file", str(task.secretsFile), "values not shown"),
    ];
}

function outputRows(task: Task): DetailRow[] {
    const size = isSet(task.logMaxSize)
        ? q(formatBytes(task.logMaxSize).replace(" ", ""))
        : undefined;
    return [
        ...row("log_max_size", size, task.logOnFull ? `${task.logOnFull} when full` : undefined),
        ...row("keep_runs", num(task.keepRuns)),
        ...row("keep_for", dur(task.keepFor)),
    ];
}

/** The task's configuration in runwisp.toml terms, the sections that apply. */
export function taskDetailSections(task: Task): DetailSection[] {
    const command: DetailSection[] = task.run
        ? [{ title: "Command", command: task.run, rows: row("shell", str(task.shell)) }]
        : [];
    const sections: DetailSection[] = [
        { title: "Schedule", rows: scheduleRows(task) },
        { title: "Service", rows: serviceRows(task) },
        { title: "Parameters", rows: parameterRows(task) },
        { title: "Execution", rows: executionRows(task) },
        { title: "Environment", rows: environmentRows(task) },
        { title: "Output and retention", rows: outputRows(task) },
        { title: "Defined in", rows: row("file", task.sourceFile) },
    ];
    return [...command, ...sections.filter((s) => s.rows.length > 0)];
}
