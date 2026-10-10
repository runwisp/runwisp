// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A task handle with node-cron 4's ScheduledTask surface, so code written for
// node-cron keeps working. Scheduling itself happens in RunWisp; this class
// only runs the callback when RunWisp fires it and tracks node-cron's state.

import { randomUUID } from "node:crypto";

/** Where warnings and errors go. node-cron's Logger fits. */
export interface Logger {
    info(message: string): void;
    warn(message: string): void;
    error(message: string): void;
}

/** node-cron's RunCoordinator. Accepted and never called: RunWisp runs each task once per data directory. */
export interface RunCoordinator {
    shouldRun(key: string, ttlMs: number): boolean | Promise<boolean>;
    onComplete?(key: string): void | Promise<void>;
}

/** node-cron's TaskOptions. */
export interface NodeCronOptions {
    timezone?: string;
    name?: string;
    noOverlap?: boolean;
    maxExecutions?: number;
    /** Milliseconds; becomes the task's RunWisp `timeout`. */
    executeTimeout?: number;
    /** Ignored, with a warning: RunWisp runs each task once per data directory. */
    distributed?: boolean;
    /** Ignored, with a warning: RunWisp runs each task once per data directory. */
    runCoordinator?: RunCoordinator;
    /** Ignored, with a warning: RunWisp runs each task once per data directory. */
    distributedLease?: number;
    /** Ignored, with a warning: use task() with the `jitter` key instead. */
    maxRandomDelay?: number;
    /** Gets this task's warnings and its event listeners' errors instead of the RunWisp instance's log. */
    logger?: Logger;
    /** true: no warning for a missed run when nothing listens for execution:missed. */
    suppressMissedWarning?: boolean;
    /** Ignored: RunWisp fires a late tick when it can, and records ticks missed while it was down. */
    missedExecutionTolerance?: number;
    /** Ignored: a background task's process starts per run, under the run's timeout. */
    startTimeout?: number;
    /** Ignored: no timer in this process holds the schedule. close() lets the process exit. */
    unref?: boolean;
}

export interface Execution {
    id: string;
    reason: "invoked" | "scheduled";
    startedAt?: Date;
    finishedAt?: Date;
    error?: Error;
    result?: unknown;
}

/** What a callback gets for one run. */
export interface RunContext {
    /** Aborted when RunWisp stops the run (timeout, Stop button) or the app's connection to it drops. */
    signal: AbortSignal;
    /** The unit's `env`, `secrets` and env params, resolved by RunWisp. The process env stays the app's own. */
    env: Record<string, string>;
    /** The run's parameter values, by key. */
    params: Record<string, string>;
}

export interface TaskContext extends RunContext {
    /** The firing a scheduled run is for; for any other event, when it happened. */
    date: Date;
    /** date as local time in the task's timezone, with its offset. */
    dateLocalIso: string;
    triggeredAt: Date;
    task: ScheduledTask;
    execution: Execution;
    error?: Error;
}

export interface LastRun {
    date: Date;
    result?: unknown;
    error?: Error;
}

export type TaskFn = (context: TaskContext) => unknown;

/**
 * node-cron 4's events. `task:failed` and `execution:skipped` are accepted so
 * node-cron code type-checks, and never emitted: a background task's process
 * starts per run, so its crash fails that run (execution:failed), and RunWisp
 * has no run coordinator to skip one.
 */
export type TaskEvent =
    | "task:started"
    | "task:stopped"
    | "task:destroyed"
    | "task:failed"
    | "execution:started"
    | "execution:finished"
    | "execution:failed"
    | "execution:missed"
    | "execution:overlap"
    | "execution:maxReached"
    | "execution:skipped";

type Listener = (context: TaskContext) => unknown;

/** What a task needs from the RunWisp instance that owns it. */
export interface TaskControl {
    setPaused(name: string, paused: boolean): void;
    remove(name: string): void;
    /** task's next firing: what RunWisp reported while it is still ahead, else computed. */
    nextRun(task: ScheduledTask, pattern: string, timezone: string | undefined): Date | null;
    nextRuns(pattern: string, timezone: string | undefined, count: number): Date[];
    match(pattern: string, timezone: string | undefined, date: Date): boolean;
    /** The RunWisp instance's log, for a task without a logger of its own. */
    log(): Logger | undefined;
}

/** What only the owning RunWisp may do with a task: kept off the handle. */
export interface TaskInternals {
    /**
     * Runs the callback for one RunWisp run and rethrows its error, so the
     * run's exit code reflects it. at: the firing, for a run the schedule
     * started; a run started by hand has none and counts as invoked.
     */
    run(at: Date | undefined, run: RunContext): Promise<unknown>;
    /** RunWisp no longer runs this task: another took its name, or the instance closed. */
    retire(): void;
    /** RunWisp skipped the firing scheduled for at, since the previous run was still going. */
    overlap(at: Date): void;
    /** RunWisp missed firings while it was down, the latest at at. */
    missed(at: Date): void;
}

