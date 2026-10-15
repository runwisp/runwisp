// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A script that schedules a task and destroys it, for the e2e tests:
// `node destroyed.ts <data> <port> <binary>`. Like with node-cron, nothing
// keeps it running after that, so it exits on its own.

import { RunWisp } from "../../src/index.ts";

const [data, port, binary] = process.argv.slice(2);
if (!data || !port || !binary) throw new Error("usage: destroyed.ts <data> <port> <binary>");

const cron = new RunWisp({ data, port: Number(port), binary, log: false });
const task = cron.schedule("0 3 * * *", () => undefined, { name: "nightly" });
await cron.start();
task.destroy();
