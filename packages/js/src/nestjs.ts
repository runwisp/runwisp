// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A drop-in for @nestjs/schedule: the same module, decorators and registry,
// with RunWisp holding the schedule. Importing from "@runwisp/js/nestjs"
// instead of "@nestjs/schedule" is the whole migration.

import {
    Logger,
    SetMetadata,
    type DynamicModule,
    type OnModuleDestroy,
    type OnModuleInit,
} from "@nestjs/common";
import { DiscoveryModule, DiscoveryService, MetadataScanner, Reflector } from "@nestjs/core";

import { OVERLAPPING } from "./overlap.ts";
import { RunWisp, type RunWispOptions } from "./runwisp.ts";
import type { TaskOptions, ScheduledTask, TaskFn } from "./scheduled-task.ts";

const SCHEDULE = "runwisp:schedule";

type Kind = "cron" | "interval" | "timeout";

type Schedule =
    | { kind: "cron"; cron: string; options: CronOptions }
    | { kind: "interval" | "timeout"; name: string | undefined; ms: number };

/** @nestjs/schedule's CronOptions. */
export interface CronOptions {
    /** The RunWisp task name. Default: `ClassName.methodName`. */
    name?: string;
    timeZone?: string;
    /** true skips a run that fires while the previous one is still running. Default: runs overlap. */
    waitForCompletion?: boolean;
    /** true registers the task without its schedule: it runs from the web UI, TUI or CLI, or after `getCronJob(name).start()`. */
    disabled?: boolean;
    /** @deprecated Ignored: RunWisp takes a time zone, not a fixed offset. Use timeZone. */
    utcOffset?: number;
    /** @deprecated Ignored: RunWisp has no startup delay for scheduled runs. */
    initialDelay?: number;
    /** @deprecated Ignored: RunWisp holds the schedule, not a timer in this process. */
    unrefTimeout?: boolean;
    /** @deprecated Ignored: RunWisp never runs a missed time late. */
    threshold?: number;
}

// Accepted so code written for @nestjs/schedule compiles, and warned about once each.
const IGNORED_CRON_OPTIONS: [keyof CronOptions, string][] = [
    ["utcOffset", "RunWisp takes a time zone, not a fixed offset; use timeZone"],
    ["initialDelay", "RunWisp has no startup delay for scheduled runs"],
    ["unrefTimeout", "RunWisp holds the schedule, not a timer in this process"],
    ["threshold", "RunWisp never runs a missed time late"],
];

