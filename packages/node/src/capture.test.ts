// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { setTimeout as sleep } from "node:timers/promises";

import { describe, expect, it } from "vitest";

import { runCaptured } from "./capture.ts";
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
});
