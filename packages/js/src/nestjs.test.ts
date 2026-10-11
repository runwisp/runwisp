// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { Module, type DynamicModule } from "@nestjs/common";
import { NestFactory } from "@nestjs/core";
import { Test } from "@nestjs/testing";
import { describe, expect, it, vi } from "vitest";

import {
    Cron,
    CronExpression,
    Interval,
    RunWisp,
    RunWispModule,
    SchedulerRegistry,
    Timeout,
} from "./nestjs.ts";

class Jobs {
    calls: string[] = [];
    nightly() {
        this.calls.push("nightly");
    }
    paused() {
        return "paused";
    }
    poll() {
        return "poll";
    }
    warmUp() {
        return "warm";
    }
}

/** What the TypeScript decorator syntax compiles to, without needing experimentalDecorators here. */
function decorate(decorator: MethodDecorator, key: keyof Jobs): void {
    const descriptor = Object.getOwnPropertyDescriptor(Jobs.prototype, key);
    if (!descriptor) throw new Error(`no method ${key}`);
    decorator(Jobs.prototype, key, descriptor);
}

decorate(Cron(CronExpression.EVERY_DAY_AT_3AM, { timeZone: "Europe/Bratislava" }), "nightly");
decorate(
    Cron("0 * * * *", { name: "paused-job", disabled: true, waitForCompletion: true }),
    "paused",
);
decorate(Interval(30_000), "poll");
decorate(Timeout("warm-up", 5000), "warmUp");

const HOOKS = ["onModuleDestroy", "beforeApplicationShutdown", "onApplicationShutdown"];

/** A provider whose shutdown hooks record that they ran. */
function hooks(name: string, events: string[]) {
    const Provider = class {
        onModuleDestroy() {
            events.push(`${name} onModuleDestroy`);
        }
        beforeApplicationShutdown() {
            events.push(`${name} beforeApplicationShutdown`);
        }
        onApplicationShutdown() {
            events.push(`${name} onApplicationShutdown`);
        }
    };
    Object.defineProperty(Provider, "name", { value: name });
    return Provider;
}

function forRoot(): DynamicModule {
    return RunWispModule.forRoot({ enabled: false, log: false, binary: "/nonexistent/runwisp" });
}

/** RunWisp's close, taking a while as with a task still running. */
function slowClose(runwisp: RunWisp, events: string[]) {
    return vi.spyOn(runwisp, "close").mockImplementation(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
        events.push("runwisp closed");
    });
}

async function boot() {
    const module = await Test.createTestingModule({
        imports: [
            RunWispModule.forRoot({ enabled: false, log: false, binary: "/nonexistent/runwisp" }),
        ],
        providers: [Jobs],
    }).compile();
    await module.init();
    return module;
}

