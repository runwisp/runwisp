// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createHash } from "node:crypto";
import { constants } from "node:os";
import { isAbsolute, join, relative } from "node:path";
import { setImmediate as nextTurn } from "node:timers/promises";

import { forkedTask, solvePath } from "./background.ts";
import { ownStack, runCaptured } from "./capture.ts";
import { CronAnswers, type DetailedValidation, type ParsedFields } from "./cron.ts";
import {
    choosePort,
    daemonPaths,
    dashboardUrl,
    DEFAULT_PORT,
    healthy,
    identity,
    launchFingerprint,
    nextRuns,
    prepareDirs,
    recordLaunch,
    resolveSettings,
    savedPort,
    staleReason,
    startDaemon,
    stopDaemon,
    validateConfig,
    type DaemonPaths,
    type Settings,
} from "./daemon.ts";
import type { Config, ServiceConfig, TaskConfig } from "./generated/config.ts";
import { Runner, type OverlapPolicy, type Serve } from "./runner.ts";
import {
    createScheduledTask,
    type Logger,
    type NodeCronOptions,
    type RunContext,
    type RunCoordinator,
    type ScheduledTask,
    type TaskControl,
    type TaskFn,
    type TaskInternals,
} from "./scheduled-task.ts";
import { Session, type SessionEvents } from "./session.ts";

export type { Logger } from "./scheduled-task.ts";

export interface RunWispOptions {
    /** Names this app to RunWisp. Copies of one app share a data directory and its RunWisp; a different app is refused. Default: the `name` in the nearest package.json from the working directory, else the working directory. */
    app?: string;
    /** Data directory for RunWisp's database, logs and socket. Default: `RUNWISP_DATA`, else `./.runwisp` (`/var/lib/runwisp` as root). */
    data?: string;
    /** Web UI port. Default: `RUNWISP_PORT`, else 9477. */
    port?: number;
    /** Web UI bind address. Default: `RUNWISP_HOST`, else 127.0.0.1. */
    host?: string;
    /** Web UI password. Default: `RUNWISP_PASSWORD`, else a new password on each start; the dashboard notice says how to print it. */
    password?: string;
    /** false turns the web UI login off (`RUNWISP_AUTH=off`). Default: true. */
    auth?: boolean;
    /** Path to the runwisp binary. Default: the one from the `runwisp` npm package, else `runwisp` on PATH. */
    binary?: string;
    /** Where the dashboard URL and warnings go. Default: console. false silences them. */
    log?: Logger | false;
    /** false (or `RUNWISP_NODE=off`) starts nothing: for unit tests. Tasks register and `execute()` still works. */
    enabled?: boolean;
    /** true closes RunWisp on SIGTERM and SIGINT, letting running tasks finish, then exits the process with 128 + the signal number (143, 130): for servers with no shutdown hook. Default: false. */
    exitOnSignal?: boolean;
    /**
     * Everything else a runwisp.toml holds, with the same keys: `defaults`,
     * `notifiers`, `route`, `daemon`, `storage`, and tasks or services that run
     * a shell command. Relative paths resolve against the current directory.
     */
    config?: Config;
}

/** A `[tasks.<name>]` table, minus `sdk`: the callback runs the task. */
export type TaskOptions = Omit<TaskConfig, "sdk">;
/** A `[services.<name>]` table, minus `sdk`: the callback runs the service. */
export type ServiceOptions = Omit<ServiceConfig, "sdk">;
/** A service's callback: runs until `ctx.signal` aborts. RunWisp restarts it when it returns or throws. */
export type ServiceFn = (context: RunContext) => unknown;

interface TaskJob {
    options: TaskOptions;
    /** A stopped node-cron task: in RunWisp without its cron. */
    paused: boolean;
    task: ScheduledTask;
    internals: TaskInternals;
}

interface ServiceJob {
    options: ServiceOptions;
    fn: ServiceFn;
}

// One instance per data directory per process: `new RunWisp()` in every file
// that schedules tasks gets the same one, like node-cron's module. On
// globalThis, so a second copy of this package (a bundler that duplicated it,
// ESM next to CJS) gets it too instead of connecting as a standby copy.
const INSTANCES = Symbol.for("runwisp.instances.v1");

function isRegistry(value: unknown): value is Map<string, RunWisp> {
    return value instanceof Map;
}