/** @nestjs/schedule's CronExpression, with the same values. */
export const CronExpression = {
    EVERY_SECOND: "* * * * * *",
    EVERY_5_SECONDS: "*/5 * * * * *",
    EVERY_10_SECONDS: "*/10 * * * * *",
    EVERY_30_SECONDS: "*/30 * * * * *",
    EVERY_MINUTE: "*/1 * * * *",
    EVERY_5_MINUTES: "0 */5 * * * *",
    EVERY_10_MINUTES: "0 */10 * * * *",
    EVERY_30_MINUTES: "0 */30 * * * *",
    EVERY_HOUR: "0 0-23/1 * * *",
    EVERY_2_HOURS: "0 0-23/2 * * *",
    EVERY_3_HOURS: "0 0-23/3 * * *",
    EVERY_4_HOURS: "0 0-23/4 * * *",
    EVERY_5_HOURS: "0 0-23/5 * * *",
    EVERY_6_HOURS: "0 0-23/6 * * *",
    EVERY_7_HOURS: "0 0-23/7 * * *",
    EVERY_8_HOURS: "0 0-23/8 * * *",
    EVERY_9_HOURS: "0 0-23/9 * * *",
    EVERY_10_HOURS: "0 0-23/10 * * *",
    EVERY_11_HOURS: "0 0-23/11 * * *",
    EVERY_12_HOURS: "0 0-23/12 * * *",
    EVERY_DAY_AT_1AM: "0 01 * * *",
    EVERY_DAY_AT_2AM: "0 02 * * *",
    EVERY_DAY_AT_3AM: "0 03 * * *",
    EVERY_DAY_AT_4AM: "0 04 * * *",
    EVERY_DAY_AT_5AM: "0 05 * * *",
    EVERY_DAY_AT_6AM: "0 06 * * *",
    EVERY_DAY_AT_7AM: "0 07 * * *",
    EVERY_DAY_AT_8AM: "0 08 * * *",
    EVERY_DAY_AT_9AM: "0 09 * * *",
    EVERY_DAY_AT_10AM: "0 10 * * *",
    EVERY_DAY_AT_11AM: "0 11 * * *",
    EVERY_DAY_AT_NOON: "0 12 * * *",
    EVERY_DAY_AT_1PM: "0 13 * * *",
    EVERY_DAY_AT_2PM: "0 14 * * *",
    EVERY_DAY_AT_3PM: "0 15 * * *",
    EVERY_DAY_AT_4PM: "0 16 * * *",
    EVERY_DAY_AT_5PM: "0 17 * * *",
    EVERY_DAY_AT_6PM: "0 18 * * *",
    EVERY_DAY_AT_7PM: "0 19 * * *",
    EVERY_DAY_AT_8PM: "0 20 * * *",
    EVERY_DAY_AT_9PM: "0 21 * * *",
    EVERY_DAY_AT_10PM: "0 22 * * *",
    EVERY_DAY_AT_11PM: "0 23 * * *",
    EVERY_DAY_AT_MIDNIGHT: "0 0 * * *",
    EVERY_WEEK: "0 0 * * 0",
    EVERY_WEEKDAY: "0 0 * * 1-5",
    EVERY_WEEKEND: "0 0 * * 6,0",
    EVERY_1ST_DAY_OF_MONTH_AT_MIDNIGHT: "0 0 1 * *",
    EVERY_1ST_DAY_OF_MONTH_AT_NOON: "0 12 1 * *",
    EVERY_2ND_HOUR: "0 */2 * * *",
    EVERY_2ND_HOUR_FROM_1AM_THROUGH_11PM: "0 1-23/2 * * *",
    EVERY_2ND_MONTH: "0 0 1 */2 *",
    EVERY_QUARTER: "0 0 1 */3 *",
    EVERY_6_MONTHS: "0 0 1 */6 *",
    EVERY_YEAR: "0 0 1 1 *",
    EVERY_30_MINUTES_BETWEEN_9AM_AND_5PM: "0 */30 9-17 * * *",
    EVERY_30_MINUTES_BETWEEN_9AM_AND_6PM: "0 */30 9-18 * * *",
    EVERY_30_MINUTES_BETWEEN_10AM_AND_7PM: "0 */30 10-19 * * *",
    MONDAY_TO_FRIDAY_AT_1AM: "0 0 01 * * 1-5",
    MONDAY_TO_FRIDAY_AT_2AM: "0 0 02 * * 1-5",
    MONDAY_TO_FRIDAY_AT_3AM: "0 0 03 * * 1-5",
    MONDAY_TO_FRIDAY_AT_4AM: "0 0 04 * * 1-5",
    MONDAY_TO_FRIDAY_AT_5AM: "0 0 05 * * 1-5",
    MONDAY_TO_FRIDAY_AT_6AM: "0 0 06 * * 1-5",
    MONDAY_TO_FRIDAY_AT_7AM: "0 0 07 * * 1-5",
    MONDAY_TO_FRIDAY_AT_8AM: "0 0 08 * * 1-5",
    MONDAY_TO_FRIDAY_AT_9AM: "0 0 09 * * 1-5",
    MONDAY_TO_FRIDAY_AT_09_30AM: "0 30 09 * * 1-5",
    MONDAY_TO_FRIDAY_AT_10AM: "0 0 10 * * 1-5",
    MONDAY_TO_FRIDAY_AT_11AM: "0 0 11 * * 1-5",
    MONDAY_TO_FRIDAY_AT_11_30AM: "0 30 11 * * 1-5",
    MONDAY_TO_FRIDAY_AT_12PM: "0 0 12 * * 1-5",
    MONDAY_TO_FRIDAY_AT_1PM: "0 0 13 * * 1-5",
    MONDAY_TO_FRIDAY_AT_2PM: "0 0 14 * * 1-5",
    MONDAY_TO_FRIDAY_AT_3PM: "0 0 15 * * 1-5",
    MONDAY_TO_FRIDAY_AT_4PM: "0 0 16 * * 1-5",
    MONDAY_TO_FRIDAY_AT_5PM: "0 0 17 * * 1-5",
    MONDAY_TO_FRIDAY_AT_6PM: "0 0 18 * * 1-5",
    MONDAY_TO_FRIDAY_AT_7PM: "0 0 19 * * 1-5",
    MONDAY_TO_FRIDAY_AT_8PM: "0 0 20 * * 1-5",
    MONDAY_TO_FRIDAY_AT_9PM: "0 0 21 * * 1-5",
    MONDAY_TO_FRIDAY_AT_10PM: "0 0 22 * * 1-5",
    MONDAY_TO_FRIDAY_AT_11PM: "0 0 23 * * 1-5",
} as const;
export type CronExpression = (typeof CronExpression)[keyof typeof CronExpression];

