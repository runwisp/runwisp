// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Next.js helpers: withRunWisp() for next.config, and scheduleVercelCrons()
// to run vercel.json's cron jobs under RunWisp on a self-hosted server.

import { relative } from "node:path";

import { binaryFiles } from "./daemon.ts";
import type { RunWisp } from "./runwisp.ts";

/** The part of Next's config withRunWisp() changes. */
export interface NextConfig {
    serverExternalPackages?: string[];
    outputFileTracingIncludes?: Record<string, string[]>;
}

/**
 * Keeps @runwisp/js out of Next's server bundle, so it finds the runwisp
 * binary, and copies the binary into `output: "standalone"` builds.
 */
export function withRunWisp<T extends NextConfig>(config: T): T {
    // Vercel runs each request in a short-lived function: no process for the
    // tasks to run in, and nothing would tell you they never ran.
    if (process.env["VERCEL"]) {
        throw new Error(
            "@runwisp/js/next: tasks need a server that keeps running, and Vercel doesn't run one. Self-host the app, or remove withRunWisp().",
        );
    }
    const tracing = config.outputFileTracingIncludes ?? {};
    return {
        ...config,
        serverExternalPackages: [...(config.serverExternalPackages ?? []), "@runwisp/js"],
        outputFileTracingIncludes: {
            ...tracing,
            // Next matches the keys against route names with picomatch:
            // "/*" misses "/" and "instrumentation", "**" matches every one.
            // Relative: Next globs the values from the project directory.
            "**": [
                ...(tracing["**"] ?? []),
                ...binaryFiles().map((f) => relative(process.cwd(), f)),
            ],
        },
    };
}

/** The part of vercel.json scheduleVercelCrons() reads. */
export interface VercelConfig {
    crons?: readonly { path: string; schedule: string }[];
}

export interface VercelCronOptions {
    /** Where the app is served. Default: `http://localhost:$PORT` (3000 without `PORT`). */
    baseUrl?: string;
}

/**
 * Schedules each of vercel.json's `crons` as a RunWisp task, named after its
 * path, that requests the path the way Vercel does: a GET with
 * `Authorization: Bearer $CRON_SECRET`. A response other than 2xx fails the
 * run. Schedules are in UTC, as on Vercel.
 */
export function scheduleVercelCrons(
    runwisp: RunWisp,
    vercel: VercelConfig,
    options: VercelCronOptions = {},
): void {
    const baseUrl = options.baseUrl ?? `http://localhost:${process.env["PORT"] ?? "3000"}`;
    for (const { path, schedule } of vercel.crons ?? []) {
        const name =
            path
                .replace(/^\/+/, "")
                .replaceAll(/[^a-zA-Z0-9._:-]+/g, "-")
                .slice(0, 100) || "root";
        runwisp.task(name, { cron: schedule, timezone: "UTC" }, async ({ signal }) => {
            const secret = process.env["CRON_SECRET"];
            const response = await fetch(new URL(path, baseUrl), {
                headers: {
                    "user-agent": "vercel-cron/1.0",
                    ...(secret ? { authorization: `Bearer ${secret}` } : {}),
                },
                signal,
            });
            const body = await response.text();
            if (body !== "") console.log(body);
            if (!response.ok) {
                throw new Error(`GET ${path}: ${String(response.status)} ${response.statusText}`);
            }
        });
    }
}
