// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A node-cron background task file, for the e2e tests: runs in its own process.

export function task(ctx: { execution: { reason: string }; env: Record<string, string> }): string {
    console.log(
        `background ${ctx.execution.reason} pid ${String(process.pid)} region ${String(ctx.env["REGION"])}`,
    );
    return "done";
}
