# @runwisp/js

node-cron, but you can see what ran. `@runwisp/js` runs your node-cron tasks
under [RunWisp](https://runwisp.com): every run is recorded with its output,
exit code and duration, and shown in a web UI. Your callbacks still run inside
your app.

```bash
npm install @runwisp/js
```

```ts
// import cron from "node-cron";
import { RunWisp } from "@runwisp/js";

const cron = new RunWisp();

cron.schedule(
    "0 3 * * *",
    async () => {
        console.log("dumping the database");
    },
    { name: "nightly-backup" },
);
```

```
RunWisp dashboard: http://127.0.0.1:9477, password: npx runwisp password --data .runwisp
```

- Thrown errors fail the run, with the stack trace in its log.
- Runs once per data directory, however many copies of the app run. A
  different app on the same data directory is refused.
- Retries, jitter, timeouts and notifications through `task()`.

Framework integrations:

- `@runwisp/js/nestjs`: a drop-in for `@nestjs/schedule`.
- `@runwisp/js/nuxt`: a Nuxt module that also runs Nitro's `scheduledTasks`.
- `@runwisp/js/next`: `withRunWisp()` for `next.config`, and Vercel Cron jobs from `vercel.json`.
- `@runwisp/js/astro`: starts the tasks when `astro dev` or the Node adapter's built server starts.

Linux, macOS and WSL, Node 20.19, 22.12 or later, or Bun 1.1 or later. Read the
[JavaScript SDK guide](https://docs.runwisp.com/sdk/javascript/) for every
option, and [From node-cron](https://docs.runwisp.com/coming-from/node-cron/)
for what differs from node-cron.

Apache-2.0. The `runwisp` binary it runs is GPL-3.0-or-later.