function instances(): Map<string, RunWisp> {
    const found: unknown = Reflect.get(globalThis, INSTANCES);
    if (isRegistry(found)) return found;
    const created = new Map<string, RunWisp>();
    Object.defineProperty(globalThis, INSTANCES, { value: created });
    return created;
}

/** Settings two `new RunWisp()` for one data directory must agree on. */
const SHARED_SETTINGS = ["app", "port", "host", "auth", "password", "binary"] as const;

const UNIT_NAME = /^[a-zA-Z0-9._:-]{1,100}$/;

// node-cron lets runs overlap unless noOverlap is set; RunWisp's cap on that.
const NODE_CRON_MAX_CONCURRENT = 1024;

// How soon a copy reconnects after RunWisp went away, doubling per failed try up to the cap.
const RECONNECT_MS = 1000;
const MAX_RECONNECT_MS = 30_000;

const EXIT_SIGNALS = ["SIGTERM", "SIGINT"] as const;

// What adapter-node (SvelteKit) emits once its server has stopped on SIGTERM.
const SVELTEKIT_SHUTDOWN = "sveltekit:shutdown";

const RUNS_ONCE = "each task runs once per data directory, however many copies of the app run";

const IGNORED_NODE_CRON_OPTIONS: Partial<Record<keyof NodeCronOptions, string>> = {
    distributed: RUNS_ONCE,
    runCoordinator: RUNS_ONCE,
    distributedLease: RUNS_ONCE,
    maxRandomDelay: "use task() with the jitter option instead",
};

