// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { setTimeout as sleep } from "node:timers/promises";

import { describe, expect, it, vi } from "vitest";

import { Capture, MAX_PIECE, runCaptured } from "./capture.ts";
import type { Stream } from "./session.ts";

describe("runCaptured", () => {
    it("keeps each run's console output in its own sink across awaits", async () => {
        const a: [Stream, string][] = [];
        const b: [Stream, string][] = [];
        await Promise.all([
            runCaptured(
                (s, l) => a.push([s, l]),
                async () => {
                    console.log("a1");
                    await sleep(10);
                    console.error("a2\na3");
                },
            ),
            runCaptured(
                (s, l) => b.push([s, l]),
                async () => {
                    await sleep(5);
                    console.info("b1 %d", 42);
                },
            ),
        ]);
        console.log("outside any run");
        expect(a).toEqual([
            ["stdout", "a1"],
            ["stderr", "a2"],
            ["stderr", "a3"],
        ]);
        expect(b).toEqual([["stdout", "b1 42"]]);
    });

    it("captures direct stream writes, joining partial lines and flushing the last one", async () => {
        const lines: [Stream, string][] = [];
        await runCaptured(
            (s, l, partial) => {
                expect(partial).toBe(false);
                lines.push([s, l]);
            },
            async () => {
                process.stdout.write("[Nest] ");
                process.stdout.write(Buffer.from("started\nnext "));
                await sleep(1);
                process.stderr.write("oops\n");
                console.log("via console");
                process.stdout.write("no newline");
            },
        );
        expect(lines).toEqual([
            ["stdout", "[Nest] started"],
            ["stderr", "oops"],
            ["stdout", "next via console"],
            ["stdout", "no newline"],
        ]);
    });

    // One message must stay far below the daemon's 16 MiB line cap; \r
    // progress output never ends a line at all.
    it("sends a long or never-ending line in pieces the next one continues", () => {
        const pieces: [string, boolean][] = [];
        const capture = new Capture((_s, line, partial) => pieces.push([line, partial]));
        capture.push("stdout", `${"x".repeat(MAX_PIECE * 2 + 5)}\n`);
        for (let i = 0; i < 2000; i++) capture.push("stdout", `${"#".repeat(40)}\r`);
        capture.flush();
        expect(pieces.slice(0, 3)).toEqual([
            ["x".repeat(MAX_PIECE), true],
            ["x".repeat(MAX_PIECE), true],
            ["xxxxx", false],
        ]);
        const progress = pieces.slice(3);
        expect(progress.every(([line]) => line.length <= MAX_PIECE)).toBe(true);
        expect(progress.map(([line]) => line).join("")).toBe(`${"#".repeat(40)}\r`.repeat(2000));
        expect(progress.at(-1)?.[1]).toBe(false);
    });

    it("never splits a surrogate pair between pieces", () => {
        const pieces: string[] = [];
        const capture = new Capture((_s, line) => pieces.push(line));
        capture.push("stdout", `${"x".repeat(MAX_PIECE - 1)}😀\n`);
        expect(pieces).toEqual(["x".repeat(MAX_PIECE - 1), "😀"]);
    });

    // A bundler that duplicates the package, or ESM next to CJS, loads it twice.
    it("captures output for a second copy of the package in the process", async () => {
        await runCaptured(
            () => undefined,
            () => Promise.resolve(),
        ); // this copy patches console
        vi.resetModules();
        const copy = await import("./capture.ts");
        expect(copy.runCaptured).not.toBe(runCaptured);
        const lines: string[] = [];
        await copy.runCaptured(
            (_s, line) => lines.push(line),
            () => {
                console.log("from the second copy");
                return Promise.resolve();
            },
        );
        expect(lines).toEqual(["from the second copy"]);
    });
});