/** How createScheduledTask makes a task. */
export interface TaskSettings {
    /** The RunWisp task name. */
    id: string;
    name: string;
    /** The cron expression; "" for a task without a schedule. */
    pattern: string;
    /** The zone the pattern is evaluated in; undefined: the host's. */
    timezone: string | undefined;
    fn: TaskFn;
    maxExecutions: number | undefined;
    logger?: Logger | undefined;
    suppressMissedWarning?: boolean | undefined;
}

type Status = "stopped" | "idle" | "destroyed";

function toError(err: unknown): Error {
    return err instanceof Error ? err : new Error(String(err));
}

/** date as local time in timezone (undefined: the process's), ISO 8601 with its offset, as node-cron's dateLocalIso. */
export function localIso(date: Date, timezone: string | undefined): string {
    const parts = new Map(
        new Intl.DateTimeFormat("en-US", {
            ...(timezone === undefined ? {} : { timeZone: timezone }),
            hourCycle: "h23",
            year: "numeric",
            month: "2-digit",
            day: "2-digit",
            hour: "2-digit",
            minute: "2-digit",
            second: "2-digit",
            timeZoneName: "longOffset",
        })
            .formatToParts(date)
            .map((part) => [part.type, part.value]),
    );
    const part = (type: Intl.DateTimeFormatPartTypes) => parts.get(type) ?? "";
    // "GMT+02:00"; "GMT" or "GMT+00:00" at UTC, which is Z, as in node-cron.
    const gmt = part("timeZoneName").replace("GMT", "");
    const offset = /^([+-]00:00)?$/.test(gmt) ? "Z" : gmt;
    const ms = String(date.getUTCMilliseconds()).padStart(3, "0");
    return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}:${part("second")}.${ms}${offset}`;
}

export class ScheduledTask {
    /** The RunWisp task name. */
    readonly id: string;
    readonly name: string;
    readonly #control: TaskControl;
    readonly #settings: TaskSettings;
    #status: Status = "stopped";
    /** Scheduled runs in progress: node-cron's "running". */
    #busy = 0;
    /** Scheduled runs started, which maxExecutions counts. */
    #runs = 0;
    #last: LastRun | null = null;
    /** Listeners by event, in the order added; once: removed when first called. */
    readonly #listeners = new Map<TaskEvent, { listener: Listener; once: boolean }[]>();

    /** Use createScheduledTask: the internals go to the caller, not onto the handle. */
    constructor(
        control: TaskControl,
        settings: TaskSettings,
        bind: (internals: TaskInternals) => void,
    ) {
        this.#control = control;
        this.#settings = settings;
        this.id = settings.id;
        this.name = settings.name;
        bind({
            run: (at, run) => this.#run(at, run),
            retire: () => {
                if (this.#status === "destroyed") return;
                this.#halt();
                this.#status = "destroyed";
                this.#emit("task:destroyed");
            },
            overlap: (at) => {
                this.#log()?.warn(
                    `runwisp: task "${this.name}" is still running, so RunWisp skipped its run at ${localIso(at, settings.timezone)}`,
                );
                this.#emit("execution:overlap", at);
            },
            missed: (at) => {
                if (
                    (this.#listeners.get("execution:missed") ?? []).length === 0 &&
                    settings.suppressMissedWarning !== true
                ) {
                    this.#log()?.warn(
                        `runwisp: task "${this.name}" missed its run at ${localIso(at, settings.timezone)}: RunWisp was not running`,
                    );
                }
                this.#emit("execution:missed", at);
            },
        });
    }

    start(): void {
        if (this.#status !== "stopped") return;
        this.#status = "idle";
        this.#control.setPaused(this.id, false);
        this.#emit("task:started");
    }

    stop(): void {
        if (!this.#halt()) return;
        this.#control.setPaused(this.id, true);
    }

    /** Removes the task from RunWisp's config. Its past runs stay in RunWisp until retention removes them. */
    destroy(): void {
        if (this.#status === "destroyed") return;
        this.stop();
        this.#status = "destroyed";
        this.#control.remove(this.id);
        this.#emit("task:destroyed");
    }

    getStatus(): string {
        return this.#busy > 0 && this.#status === "idle" ? "running" : this.#status;
    }

    /** Runs the callback now, in this process. Not recorded in RunWisp; use the UI's Run button for a recorded run. */
    execute(): Promise<unknown> {
        return this.#run(undefined, {
            signal: new AbortController().signal,
            env: {},
            params: {},
        });
    }

    /** The next firing. null only while stopped (as node-cron), and for a task without a schedule. */
    getNextRun(): Date | null {
        if (this.#status === "stopped" || this.#settings.pattern === "") return null;
        return this.#control.nextRun(this, this.#settings.pattern, this.#settings.timezone);
    }

    /** The next count firings from now, whatever the status. */
    getNextRuns(count: number): Date[] {
        const { pattern, timezone } = this.#settings;
        return pattern === "" ? [] : this.#control.nextRuns(pattern, timezone, count);
    }

    /** Whether the schedule fires in date's second. */
    match(date: Date): boolean {
        const { pattern, timezone } = this.#settings;
        return pattern !== "" && this.#control.match(pattern, timezone, date);
    }

    /** Milliseconds to the next firing, or null when there is none. Not clamped: node-cron's isn't either. */
    msToNext(): number | null {
        const next = this.getNextRun();
        return next ? next.getTime() - Date.now() : null;
    }

    isBusy(): boolean {
        return this.getStatus() === "running";
    }

    runsLeft(): number | undefined {
        const max = this.#settings.maxExecutions;
        return max === undefined ? undefined : Math.max(0, max - this.#runs);
    }

    getPattern(): string {
        return this.#settings.pattern;
    }

    lastRun(): LastRun | null {
        return this.#last;
    }

    /** No-op: RunWisp holds the schedule, not a timer in this process. */
    ref(): void {
        // Nothing keeps the process alive per task.
    }

    /** No-op: RunWisp holds the schedule, not a timer in this process. */
    unref(): void {
        // Nothing keeps the process alive per task.
    }

    on(event: TaskEvent, listener: Listener): void {
        this.#listen(event, listener, false);
    }

    off(event: TaskEvent, listener: Listener): void {
        // As EventEmitter: the most recently added registration goes.
        const list = this.#listeners.get(event) ?? [];
        const index = list.map((entry) => entry.listener).lastIndexOf(listener);
        if (index >= 0) list.splice(index, 1);
    }

    once(event: TaskEvent, listener: Listener): void {
        this.#listen(event, listener, true);
    }

    #listen(event: TaskEvent, listener: Listener, once: boolean): void {
        const list = this.#listeners.get(event) ?? [];
        list.push({ listener, once });
        this.#listeners.set(event, list);
    }

    /** idle to stopped, emitting task:stopped; false when it wasn't idle. */
    #halt(): boolean {
        if (this.#status !== "idle") return false;
        this.#status = "stopped";
        this.#emit("task:stopped");
        return true;
    }

    #log(): Logger | undefined {
        return this.#settings.logger ?? this.#control.log();
    }

    async #run(at: Date | undefined, run: RunContext): Promise<unknown> {
        // node-cron emits it for a firing that comes while the previous one runs.
        if (at && this.#busy > 0) this.#emit("execution:overlap", at);
        const startedAt = new Date();
        const execution: Execution = {
            id: randomUUID(),
            reason: at ? "scheduled" : "invoked",
            startedAt,
        };
        const context = this.#context(at ?? startedAt, execution, run);
        if (at) {
            this.#busy++;
            this.#runs++;
        }
        this.#dispatch("execution:started", context);
        try {
            const result = await this.#settings.fn(context);
            execution.finishedAt = new Date();
            execution.result = result;
            this.#last = { date: execution.finishedAt, result };
            this.#dispatch("execution:finished", context);
            const max = this.#settings.maxExecutions;
            // As node-cron: checked after a run that succeeded, against every scheduled run so far.
            if (at && max !== undefined && max > 0 && this.#runs >= max) {
                this.#emit("execution:maxReached", at);
                this.destroy();
            }
            return result;
        } catch (err) {
            const error = toError(err);
            execution.finishedAt = new Date();
            execution.error = error;
            this.#last = { date: execution.finishedAt, error };
            this.#dispatch("execution:failed", { ...context, error });
            throw err;
        } finally {
            if (at) this.#busy--;
        }
    }

    #context(date: Date, execution: Execution, run: RunContext): TaskContext {
        return {
            ...run,
            date,
            dateLocalIso: localIso(date, this.#settings.timezone),
            triggeredAt: new Date(),
            task: this,
            execution,
        };
    }

    /** An event about the task rather than a run. date: when it happened, or the firing it is about. */
    #emit(event: TaskEvent, date = new Date()): void {
        this.#dispatch(
            event,
            this.#context(
                date,
                { id: randomUUID(), reason: "invoked" },
                { signal: new AbortController().signal, env: {}, params: {} },
            ),
        );
    }

    /** Calls each listener; one that throws or rejects is logged, and never fails the run or the caller. */
    #dispatch(event: TaskEvent, context: TaskContext): void {
        const failed = (err: unknown) => {
            const error = toError(err);
            this.#log()?.error(
                `runwisp: a ${event} listener of task "${this.name}" failed: ${error.stack ?? error.message}`,
            );
        };
        const list = this.#listeners.get(event) ?? [];
        this.#listeners.set(
            event,
            list.filter((entry) => !entry.once),
        );
        for (const { listener } of list) {
            try {
                Promise.resolve(listener(context)).catch(failed);
            } catch (err) {
                failed(err);
            }
        }
    }
}

/** A task handle, and the internals only its owner gets. */
export function createScheduledTask(
    control: TaskControl,
    settings: TaskSettings,
): { task: ScheduledTask; internals: TaskInternals } {
    const bound: { internals?: TaskInternals } = {};
    const task = new ScheduledTask(control, settings, (internals) => {
        bound.internals = internals;
    });
    if (!bound.internals) throw new Error("unreachable: ScheduledTask binds its internals");
    return { task, internals: bound.internals };
}