function shellQuote(value: string): string {
    if (/^[\w./:=@%+-]+$/.test(value)) return value;
    return `'${value.replaceAll("'", `'\\''`)}'`;
}

function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
}

/**
 * A RunWisp instance for this app: runs RunWisp with this app's tasks as its
 * config, and runs their callbacks in this process when RunWisp fires them.
 * RunWisp records every run (output, exit code, duration) and shows it in its
 * web UI and TUI. Its methods match node-cron's, so `const cron = new
 * RunWisp()` replaces `import cron from "node-cron"`.
 *
 * Copies of the app that share a data directory share one daemon. The first
 * to connect is active: it supplies the config and runs every callback. The
 * others stand by and take over when it leaves. The daemon stops once the
 * last copy is gone.
 */
export class RunWisp {
    private readonly settings: Settings;
    private readonly paths: DaemonPaths;
    private readonly enabled: boolean;
    /** The log the options asked for, which setLogger(undefined) goes back to. */
    private readonly defaultLog: Logger | undefined;
    private log: Logger | undefined;
    private readonly config: Config;
    private readonly runner: Runner;
    private readonly cron: CronAnswers;
    private readonly tasks = new Map<string, TaskJob>();
    private readonly services = new Map<string, ServiceJob>();
    private readonly next = new Map<string, Date>();
    private readonly warned = new Set<string>();
    /**
     * Ids registered in this turn, with the name each came from (undefined:
     * derived from its code). Two registrations of one id in the same turn
     * are a loop or two files colliding, not a dev server re-running a file.
     */
    private readonly batch = new Map<string, string | undefined>();
    private session: Session | undefined;
    /** Why the daemon attached to is not what this copy would start; restarted once this copy is alone. */
    private stale: string | undefined;
    private standby = false;
    private retries = 0;
    private retryTimer: NodeJS.Timeout | undefined;
    /** The config the daemon has from this copy. */
    private pushed: string | undefined;
    private booted: Promise<void> | undefined;
    private pending: Promise<void> | undefined;
    private queue: Promise<void> = Promise.resolve();
    private announced = false;
    private closed = false;
    private exitsOnSignal = false;

    private readonly control: TaskControl = {
        setPaused: (name, paused) => {
            const job = this.tasks.get(name);
            if (!job) return;
            job.paused = paused;
            this.changed();
        },
        remove: (name) => {
            this.tasks.delete(name);
            this.changed();
        },
        nextRun: (task, pattern, timezone) => {
            // What RunWisp reported, while it is still this handle's and still ahead.
            const reported = this.next.get(task.id);
            if (reported && reported > new Date() && this.tasks.get(task.id)?.task === task)
                return reported;
            return this.cron.next(pattern, timezone, 1)[0] ?? null;
        },
        nextRuns: (pattern, timezone, count) => this.cron.next(pattern, timezone, count),
        match: (pattern, timezone, date) => this.cron.match(pattern, timezone, date),
        log: () => this.log,
    };

    constructor(options: RunWispOptions = {}) {
        const settings = resolveSettings(options);
        this.settings = settings;
        this.paths = daemonPaths(settings.data);
        this.enabled = options.enabled ?? process.env["RUNWISP_NODE"] !== "off";
        this.defaultLog = options.log === false ? undefined : (options.log ?? console);
        this.log = this.defaultLog;
        this.config = options.config ?? {};
        this.runner = new Runner(this.serve, this.policy);
        this.cron = new CronAnswers(settings.binary);
        if (options.config) this.check(options.config);
        if (!this.enabled) return;
        const existing = instances().get(settings.data);
        if (existing) {
            existing.assertSameSettings(settings, options.config);
            if (options.exitOnSignal === true) existing.exitOnSignal();
            return existing;
        }
        instances().set(settings.data, this);
        process.once(SVELTEKIT_SHUTDOWN, this.closeOnShutdown);
        if (options.exitOnSignal === true) this.exitOnSignal();
    }

    /** The web UI address. */
    get url(): string {
        const port = this.settings.port ?? savedPort(this.paths) ?? DEFAULT_PORT;
        return dashboardUrl(this.settings.host, port);
    }

    /**
     * node-cron's `schedule`: registers the task in RunWisp and starts it. fn
     * is a callback, or the path of a module exporting `task` (relative to the
     * calling file), which runs in a new Node process per run.
     */
    schedule(
        expression: string,
        fn: TaskFn | string,
        options: NodeCronOptions = {},
    ): ScheduledTask {
        const task = this.createTask(expression, fn, options);
        task.start();
        return task;
    }

    /** node-cron's `createTask`: registers the task in RunWisp, stopped (no schedule) until `start()`. */
    createTask(
        expression: string,
        fn: TaskFn | string,
        options: NodeCronOptions = {},
    ): ScheduledTask {
        if (typeof fn !== "function" && typeof fn !== "string") {
            throw new TypeError(
                "a task is a function, or the path of a module that exports `task`",
            );
        }
        // A path: its URL names the task, as the code of a callback does.
        const source = typeof fn === "string" ? solvePath(fn) : fn.toString();
        const run = typeof fn === "string" ? forkedTask(source) : fn;
        this.warnIgnored(options);
        const derived = options.name === undefined;
        const name = options.name ?? this.derivedName(expression, source);
        const id = name
            .trim()
            .replaceAll(/[^a-zA-Z0-9._:-]+/g, "-")
            .slice(0, 100);
        // node-cron never catches up on runs missed while the app was down.
        const taskOptions: TaskOptions = { cron: expression, catch_up: 0 };
        if (options.timezone !== undefined) taskOptions.timezone = options.timezone;
        if (options.noOverlap === true) taskOptions.on_overlap = "skip";
        else taskOptions.max_concurrent = NODE_CRON_MAX_CONCURRENT;
        if (options.executeTimeout !== undefined)
            taskOptions.timeout = `${String(options.executeTimeout)}ms`;
        return this.addTask(id, derived ? undefined : name, run, taskOptions, options);
    }

    /**
     * Registers a task with any `[tasks.<name>]` key (retries, jitter,
     * catch-up, timeout, notifications, ...) and starts it. Without `cron`,
     * the task only runs from the web UI, TUI or CLI.
     */
    task(name: string, options: TaskOptions, fn: TaskFn): ScheduledTask {
        const task = this.addTask(name, name, fn, options, {});
        task.start();
        return task;
    }

    /**
     * Registers a service with any `[services.<name>]` key: RunWisp keeps fn
     * running, restarting it when it returns or throws, and asks it to stop
     * through `ctx.signal`.
     */
    service(name: string, options: ServiceOptions, fn: ServiceFn): void {
        this.assertName(name, this.tasks);
        this.check(this.unitConfig({ services: { [name]: { ...options, sdk: true } } }));
        this.services.set(name, { options, fn });
        this.changed();
    }

    /** node-cron's `validate`, answered by `runwisp sdk-cron` so the grammar is exactly RunWisp's. */
    validate(expression: string): boolean {
        return this.cron.explain(expression).valid;
    }

    /** node-cron's `validateDetailed`: per-field errors, or the values each field matches. */
    validateDetailed(expression: string): DetailedValidation {
        return this.cron.explain(expression);
    }

    /** node-cron's `parse`: the values each field matches. Throws for an invalid expression, and for `@every`, which has no fields. */
    parse(expression: string): ParsedFields {
        const { fields, errors } = this.cron.explain(expression);
        if (fields) return fields;
        throw new Error(
            errors[0]?.message ?? `"${expression}" is an interval, which has no fields to parse`,
        );
    }

    /** node-cron's `setLogger`: where warnings and errors go from now on. undefined: back to the `log` option. */
    setLogger(logger?: Logger | false): void {
        this.log = logger === false ? undefined : (logger ?? this.defaultLog);
    }

    /** node-cron's `setRunCoordinator`: accepted, never called. RunWisp runs each task once per data directory. */
    setRunCoordinator(_coordinator?: RunCoordinator): void {
        this.warnOnce(
            "runCoordinator",
            `runwisp: node-cron option "runCoordinator" is ignored: ${RUNS_ONCE}`,
        );
    }

    getTasks(): Map<string, ScheduledTask> {
        return new Map([...this.tasks].map(([name, job]) => [name, job.task]));
    }

    getTask(id: string): ScheduledTask | undefined {
        return this.tasks.get(id)?.task;
    }

    /** Starts (or connects to) RunWisp now instead of on the first task. Rejects with RunWisp's error if it can't. */
    start(): Promise<void> {
        return this.sync();
    }

    /** Closes every RunWisp in this process: for a server's shutdown hook. */
    static async closeAll(): Promise<void> {
        await Promise.all([...instances().values()].map((instance) => instance.close()));
    }

    /**
     * node-cron's `shutdown`: close(), waiting at most timeout ms for running
     * tasks to finish. Every task is destroyed: getTasks() is empty after.
     */
    async shutdown(timeout = 5000): Promise<void> {
        let timer: NodeJS.Timeout | undefined;
        const waited = new Promise<void>((resolve) => {
            timer = setTimeout(resolve, timeout);
            timer.unref();
        });
        try {
            await Promise.race([this.close(), waited]);
        } finally {
            clearTimeout(timer);
        }
    }

    /**
     * Stops serving tasks, after the running ones finish, and stops the
     * services. Stops RunWisp too, unless another copy of the app still uses it.
     */
    async close(): Promise<void> {
        this.closed = true;
        clearTimeout(this.retryTimer);
        if (instances().get(this.settings.data) === this) instances().delete(this.settings.data);
        process.off(SVELTEKIT_SHUTDOWN, this.closeOnShutdown);
        // A second signal while closing gets Node's default: exit now.
        for (const [signal, exit] of this.signalExits) process.off(signal, exit);
        await this.queue;
        this.session?.leave();
        // As node-cron's shutdown: every task is destroyed. After leave, so no
        // run RunWisp sent meanwhile finds its task gone.
        for (const job of this.tasks.values()) job.internals.retire();
        this.tasks.clear();
        // The next copy, if any, runs them now; the last copy hears nothing
        // back from RunWisp, so this can't wait to be told.
        this.runner.stopTasks(this.services.keys());
        await this.runner.close();
        process.off("uncaughtExceptionMonitor", this.runner.reportCrash);
        const session = this.session;
        this.session = undefined;
        await session?.close();
    }

    private exitOnSignal(): void {
        if (this.exitsOnSignal) return;
        this.exitsOnSignal = true;
        for (const [signal, exit] of this.signalExits) process.once(signal, exit);
    }

    /** Per signal: close, then exit like a process the signal killed (128 + its number). */
    private readonly signalExits = new Map(
        EXIT_SIGNALS.map((signal) => [
            signal,
            (): void => {
                void this.close().finally(() => process.exit(128 + constants.signals[signal]));
            },
        ]),
    );

    private readonly closeOnShutdown = (): void => {
        this.close().catch((err: unknown) => {
            this.log?.error(`runwisp: ${message(err)}`);
        });
    };

    private assertSameSettings(settings: Settings, config: Config | undefined): void {
        const differs = SHARED_SETTINGS.find((key) => settings[key] !== this.settings[key]);
        if (differs) {
            throw new Error(
                `a RunWisp for ${settings.data} already exists in this process with a different ${differs}; pass the same options or use another data directory`,
            );
        }
        if (config) {
            throw new Error(
                `a RunWisp for ${settings.data} already exists in this process; pass config to the first one only`,
            );
        }
    }

    /**
     * A name taken by a unit of the other kind, or by one in config, is
     * rejected. One of the same kind is replaced: a dev server that runs the
     * file registering it again (Vite after an edit) gets the new code.
     */
    private assertName(name: string, otherKind: ReadonlyMap<string, unknown>): void {
        if (!UNIT_NAME.test(name)) {
            throw new Error(`invalid task name "${name}": use 1-100 of a-z A-Z 0-9 . _ : -`);
        }
        if (
            otherKind.has(name) ||
            name in (this.config.tasks ?? {}) ||
            name in (this.config.services ?? {})
        ) {
            throw new Error(`a task named "${name}" is already scheduled`);
        }
    }

    /** name: what the caller passed, undefined when the name was derived from the code. */
    private addTask(
        id: string,
        name: string | undefined,
        fn: TaskFn,
        options: TaskOptions,
        nodeCron: Pick<NodeCronOptions, "maxExecutions" | "logger" | "suppressMissedWarning">,
    ): ScheduledTask {
        this.assertName(id, this.services);
        this.check(this.unitConfig({ tasks: { [id]: { ...options, sdk: true } } }));
        this.claim(id, name);
        const previous = this.tasks.get(id);
        if (previous && name === undefined) {
            this.warnOnce(
                `replaced:${id}`,
                `runwisp: unnamed task "${id}" replaced one registered earlier with the same cron and code. A dev server re-running the file does this; when scheduling in a loop, pass { name } to each task.`,
            );
        }
        previous?.internals.retire();
        this.next.delete(id);
        const { task, internals } = createScheduledTask(this.control, {
            id,
            name: name ?? id,
            pattern: options.cron ?? "",
            timezone: options.timezone ?? this.config.daemon?.timezone,
            fn,
            maxExecutions: nodeCron.maxExecutions,
            logger: nodeCron.logger,
            suppressMissedWarning: nodeCron.suppressMissedWarning,
        });
        this.tasks.set(id, { options, paused: true, task, internals });
        this.changed();
        return task;
    }

    /**
     * Throws when id was already registered in this turn, unless both times
     * under the same given name: a loop scheduling unnamed tasks with the same
     * cron and code, or two names RunWisp turns into the same id, would
     * otherwise replace each other. A later turn replaces, as a dev server
     * re-running the file needs.
     */
    private claim(id: string, name: string | undefined): void {
        if (this.batch.has(id)) {
            const before = this.batch.get(id);
            if (name === undefined || before === undefined) {
                throw new Error(
                    `two tasks with the same cron and code would both be "${id}": pass { name } to each`,
                );
            }
            if (before !== name) {
                throw new Error(
                    `task names "${before}" and "${name}" are both "${id}" in RunWisp, which turns characters other than a-z A-Z 0-9 . _ : - into "-": pick names that differ`,
                );
            }
        }
        if (this.batch.size === 0) {
            setImmediate(() => {
                this.batch.clear();
            }).unref();
        }
        this.batch.set(id, name);
    }

    /**
     * The app's config with only the given unit, to check one unit on its own:
     * a [[route]] may name a unit that isn't defined yet.
     */
    private unitConfig(units: Pick<Config, "tasks" | "services">): Config {
        const { tasks: _tasks, services: _services, route: _route, ...rest } = this.config;
        return { ...rest, ...units };
    }

    /**
     * Rejects a config RunWisp would reject now, not at the next push, where
     * one bad task would keep every change from applying.
     */
    private check(config: Config): void {
        let problem: string | undefined;
        try {
            problem = validateConfig(this.settings.binary, this.paths, config);
        } catch (err) {
            // Unit tests (enabled: false) may run without the binary installed.
            if (this.enabled) throw err;
            return;
        }
        if (problem !== undefined) throw new Error(problem);
    }

    /** source: the callback's code, or a background task's URL. */
    private derivedName(expression: string, source: string): string {
        const hash = createHash("sha256")
            .update(`${expression}\n${source}`)
            .digest("hex")
            .slice(0, 8);
        const name = `task-${hash}`;
        this.warnOnce(
            "unnamed",
            `runwisp: task "${name}" has no name, so one was derived from its code. Pass { name } to keep its history when the code changes.`,
        );
        return name;
    }

    private warnIgnored(options: NodeCronOptions): void {
        for (const [key, why] of Object.entries(IGNORED_NODE_CRON_OPTIONS)) {
            if (key in options)
                this.warnOnce(key, `runwisp: node-cron option "${key}" is ignored: ${why}`);
        }
    }

    private warnOnce(key: string, text: string): void {
        if (this.warned.has(key)) return;
        this.warned.add(key);
        this.log?.warn(text);
    }

    /** The whole config this app gives RunWisp, as JSON. */
    private document(): string {
        const tasks: Record<string, TaskConfig> = { ...this.config.tasks };
        for (const [name, job] of this.tasks) {
            const { cron: _cron, ...unscheduled } = job.options;
            tasks[name] = { ...(job.paused ? unscheduled : job.options), sdk: true };
        }
        const services: Record<string, ServiceConfig> = { ...this.config.services };
        for (const [name, job] of this.services) services[name] = { ...job.options, sdk: true };
        return JSON.stringify({ ...this.config, tasks, services } satisfies Config);
    }

    private changed(): void {
        this.sync().catch(() => {
            // Reported once by the queue below.
        });
    }

    /** Batches changes made in the same turn into one config push. */
    private sync(): Promise<void> {
        if (!this.enabled || this.closed) return Promise.resolve();
        if (!this.pending) {
            const pending = this.queue.then(async () => {
                await nextTurn();
                this.pending = undefined;
                await this.boot();
                await this.apply();
            });
            this.pending = pending;
            this.queue = pending.catch((err: unknown) => {
                // A repeat failure gets its first line only: the first one had the details.
                const text = this.retries === 0 ? message(err) : message(err).split("\n")[0];
                const delay = this.retryLater();
                const retry = delay === undefined ? "" : ` (retrying in ${String(delay / 1000)}s)`;
                this.log?.error(`runwisp: ${String(text)}${retry}`);
            });
        }
        return this.pending;
    }

    /**
     * Connects again later while this copy isn't connected and isn't closed,
     * backing off up to MAX_RECONNECT_MS: a daemon slow to start or busy
     * shutting down is retried until it answers. Returns the delay.
     */
    private retryLater(): number | undefined {
        if (this.closed || this.session) return undefined;
        const delay = Math.min(RECONNECT_MS * 2 ** this.retries, MAX_RECONNECT_MS);
        if (this.retryTimer) return delay;
        this.retries++;
        this.retryTimer = setTimeout(() => {
            this.retryTimer = undefined;
            this.changed();
        }, delay);
        return delay;
    }

    private boot(): Promise<void> {
        this.booted ??= this.attach().catch((err: unknown) => {
            this.booted = undefined;
            throw err;
        });
        return this.booted;
    }

    /** Starts RunWisp with this app's config unless another copy already did, and connects to it. */
    private async attach(): Promise<void> {
        await prepareDirs(this.settings.data, this.paths);
        let started: number | undefined;
        if (!(await healthy(this.paths.socket))) {
            const port = await choosePort(this.settings, this.paths);
            started = await startDaemon(this.settings, this.paths, port, this.document());
        }
        const daemon = await identity(this.paths.socket);
        if (daemon.configSource !== "app") {
            throw new Error(
                `the RunWisp daemon on ${this.settings.data} runs ${daemon.configPath}, not this app's tasks; pass a different data directory`,
            );
        }
        if (started === daemon.pid) {
            recordLaunch(this.paths, daemon.pid, launchFingerprint(this.settings));
            this.stale = undefined;
        } else {
            this.stale = staleReason(this.settings, this.paths, daemon);
        }
        const events: SessionEvents = {
            role: (session, active) => {
                if (!active) {
                    if (!this.closed && !this.standby) {
                        this.standby = true;
                        this.log?.info(
                            `runwisp: standby: another copy of this app is active (pid ${String(session.activePid)}); this one runs the tasks once that one stops`,
                        );
                    }
                    return;
                }
                if (this.standby) {
                    this.standby = false;
                    this.log?.info(
                        "runwisp: this copy of the app is now active and runs the tasks",
                    );
                }
                // A copy that becomes active supplies its own config.
                this.pushed = undefined;
                if (this.session === session) this.changed();
            },
            alone: (session) => {
                if (this.session === session && this.stale !== undefined) this.changed();
            },
            run: (session, request) => {
                this.runner.start(session, request);
            },
            stop: (run) => {
                this.runner.stop(run);
            },
            overlap: (task, at) => {
                this.tasks.get(task)?.internals.overlap(at);
            },
            missed: (task, at) => {
                this.tasks.get(task)?.internals.missed(at);
            },
            closed: (session) => {
                this.runner.disconnected(session);
                if (this.session !== session) return;
                this.session = undefined;
                this.booted = undefined;
                this.retries = 0;
                this.retryLater();
            },
        };
        this.session = await Session.open(this.paths.socket, events, {
            app: this.settings.app,
            pid: process.pid,
        });
        this.retries = 0;
        if (this.stale !== undefined && !this.session.alone) {
            this.log?.warn(
                `runwisp: RunWisp is stale (${this.stale}); it restarts once this is the only copy of the app connected`,
            );
        }
        process.off("uncaughtExceptionMonitor", this.runner.reportCrash);
        process.on("uncaughtExceptionMonitor", this.runner.reportCrash);
        this.announce();
    }

    /** Never prints the password itself: app output ends up in log collectors. */
    private announce(): void {
        if (this.announced) return;
        this.announced = true;
        let suffix = "";
        if (!this.settings.auth) suffix = " (login off)";
        else if (this.settings.password === undefined)
            suffix = `, password: ${this.passwordCommand()}`;
        this.log?.info(`RunWisp dashboard: ${this.url}${suffix}`);
    }

    /** The CLI command that prints RunWisp's per-start password. */
    private passwordCommand(): string {
        const { data } = this.settings;
        const npx = process.versions.bun ? "bunx" : "npx";
        if (this.paths.socket !== join(data, "runwisp.sock"))
            return `${npx} runwisp password --socket ${shellQuote(this.paths.socket)}`;
        const rel = relative(process.cwd(), data);
        const shown = rel === "" || rel.startsWith("..") || isAbsolute(rel) ? data : rel;
        return `${npx} runwisp password --data ${shellQuote(shown)}`;
    }

    /** The active copy pushes its config when it changed; a standby copy only follows. */
    private async apply(): Promise<void> {
        const session = this.session;
        // Alone: no other copy's runs to stop. The active copy is the one to restart it.
        if (session?.active === true && session.alone && this.stale !== undefined) {
            const why = this.stale;
            this.stale = undefined;
            await this.restart(`since ${why}`);
            return;
        }
        if (session?.active === true) {
            const doc = this.document();
            if (doc !== this.pushed) {
                const reply = await session.config(doc);
                if (reply.ok) {
                    this.pushed = doc;
                } else if (reply.restart) {
                    await this.restart(`to apply the new config (${reply.error})`);
                    return;
                } else {
                    throw new Error(reply.error);
                }
            }
        }
        await this.refreshNextRuns().catch(() => undefined);
    }

    /** A change only a daemon restart applies: restart it with this copy's config and env. */
    private async restart(why: string): Promise<void> {
        this.log?.warn(`runwisp: restarting RunWisp ${why}`);
        this.session = undefined;
        this.booted = undefined;
        await stopDaemon(this.paths.socket);
        // Another copy may have restarted it meanwhile, with its own config.
        await this.boot();
        this.changed();
    }

    private async refreshNextRuns(): Promise<void> {
        const next = await nextRuns(this.paths.socket);
        this.next.clear();
        for (const [name, at] of next) this.next.set(name, at);
    }

    private readonly policy = (name: string): OverlapPolicy => {
        const task = this.tasks.get(name)?.options;
        if (task) {
            return {
                onOverlap: task.on_overlap ?? "queue",
                maxConcurrent: task.max_concurrent ?? 1,
            };
        }
        const service = this.services.get(name)?.options;
        return { onOverlap: "queue", maxConcurrent: service?.instances ?? 1 };
    };

    private readonly serve: Serve = (request, signal, sink) =>
        runCaptured(sink, async () => {
            const run: RunContext = { signal, env: request.env, params: request.params };
            const task = this.tasks.get(request.task)?.internals;
            const service = this.services.get(request.task)?.fn;
            if (!task && !service) {
                console.error(`runwisp: this copy of the app doesn't define "${request.task}"`);
                return 1;
            }
            try {
                if (task) await task.run(request.at, run);
                else await service?.(run);
                return 0;
            } catch (err) {
                console.error(ownStack(err));
                return 1;
            } finally {
                // Services have no next run to refresh.
                if (task)
                    this.refreshNextRuns().catch(() => {
                        // Best effort: getNextRun() keeps the previous value.
                    });
            }
        });
}
