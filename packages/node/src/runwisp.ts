// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createHash } from "node:crypto";
import { isAbsolute, join, relative } from "node:path";
import { setImmediate as nextTurn } from "node:timers/promises";

import { ownStack, runCaptured } from "./capture.ts";
import {
    choosePort,
    daemonPaths,
    dashboardUrl,
    DEFAULT_PORT,
    healthy,
    identity,
    nextRuns,
    prepareDirs,
    resolveSettings,
    savedPort,
    startDaemon,
    stopDaemon,
    validateConfig,
    validateCron,
    type DaemonPaths,
    type Settings,
} from "./daemon.ts";
import type { Config, ServiceConfig, TaskConfig } from "./generated/config.ts";
import { Runner, type OverlapPolicy, type Serve } from "./runner.ts";
import {
    ScheduledTask,
    type NodeCronOptions,
    type RunContext,
    type TaskControl,
    type TaskFn,
} from "./scheduled-task.ts";
import { Session, type SessionEvents } from "./session.ts";

export interface Logger {
    info(message: string): void;
    warn(message: string): void;
    error(message: string): void;
}

export interface RunWispOptions {
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
}

interface ServiceJob {
    options: ServiceOptions;
    fn: ServiceFn;
}

// One instance per data directory per process: `new RunWisp()` in every file
// that schedules tasks gets the same one, like node-cron's module.
const instances = new Map<string, RunWisp>();

/** Settings two `new RunWisp()` for one data directory must agree on. */
const SHARED_SETTINGS = ["port", "host", "auth", "password", "binary"] as const;

const UNIT_NAME = /^[a-zA-Z0-9._:-]{1,100}$/;

// node-cron lets runs overlap unless noOverlap is set; RunWisp's cap on that.
const NODE_CRON_MAX_CONCURRENT = 1024;