/** Runs the method on a cron schedule. The method gets the run's context (`signal`, `env`, `params`). */
export function Cron(cronTime: string, options: CronOptions = {}): MethodDecorator {
    if (typeof cronTime !== "string") {
        throw new TypeError(
            "@runwisp/js/nestjs: @Cron takes a cron expression; a Date (a one-off job) is not supported",
        );
    }
    return SetMetadata<string, Schedule>(SCHEDULE, { kind: "cron", cron: cronTime, options });
}

/** Runs the method every `timeout` milliseconds: whole seconds, at least 1000. */
export function Interval(timeout: number): MethodDecorator;
export function Interval(name: string, timeout: number): MethodDecorator;
export function Interval(nameOrTimeout: string | number, timeout?: number): MethodDecorator {
    return timed("interval", nameOrTimeout, timeout);
}

/** Runs the method once when RunWisp starts. The delay is ignored. */
export function Timeout(timeout: number): MethodDecorator;
export function Timeout(name: string, timeout: number): MethodDecorator;
export function Timeout(nameOrTimeout: string | number, timeout?: number): MethodDecorator {
    return timed("timeout", nameOrTimeout, timeout);
}

function timed(
    kind: "interval" | "timeout",
    nameOrTimeout: string | number,
    timeout: number | undefined,
): MethodDecorator {
    const schedule: Schedule =
        typeof nameOrTimeout === "string"
            ? { kind, name: nameOrTimeout, ms: timeout ?? 0 }
            : { kind, name: undefined, ms: nameOrTimeout };
    // RunWisp's intervals are whole seconds: rounding would change how often it runs.
    if (kind === "interval" && !(Number.isInteger(schedule.ms / 1000) && schedule.ms >= 1000)) {
        throw new TypeError(
            `@runwisp/js/nestjs: @Interval(${String(schedule.ms)}): RunWisp runs intervals in whole seconds, so it takes a multiple of 1000 milliseconds, at least 1000`,
        );
    }
    return SetMetadata(SCHEDULE, schedule);
}

const NOUNS: Record<Kind, string> = { cron: "cron job", interval: "interval", timeout: "timeout" };

function unsupported(method: string): Error {
    return new Error(
        `@runwisp/js/nestjs: SchedulerRegistry.${method}() is not supported; inject RunWisp and call task() instead`,
    );
}

interface Job {
    kind: Kind;
    task: ScheduledTask;
}

// Each registry's jobs, kept out of the class so that adding one is not public API.
const registries = new WeakMap<SchedulerRegistry, Map<string, Job>>();

function jobsOf(registry: SchedulerRegistry): Map<string, Job> {
    let jobs = registries.get(registry);
    if (!jobs) {
        jobs = new Map();
        registries.set(registry, jobs);
    }
    return jobs;
}

/**
 * @nestjs/schedule's SchedulerRegistry, for the decorated methods. Each job
 * is the task's handle: `stop()`, `start()`, `getNextRun()`, `isBusy()`.
 */
export class SchedulerRegistry {
    private get jobs(): Map<string, Job> {
        return jobsOf(this);
    }

    doesExist(type: "cron" | "interval" | "timeout", name: string): boolean {
        return this.jobs.get(name)?.kind === type;
    }

