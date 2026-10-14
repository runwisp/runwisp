// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from "vitest";

import { scheduleVercelCrons, withRunWisp } from "./next.ts";
import { RunWisp } from "./runwisp.ts";

describe("withRunWisp", () => {
    // Next matches the keys with picomatch (contains: true): "/*" misses the
    // "/" route and the instrumentation entry, so standalone builds of an
    // app whose other routes are static would ship without the binary.
    it("adds to the app's config without replacing it, under a key that matches every route", () => {
        const config = withRunWisp({
            reactStrictMode: true,
            serverExternalPackages: ["sharp"],
            outputFileTracingIncludes: { "**": ["./data/**"], "/*": ["./y"], "/api": ["./x"] },
        });
        expect(config.reactStrictMode).toBe(true);
        expect(config.serverExternalPackages).toEqual(["sharp", "@runwisp/js"]);
        expect(config.outputFileTracingIncludes).toEqual({
            "**": [
                "./data/**",
                expect.stringMatching(/^[^/].*\/runwisp\/bin\/runwisp$/),
                expect.stringMatching(
                    new RegExp(`^[^/].*/@runwisp/${process.platform}-${process.arch}/runwisp$`),
                ),
            ],
            "/*": ["./y"],
            "/api": ["./x"],
        });
    });
});

describe("withRunWisp on Vercel", () => {
    afterEach(() => {
        vi.unstubAllEnvs();
    });

    it("fails the build instead of scheduling tasks that never run", () => {
        vi.stubEnv("VERCEL", "1");
        expect(() => withRunWisp({})).toThrow(/Vercel/);
    });
});

describe("scheduleVercelCrons", () => {
    afterEach(() => {
        vi.unstubAllGlobals();
        vi.unstubAllEnvs();
    });

    it("requests each cron path like Vercel does, failing the run on an error status", async () => {
        const runwisp = new RunWisp({ enabled: false, log: false, binary: "/nonexistent/runwisp" });
        scheduleVercelCrons(
            runwisp,
            {
                crons: [
                    { path: "/api/cron/cleanup", schedule: "0 * * * *" },
                    { path: "/api/cron/report?full=1", schedule: "0 5 * * *" },
                ],
            },
            { baseUrl: "http://app.internal:8080" },
        );
        expect([...runwisp.getTasks().keys()]).toEqual([
            "api-cron-cleanup",
            "api-cron-report-full-1",
        ]);
        expect(runwisp.getTask("api-cron-cleanup")?.getPattern()).toBe("0 * * * *");

        vi.stubEnv("CRON_SECRET", "s3cret");
        const fetch = vi.fn<typeof globalThis.fetch>(() =>
            Promise.resolve(new Response("nope", { status: 500, statusText: "Server Error" })),
        );
        vi.stubGlobal("fetch", fetch);
        await expect(runwisp.getTask("api-cron-report-full-1")?.execute()).rejects.toThrow(
            "GET /api/cron/report?full=1: 500 Server Error",
        );
        const [url, init] = fetch.mock.calls[0] ?? [];
        expect(url).toEqual(new URL("http://app.internal:8080/api/cron/report?full=1"));
        expect(init?.headers).toEqual({
            "user-agent": "vercel-cron/1.0",
            authorization: "Bearer s3cret",
        });
    });
});
