# @runwisp/node

node-cron, but you can see what ran. `@runwisp/node` runs your node-cron tasks
under [RunWisp](https://runwisp.com): every run is recorded with its output,
exit code and duration, and shown in a web UI. Your callbacks still run inside
your app.

```bash
npm install @runwisp/node
```

```ts
// import cron from "node-cron";
import { RunWisp } from "@runwisp/node";

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
- Runs once per data directory, however many copies of the app run.
- Retries, jitter, timeouts and notifications through `task()`.

Linux, macOS and WSL, Node 20 or later. Read the
[JavaScript SDK guide](https://docs.runwisp.com/sdk/javascript/) for every
option, and [From node-cron](https://docs.runwisp.com/coming-from/node-cron/)
for what differs from node-cron.

Apache-2.0. The `runwisp` binary it runs is GPL-3.0-or-later.