    getCronJob(name: string): ScheduledTask {
        return this.get("cron", name);
    }

    getInterval(name: string): ScheduledTask {
        return this.get("interval", name);
    }

    getTimeout(name: string): ScheduledTask {
        return this.get("timeout", name);
    }

    getCronJobs(): Map<string, ScheduledTask> {
        return new Map(
            [...this.jobs].filter(([, job]) => job.kind === "cron").map(([n, j]) => [n, j.task]),
        );
    }

    getIntervals(): string[] {
        return this.names("interval");
    }

    getTimeouts(): string[] {
        return this.names("timeout");
    }

    /** Removes the task from RunWisp. Its past runs stay until retention removes them. */
    deleteCronJob(name: string): void {
        this.delete("cron", name);
    }

    deleteInterval(name: string): void {
        this.delete("interval", name);
    }

    deleteTimeout(name: string): void {
        this.delete("timeout", name);
    }

    addCronJob(): never {
        throw unsupported("addCronJob");
    }

    addInterval(): never {
        throw unsupported("addInterval");
    }

    addTimeout(): never {
        throw unsupported("addTimeout");
    }

    private get(kind: Kind, name: string): ScheduledTask {
        const job = this.jobs.get(name);
        if (job?.kind !== kind) throw new Error(`no ${NOUNS[kind]} named "${name}"`);
        return job.task;
    }

    private names(kind: Kind): string[] {
        return [...this.jobs].filter(([, job]) => job.kind === kind).map(([name]) => name);
    }

    private delete(kind: Kind, name: string): void {
        this.get(kind, name).destroy();
        this.jobs.delete(name);
    }
}

// Nest's shutdown hooks, in the order it calls them.
const SHUTDOWN_HOOKS = ["onModuleDestroy", "beforeApplicationShutdown", "onApplicationShutdown"];

/**
 * Finds the decorated methods once every provider exists, and closes RunWisp
 * on shutdown, before any other shutdown hook runs.
 */
class Explorer implements OnModuleInit, OnModuleDestroy {
    private readonly logger = new Logger("RunWisp");
    private readonly warned = new Set<keyof CronOptions>();
    private readonly runwisp: RunWisp;
    private readonly registry: SchedulerRegistry;
    private readonly discovery: DiscoveryService;
    private readonly scanner: MetadataScanner;
    private closing: Promise<void> | undefined;

    constructor(
        runwisp: RunWisp,
        registry: SchedulerRegistry,
        discovery: DiscoveryService,
        scanner: MetadataScanner,
    ) {
        this.runwisp = runwisp;
        this.registry = registry;
        this.discovery = discovery;
        this.scanner = scanner;
    }

    onModuleInit(): void {
        for (const wrapper of [
            ...this.discovery.getControllers(),
            ...this.discovery.getProviders(),
        ]) {
            const instance: unknown = wrapper.instance;
            if (typeof instance !== "object" || instance === null || instance === this) continue;
            const isStatic = wrapper.isDependencyTreeStatic();
            if (isStatic) this.closeFirst(instance);
            this.scheduleMethods(instance, isStatic);
        }
    }

    onModuleDestroy(): Promise<void> {
        return this.close();
    }

    // Logs a failure rather than rejecting: Nest 10 and 11 stop the whole
    // shutdown at a rejected hook, and the app's own hooks must still run.
    private close(): Promise<void> {
        this.closing ??= this.runwisp.close().catch((err: unknown) => {
            this.logger.error(
                `could not close RunWisp: ${err instanceof Error ? err.message : String(err)}`,
            );
        });
        return this.closing;
    }

    /**
     * Makes the instance's shutdown hooks wait for the running tasks to
     * finish, so the clients the tasks use stay open. Nest orders the hooks
     * by module depth, the other way round in Nest 10, and runs the root
     * module's first; no place for this module comes before all of them.
     * Nest calls each hook as a method of the instance, in every version.
     */
    private closeFirst(instance: object): void {
        for (const hook of SHUTDOWN_HOOKS) {
            const fn: unknown = Reflect.get(instance, hook);
            if (typeof fn !== "function") continue;
            const wrapped = async (...args: unknown[]): Promise<unknown> => {
                await this.close();
                return Reflect.apply(fn, instance, args);
            };
            if (!Reflect.set(instance, hook, wrapped)) {
                this.logger.warn(
                    `${instance.constructor.name}.${hook} may run before the running tasks finish`,
                );
            }
        }
    }

