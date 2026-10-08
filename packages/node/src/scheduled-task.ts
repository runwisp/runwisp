// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A task handle with node-cron 4's ScheduledTask surface, so code written for
// node-cron keeps working. Scheduling itself happens in RunWisp; this class
// only runs the callback when RunWisp fires it and tracks node-cron's state.

import { randomUUID } from "node:crypto";
import { EventEmitter } from "node:events";

/** node-cron's TaskOptions. Keys RunWisp has no use for are accepted and ignored. */
export interface NodeCronOptions {
    timezone?: string;
    name?: string;
    noOverlap?: boolean;
    maxExecutions?: number;
    /** Milliseconds; becomes the task's RunWisp `timeout`. */
    executeTimeout?: number;
    distributed?: boolean;
    runCoordinator?: unknown;
    distributedLease?: number;
    maxRandomDelay?: number;
    logger?: unknown;
    suppressMissedWarning?: boolean;
    missedExecutionTolerance?: number;
    startTimeout?: number;
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
    date: Date;
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

export type TaskEvent =
    | "task:started"
    | "task:stopped"
    | "task:destroyed"
    | "execution:started"
    | "execution:finished"
    | "execution:failed"
    | "execution:maxReached";

type Listener = (context: TaskContext) => unknown;

/** What a task needs from the RunWisp instance that owns it. */
export interface TaskControl {
    setPaused(name: string, paused: boolean): void;
    remove(name: string): void;
    nextRun(name: string): Date | null;
}

type Status = "stopped" | "idle" | "destroyed";

export class ScheduledTask {
    /** The RunWisp task name. */
    readonly id: string;
    readonly name: string;
    private readonly control: TaskControl;
    private readonly pattern: string;
    private readonly fn: TaskFn;
    private readonly maxExecutions: number | undefined;
    private status: Status = "stopped";
    private busy = 0;
    private runs = 0;
    private last: LastRun | null = null;
    private readonly events = new EventEmitter();

    constructor(
        control: TaskControl,
        id: string,
        name: string,
        pattern: string,
        fn: TaskFn,
        maxExecutions: number | undefined,
    ) {
        this.control = control;
        this.id = id;
        this.name = name;
        this.pattern = pattern;
        this.fn = fn;
        this.maxExecutions = maxExecutions;
    }

    start(): void {
        if (this.status === "destroyed") return;
        this.status = "idle";
        this.control.setPaused(this.id, false);
        this.emit("task:started");
    }

    stop(): void {
        if (this.status === "destroyed") return;
        this.status = "stopped";
        this.control.setPaused(this.id, true);
        this.emit("task:stopped");
    }

    /** Removes the task from RunWisp's config. Its past runs stay in RunWisp until retention removes them. */
    destroy(): void {
        if (this.status === "destroyed") return;
        this.status = "destroyed";
        this.control.remove(this.id);
        this.emit("task:destroyed");
    }

    getStatus(): string {
        return this.busy > 0 && this.status !== "destroyed" ? "running" : this.status;
    }

    /** Runs the callback now, in this process. Not recorded in RunWisp; use the UI's Run button for a recorded run. */
    execute(): Promise<unknown> {
        return this.run("invoked", { signal: new AbortController().signal, env: {}, params: {} });
    }

    /** Next fire time as RunWisp last reported it; null while stopped or for a task without a schedule. */
    getNextRun(): Date | null {
        return this.status === "idle" ? this.control.nextRun(this.id) : null;
    }

    msToNext(): number | null {
        const next = this.getNextRun();
        return next ? Math.max(0, next.getTime() - Date.now()) : null;
    }

    isBusy(): boolean {
        return this.busy > 0;
    }

    runsLeft(): number | undefined {
        return this.maxExecutions === undefined
            ? undefined
            : Math.max(0, this.maxExecutions - this.runs);
    }

    getPattern(): string {
        return this.pattern;
    }

    lastRun(): LastRun | null {
        return this.last;
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
        this.events.on(event, listener);
    }

    off(event: TaskEvent, listener: Listener): void {
        this.events.off(event, listener);
    }

    once(event: TaskEvent, listener: Listener): void {
        this.events.once(event, listener);
    }

    /** @internal Runs the callback for one execution and rethrows its error, so the run's exit code reflects it. */
    async run(reason: Execution["reason"], run: RunContext): Promise<unknown> {
        const startedAt = new Date();
        const execution: Execution = { id: randomUUID(), reason, startedAt };
        const context: TaskContext = {
            ...run,
            date: startedAt,
            dateLocalIso: startedAt.toISOString(),
            triggeredAt: startedAt,
            task: this,
            execution,
        };
        this.busy++;
        this.events.emit("execution:started", context);
        try {
            const result = await this.fn(context);
            execution.result = result;
            this.last = { date: startedAt, result };
            this.events.emit("execution:finished", context);
            return result;
        } catch (err) {
            const error = err instanceof Error ? err : new Error(String(err));
            execution.error = error;
            this.last = { date: startedAt, error };
            this.events.emit("execution:failed", { ...context, error });
            throw err;
        } finally {
            execution.finishedAt = new Date();
            this.busy--;
            this.countRun(context);
        }
    }

    private countRun(context: TaskContext): void {
        this.runs++;
        if (this.maxExecutions !== undefined && this.runs >= this.maxExecutions) {
            this.events.emit("execution:maxReached", context);
            this.destroy();
        }
    }

    private emit(event: TaskEvent): void {
        const now = new Date();
        this.events.emit(event, {
            date: now,
            dateLocalIso: now.toISOString(),
            triggeredAt: now,
            task: this,
            execution: { id: randomUUID(), reason: "invoked" },
            signal: new AbortController().signal,
            env: {},
            params: {},
        } satisfies TaskContext);
    }
}
