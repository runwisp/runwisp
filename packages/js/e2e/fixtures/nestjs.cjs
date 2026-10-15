// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A Nest app compiled to CommonJS, Nest's default: require()s the built
// package, boots with a decorated provider, and prints the tasks it got.

"use strict";

/* global console */

const { Injectable, Module } = require("@nestjs/common");
const { NestFactory } = require("@nestjs/core");
const { Cron, Interval, RunWisp, RunWispModule, Timeout } = require("@runwisp/js/nestjs");

// What tsc emits for decorators with experimentalDecorators.
class Jobs {
    nightly() {}
    poll() {}
    warmUp() {}
}
Reflect.decorate([Injectable()], Jobs);
for (const [key, decorator] of [
    ["nightly", Cron("0 3 * * *")],
    ["poll", Interval(30_000)],
    ["warmUp", Timeout("warm-up", 5000)],
]) {
    const descriptor = Object.getOwnPropertyDescriptor(Jobs.prototype, key);
    Reflect.decorate([decorator], Jobs.prototype, key, descriptor);
}

class AppModule {}
Reflect.decorate(
    [
        Module({
            imports: [RunWispModule.forRoot({ enabled: false, log: false })],
            providers: [Jobs],
        }),
    ],
    AppModule,
);

void (async () => {
    const app = await NestFactory.createApplicationContext(AppModule, { logger: false });
    const tasks = app.get(RunWisp).getTasks();
    console.log(JSON.stringify([...tasks].map(([name, task]) => [name, task.getPattern()])));
    await app.close();
})();