// How soon a copy reconnects after RunWisp went away.
const RECONNECT_MS = 1000;

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
    private readonly log: Logger | undefined;
    private readonly config: Config;
    private readonly runner: Runner;
    private readonly tasks = new Map<string, TaskJob>();
    private readonly services = new Map<string, ServiceJob>();
    private readonly next = new Map<string, Date>();
    private readonly warned = new Set<string>();
    private session: Session | undefined;
    /** The config the daemon has from this copy. */
    private pushed: string | undefined;
    private booted: Promise<void> | undefined;
    private pending: Promise<void> | undefined;
    private queue: Promise<void> = Promise.resolve();
    private announced = false;
    private closed = false;

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
        nextRun: (name) => this.next.get(name) ?? null,
    };

    constructor(options: RunWispOptions = {}) {
        const settings = resolveSettings(options);
        this.settings = settings;
        this.paths = daemonPaths(settings.data);
        this.enabled = options.enabled ?? process.env["RUNWISP_NODE"] !== "off";
        this.log = options.log === false ? undefined : (options.log ?? console);
        this.config = options.config ?? {};
        this.runner = new Runner(this.serve, this.policy);
        if (options.config) this.check(options.config);
        if (!this.enabled) return;
        const existing = instances.get(settings.data);
        if (existing) {
            existing.assertSameSettings(settings, options.config);
            return existing;
        }
        instances.set(settings.data, this);
    }

    /** The web UI address. */
    get url(): string {
        const port = this.settings.port ?? savedPort(this.paths) ?? DEFAULT_PORT;
        return dashboardUrl(this.settings.host, port);
    }

    /** node-cron's `schedule`: registers the task in RunWisp and starts it. */
    schedule(expression: string, fn: TaskFn, options: NodeCronOptions = {}): ScheduledTask {
        const task = this.createTask(expression, fn, options);
        task.start();
        return task;
    }

    /** node-cron's `createTask`: registers the task in RunWisp, stopped (no schedule) until `start()`. */
    createTask(expression: string, fn: TaskFn, options: NodeCronOptions = {}): ScheduledTask {
        if (typeof fn !== "function") {
            throw new TypeError(
                "@runwisp/node runs callbacks; a file path (node-cron background task) is not supported",
            );
        }
        this.warnIgnored(options);
        const name = options.name ?? this.derivedName(expression, fn);
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
        return this.addTask(id, name, fn, options.maxExecutions, taskOptions);
    }

    /**
     * Registers a task with any `[tasks.<name>]` key (retries, jitter,
     * catch-up, timeout, notifications, ...) and starts it. Without `cron`,
     * the task only runs from the web UI, TUI or CLI.
     */
    task(name: string, options: TaskOptions, fn: TaskFn): ScheduledTask {
        const task = this.addTask(name, name, fn, undefined, options);
        task.start();
        return task;
    }

    /**
     * Registers a service with any `[services.<name>]` key: RunWisp keeps fn
     * running, restarting it when it returns or throws, and asks it to stop
     * through `ctx.signal`.
     */
    service(name: string, options: ServiceOptions, fn: ServiceFn): void {
        this.assertNewName(name);
        this.check(this.unitConfig({ services: { [name]: { ...options, sdk: true } } }));
        this.services.set(name, { options, fn });
        this.changed();
    }

    /** node-cron's `validate`, answered by `runwisp validate` so the grammar is exactly RunWisp's. */
    validate(expression: string): boolean {
        return validateCron(this.settings.binary, this.paths, expression);
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

    /** node-cron's `shutdown`: same as close(). */
    shutdown(): Promise<void> {
        return this.close();
    }

    /**
     * Stops serving tasks, after the running ones finish, and stops the
     * services. Stops RunWisp too, unless another copy of the app still uses it.
     */
    async close(): Promise<void> {
        this.closed = true;
        if (instances.get(this.settings.data) === this) instances.delete(this.settings.data);
        await this.queue;
        this.session?.leave();
        await this.runner.close();
        process.off("uncaughtExceptionMonitor", this.runner.reportCrash);
        const session = this.session;
        this.session = undefined;
        await session?.close();
    }

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

    private assertNewName(name: string): void {
        if (!UNIT_NAME.test(name)) {
            throw new Error(`invalid task name "${name}": use 1-100 of a-z A-Z 0-9 . _ : -`);
        }
        if (
            this.tasks.has(name) ||
            this.services.has(name) ||
            name in (this.config.tasks ?? {}) ||
            name in (this.config.services ?? {})
        ) {
            throw new Error(`a task named "${name}" is already scheduled`);
        }
    }

    private addTask(
        id: string,
        name: string,
        fn: TaskFn,
        maxExecutions: number | undefined,
        options: TaskOptions,
    ): ScheduledTask {
        this.assertNewName(id);
        this.check(this.unitConfig({ tasks: { [id]: { ...options, sdk: true } } }));
        const task = new ScheduledTask(
            this.control,
            id,
            name,
            options.cron ?? "",
            fn,
            maxExecutions,
        );
        this.tasks.set(id, { options, paused: true, task });
        this.changed();
        return task;
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

    private derivedName(expression: string, fn: TaskFn): string {
        const hash = createHash("sha256")
            .update(`${expression}\n${fn.toString()}`)
            .digest("hex")
            .slice(0, 8);
        let name = `task-${hash}`;
        for (let n = 2; this.tasks.has(name); n++) name = `task-${hash}-${String(n)}`;
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
                this.log?.error(`runwisp: ${message(err)}`);
            });
        }
        return this.pending;
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
        if (!(await healthy(this.paths.socket))) {
            const port = await choosePort(this.settings, this.paths);
            await startDaemon(this.settings, this.paths, port, this.document());
        }
        const daemon = await identity(this.paths.socket);
        if (daemon.configSource !== "app") {
            throw new Error(
                `the RunWisp daemon on ${this.settings.data} runs ${daemon.configPath}, not this app's tasks; pass a different data directory`,
            );
        }
        const events: SessionEvents = {
            role: (session, active) => {
                if (!active && this.closed) {
                    // Handed over while closing: the next copy runs the services now.
                    this.runner.stopTasks(this.services.keys());
                } else if (active) {
                    // A copy that becomes active supplies its own config.
                    this.pushed = undefined;
                    if (this.session === session) this.changed();
                }
            },
            run: (session, request) => {
                this.runner.start(session, request);
            },
            stop: (run) => {
                this.runner.stop(run);
            },
            closed: (session) => {
                this.runner.disconnected(session);
                if (this.session !== session) return;
                this.session = undefined;
                this.booted = undefined;
                if (!this.closed)
                    setTimeout(() => {
                        this.changed();
                    }, RECONNECT_MS);
            },
        };
        this.session = await Session.open(this.paths.socket, events);
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
        if (this.paths.socket !== join(data, "runwisp.sock"))
            return `npx runwisp password --socket ${shellQuote(this.paths.socket)}`;
        const rel = relative(process.cwd(), data);
        const shown = rel === "" || rel.startsWith("..") || isAbsolute(rel) ? data : rel;
        return `npx runwisp password --data ${shellQuote(shown)}`;
    }

    /** The active copy pushes its config when it changed; a standby copy only follows. */
    private async apply(): Promise<void> {
        const session = this.session;
        if (session?.active === true) {
            const doc = this.document();
            if (doc !== this.pushed) {
                const reply = await session.config(doc);
                if (reply.ok) {
                    this.pushed = doc;
                } else if (reply.restart) {
                    await this.restart(reply.error);
                    return;
                } else {
                    throw new Error(reply.error);
                }
            }
        }
        await this.refreshNextRuns().catch(() => undefined);
    }

    /** A change only a daemon restart applies: restart it with this copy's config. */
    private async restart(why: string): Promise<void> {
        this.log?.warn(`runwisp: restarting RunWisp to apply the new config (${why})`);
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
            const task = this.tasks.get(request.task)?.task;
            const service = this.services.get(request.task)?.fn;
            if (!task && !service) {
                console.error(`runwisp: this copy of the app doesn't define "${request.task}"`);
                return 1;
            }
            try {
                if (task) await task.run("scheduled", run);
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