    private scheduleMethods(instance: object, isStatic: boolean): void {
        for (const [key, fn, schedule] of this.scheduled(instance)) {
            const owner = `${instance.constructor.name}.${key}`;
            if (!isStatic) {
                this.logger.warn(
                    `cannot schedule ${owner}: it is in a request-scoped or transient provider`,
                );
                continue;
            }
            const name =
                (schedule.kind === "cron" ? schedule.options.name : schedule.name) ?? owner;
            jobsOf(this.registry).set(name, {
                kind: schedule.kind,
                task: this.register(schedule, name, fn),
            });
        }
    }

    /** The instance's methods that have one of the decorators, bound to it. */
    private *scheduled(instance: object): Generator<[string, TaskFn, Schedule]> {
        const prototype: unknown = Object.getPrototypeOf(instance);
        if (typeof prototype !== "object" || prototype === null) return;
        const reflector = new Reflector();
        for (const key of this.scanner.getAllMethodNames(prototype)) {
            const method: unknown = Reflect.get(instance, key);
            if (typeof method !== "function") continue;
            const schedule = reflector.get<Schedule | undefined>(SCHEDULE, method);
            if (schedule)
                yield [
                    key,
                    (context): unknown => Reflect.apply(method, instance, [context]),
                    schedule,
                ];
        }
    }

    private register(schedule: Schedule, name: string, fn: TaskFn): ScheduledTask {
        switch (schedule.kind) {
            case "cron": {
                const { options } = schedule;
                for (const [key, why] of IGNORED_CRON_OPTIONS) {
                    if (options[key] === undefined || this.warned.has(key)) continue;
                    this.warned.add(key);
                    this.logger.warn(`${name}: @Cron option ${key} is ignored: ${why}`);
                }
                // Overlap and missed-run behavior as in @nestjs/schedule (and node-cron).
                const cron: TaskOptions = { name };
                if (options.timeZone !== undefined) cron.timezone = options.timeZone;
                if (options.waitForCompletion === true) cron.noOverlap = true;
                return options.disabled === true
                    ? this.runwisp.createTask(schedule.cron, fn, cron)
                    : this.runwisp.schedule(schedule.cron, fn, cron);
            }
            case "interval":
                return this.runwisp.schedule(`@every ${String(schedule.ms / 1000)}s`, fn, {
                    name,
                });
            // Once per start, as @nestjs/schedule's is once per app start;
            // runs started by hand overlap like the other decorators'.
            case "timeout":
                return this.runwisp.task(name, { run_on_start: true, ...OVERLAPPING }, fn);
        }
    }
}

/**
 * Runs the decorated methods under RunWisp: import it once, in the root
 * module. Provides RunWisp (inject it to add tasks in code) and
 * SchedulerRegistry to every module.
 */
// eslint-disable-next-line @typescript-eslint/no-extraneous-class -- Nest modules are classes.
export class RunWispModule {
    static forRoot(options: RunWispOptions = {}): DynamicModule {
        return {
            module: RunWispModule,
            global: true,
            imports: [DiscoveryModule],
            providers: [
                { provide: RunWisp, useFactory: () => new RunWisp(options) },
                { provide: SchedulerRegistry, useFactory: () => new SchedulerRegistry() },
                {
                    provide: Explorer,
                    useFactory: (
                        runwisp: RunWisp,
                        registry: SchedulerRegistry,
                        discovery: DiscoveryService,
                        scanner: MetadataScanner,
                    ) => new Explorer(runwisp, registry, discovery, scanner),
                    inject: [RunWisp, SchedulerRegistry, DiscoveryService, MetadataScanner],
                },
            ],
            exports: [RunWisp, SchedulerRegistry],
        };
    }
}

/** @nestjs/schedule's name for RunWispModule. */
export { RunWispModule as ScheduleModule };
export { RunWisp };