describe("@runwisp/js/nestjs", () => {
    it("registers decorated methods as RunWisp tasks", async () => {
        const module = await boot();
        const tasks = module.get(RunWisp).getTasks();
        expect([...tasks.keys()].sort()).toEqual([
            "Jobs.nightly",
            "Jobs.poll",
            "paused-job",
            "warm-up",
        ]);
        expect(tasks.get("Jobs.nightly")?.getPattern()).toBe("0 03 * * *");
        expect(tasks.get("Jobs.nightly")?.getStatus()).toBe("idle");
        expect(tasks.get("Jobs.poll")?.getPattern()).toBe("@every 30000ms");
        expect(tasks.get("paused-job")?.getStatus()).toBe("stopped");
        expect(tasks.get("warm-up")?.getPattern()).toBe("");

        await tasks.get("Jobs.nightly")?.execute();
        expect(module.get(Jobs).calls).toEqual(["nightly"]);
        await module.close();
    });

    it("exposes the tasks through SchedulerRegistry", async () => {
        const module = await boot();
        const registry = module.get(SchedulerRegistry);
        expect(registry.doesExist("cron", "Jobs.nightly")).toBe(true);
        expect(registry.doesExist("interval", "Jobs.nightly")).toBe(false);
        expect([...registry.getCronJobs().keys()].sort()).toEqual(["Jobs.nightly", "paused-job"]);
        expect(registry.getIntervals()).toEqual(["Jobs.poll"]);
        expect(registry.getTimeouts()).toEqual(["warm-up"]);

        const job = registry.getCronJob("Jobs.nightly");
        job.stop();
        expect(job.getStatus()).toBe("stopped");
        registry.deleteInterval("Jobs.poll");
        expect(module.get(RunWisp).getTask("Jobs.poll")).toBeUndefined();
        expect(() => registry.getTimeout("Jobs.poll")).toThrow('no timeout named "Jobs.poll"');
        expect(() => registry.addCronJob()).toThrow("not supported");
        await module.close();
    });

    // Nest orders shutdown hooks by module depth: root first from Nest 11,
    // deepest and global first in Nest 10. Whatever the order, the running
    // tasks finish before any hook closes a client they use.
    it.each(["first", "last"])(
        "lets running tasks finish before any shutdown hook, with forRoot() listed %s",
        async (position) => {
            const events: string[] = [];
            // eslint-disable-next-line @typescript-eslint/no-extraneous-class -- Nest modules are classes.
            class DbModule {}
            // eslint-disable-next-line @typescript-eslint/no-extraneous-class -- Nest modules are classes.
            class FeatureModule {}
            // eslint-disable-next-line @typescript-eslint/no-extraneous-class -- Nest modules are classes.
            class NestedModule {}
            const Db = hooks("global db", events);
            const userModules: DynamicModule[] = [
                { module: DbModule, global: true, providers: [Db], exports: [Db] },
                {
                    module: FeatureModule,
                    imports: [{ module: NestedModule, providers: [hooks("nested cache", events)] }],
                    providers: [hooks("feature repo", events)],
                },
            ];
            const runwisp = forRoot();
            const module = await Test.createTestingModule({
                imports:
                    position === "first" ? [runwisp, ...userModules] : [...userModules, runwisp],
                providers: [hooks("root service", events)],
            }).compile();
            await module.init();
            const close = slowClose(module.get(RunWisp), events);
            await module.close();

            expect(close).toHaveBeenCalledOnce();
            expect(events[0]).toBe("runwisp closed");
            expect(events.slice(1).sort()).toEqual(
                ["feature repo", "global db", "nested cache", "root service"]
                    .flatMap((name) => HOOKS.map((hook) => `${name} ${hook}`))
                    .sort(),
            );
        },
    );

    it("lets running tasks finish before the root module's hook, on app.close() without shutdown hooks", async () => {
        const events: string[] = [];
        class AppModule {
            onModuleDestroy() {
                events.push("app module onModuleDestroy");
            }
        }
        Module({ imports: [forRoot()], providers: [hooks("root service", events)] })(AppModule);
        const app = await NestFactory.createApplicationContext(AppModule, { logger: false });
        slowClose(app.get(RunWisp), events);
        await app.close();

        expect(events[0]).toBe("runwisp closed");
        expect(events).toContain("app module onModuleDestroy");
        expect(events).toContain("root service onApplicationShutdown");
    });

    it("still runs the app's shutdown hooks when RunWisp fails to close", async () => {
        const events: string[] = [];
        const module = await Test.createTestingModule({
            imports: [forRoot()],
            providers: [hooks("root service", events)],
        }).compile();
        module.useLogger(false);
        await module.init();
        vi.spyOn(module.get(RunWisp), "close").mockRejectedValue(new Error("boom"));
        await module.close();
        expect(events).toEqual(HOOKS.map((hook) => `root service ${hook}`));
    });

    it("still injects RunWisp and SchedulerRegistry into any module", async () => {
        class Consumer {
            readonly runwisp: RunWisp;
            readonly registry: SchedulerRegistry;
            constructor(runwisp: RunWisp, registry: SchedulerRegistry) {
                this.runwisp = runwisp;
                this.registry = registry;
            }
        }
        // eslint-disable-next-line @typescript-eslint/no-extraneous-class -- Nest modules are classes.
        class ConsumerModule {}
        const module = await Test.createTestingModule({
            imports: [
                RunWispModule.forRoot({
                    enabled: false,
                    log: false,
                    binary: "/nonexistent/runwisp",
                }),
                {
                    module: ConsumerModule,
                    providers: [
                        {
                            provide: Consumer,
                            useFactory: (runwisp: RunWisp, registry: SchedulerRegistry) =>
                                new Consumer(runwisp, registry),
                            inject: [RunWisp, SchedulerRegistry],
                        },
                    ],
                },
            ],
        }).compile();
        const consumer = module.get(Consumer);
        expect(consumer.runwisp).toBe(module.get(RunWisp));
        expect(consumer.registry).toBe(module.get(SchedulerRegistry));
        await module.close();
    });
});
