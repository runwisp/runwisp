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

/** Nanoseconds as a TOML duration string. */
const duration = (ns: number) => q(durationText(ns));
/** Bytes as a TOML size string: "100MiB". */
const size = (bytes: number) => q(formatBytes(bytes).replace(" ", ""));

const SECOND_NS = 1e9;
/** config.DefaultRetryDelay. retry_delay shows in retry_attempts' note. */
const DEFAULT_RETRY_DELAY_NS = 5 * SECOND_NS;

/**
 * Each key's built-in default, as it renders here. The API serves resolved
 * values, so a key left out of runwisp.toml arrives as its default: a row equal
 * to its default is hidden, any other value (0 included) is shown. Mirrors
 * apps/runwisp/internal/config/defaults.go and the model.Default* constants.
 */
const DEFAULTS: Partial<Record<string, string>> = {
    catch_up: "1",
    max_concurrent: "1",
    max_queued: "100",
    on_overlap: q("queue"),
    retry_attempts: "0",
    graceful_stop: duration(5 * SECOND_NS),
    stop_signal: q("SIGTERM"),
    shell: q("/bin/sh"),
    env_base: q("inherit"),
    manual_trigger: "true",
    autostart: "true",
    instances: "1",
    restart: q("always"),
    restart_attempts: "3",
    restart_backoff: q("exponential"),
    restart_delay: duration(SECOND_NS),
    healthy_after: duration(60 * SECOND_NS),
    priority: "0",
    log_max_size: size(100 * 1024 * 1024),
    log_on_full: q("drop_old"),
    keep_for: duration(0),
};

function row(key: string, value: string | undefined, note?: string): DetailRow[] {
    if (value === undefined || value === DEFAULTS[key]) return [];
    return [{ key, value, ...(note && { note }) }];
}

// Absent means not set; 0 is a value.
const str = (v: string | undefined) => (v ? q(v) : undefined);
const num = (v: number | undefined) => (v === undefined ? undefined : String(v));
const dur = (v: number | undefined) => (v === undefined ? undefined : duration(v));

function scheduleRows(task: Task): DetailRow[] {
    if (!hasCron(task)) return [];
    return [
        ...row("cron", q(task.cron), humanizeCron(task.cron).humanized),
        ...row("timezone", str(task.timezone)),
        ...row("catch_up", num(task.catchUp)),
        ...row("jitter", dur(task.jitter)),
    ];
}

/** The retry curve and its first delay, in plain words. */
function retryNote(task: Task): string {
    const delay = task.retryDelay ?? DEFAULT_RETRY_DELAY_NS;
    return `${task.retryBackoff ?? "constant"}, from ${durationText(delay)}`;
}

/** Keys only [tasks.*] accepts: a [services.*] entry rejects them. */
function taskOnlyRows(task: Task): DetailRow[] {
    if (isService(task.kind)) return [];
    return [
        ...row("retry_attempts", num(task.retryAttempts), retryNote(task)),
        ...row("on_overlap", str(task.onOverlap)),
        ...row("max_concurrent", num(task.maxConcurrent)),
        ...row("max_queued", num(task.maxQueued)),
        ...row("run_on_start", task.runOnStart ? q(task.runOnStartMode ?? "daemon") : undefined),
        ...row("autostart", String(task.autostart), "the schedule starts paused"),
    ];
}

function executionRows(task: Task): DetailRow[] {
    return [
        ...row("timeout", dur(task.timeout)),
        ...taskOnlyRows(task),
        ...row("user", str(task.user)),
        ...row("working_dir", str(task.workingDir)),
        ...row("umask", str(task.umask)),
        ...row("graceful_stop", dur(task.gracefulStop)),
        ...row("stop_signal", str(task.stopSignal)),
        ...row(
            "manual_trigger",
            String(task.manualTrigger),
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
        ...row("autostart", String(task.autostart), "it stays down until you start it"),
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
    return [
        ...row("log_max_size", task.logMaxSize === undefined ? undefined : size(task.logMaxSize)),
        ...row("log_on_full", str(task.logOnFull)),
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
    ];
    const source: DetailSection[] = task.sourceFile
        ? [{ title: "Defined in", path: task.sourceFile, rows: [] }]
        : [];
    return [...command, ...sections.filter((s) => s.rows.length > 0), ...source];
}
