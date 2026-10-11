// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { CronAnswers } from "./cron.ts";

const FIRST = Date.parse("2026-01-02T03:01:00Z");
const MINUTE = 60_000;

/** A runwisp binary that answers every `sdk-cron` question with 60 minutely firings from 03:01Z, logging each call. */
function fakeCron() {
    const dir = mkdtempSync(join(tmpdir(), "rwn-cron-"));
    const next = Array.from({ length: 60 }, (_, i) => new Date(FIRST + i * MINUTE).toISOString());
    writeFileSync(
        join(dir, "answer.json"),
        JSON.stringify({
            valid: true,
            fields: {
                second: [0],
                minute: [1, 2],
                hour: [3],
                dayOfMonth: [],
                month: [],
                dayOfWeek: [],
            },
            errors: [],
            next,
        }),
    );
    const binary = join(dir, "runwisp");
    writeFileSync(binary, `#!/bin/sh\necho "$@" >> "${dir}/calls"\ncat "${dir}/answer.json"\n`);
    chmodSync(binary, 0o755);
    const calls = () => {
        try {
            return readFileSync(join(dir, "calls"), "utf8").trim().split("\n");
        } catch {
            return [];
        }
    };
    return { dir, answers: new CronAnswers(binary), calls };
}

describe("CronAnswers", () => {
    const dirs: string[] = [];
    afterEach(() => {
        for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
    });
    const setup = () => {
        const fake = fakeCron();
        dirs.push(fake.dir);
        return fake;
    };

    it("asks once per expression, and hands out copies", () => {
        const { answers, calls } = setup();
        const first = answers.explain("1-2 3 * * *");
        first.fields?.minute.push(99);
        expect(answers.explain("1-2 3 * * *").fields?.minute).toEqual([1, 2]);
        expect(calls()).toEqual(["sdk-cron --json --count 0 -- 1-2 3 * * *"]);
    });

    it("answers next firings from one batch until it runs short", () => {
        const { answers, calls } = setup();
        const now = new Date(FIRST - 30_000);
        expect(answers.next("* * * * *", "Europe/Bratislava", 1, now)).toEqual([new Date(FIRST)]);
        expect(calls()).toEqual([
            "sdk-cron --json --count 60 --after 2026-01-02T03:00:30.000Z --timezone Europe/Bratislava -- * * * * *",
        ]);
        const later = new Date(FIRST + 10 * MINUTE + 1);
        expect(answers.next("* * * * *", "Europe/Bratislava", 2, later)).toEqual([
            new Date(FIRST + 11 * MINUTE),
            new Date(FIRST + 12 * MINUTE),
        ]);
        expect(calls()).toHaveLength(1);
        // Another zone is another schedule; a batch with too few left is asked again.
        answers.next("* * * * *", undefined, 1, now);
        answers.next("* * * * *", "Europe/Bratislava", 2, new Date(FIRST + 58 * MINUTE + 1));
        expect(calls()).toHaveLength(3);
        expect(calls()[1]).toBe(
            "sdk-cron --json --count 60 --after 2026-01-02T03:00:30.000Z -- * * * * *",
        );
    });

    it("asks again for a time before the batch", () => {
        const { answers, calls } = setup();
        answers.next("* * * * *", undefined, 1, new Date(FIRST));
        answers.next("* * * * *", undefined, 1, new Date(FIRST - MINUTE));
        expect(calls()).toHaveLength(2);
    });

    it("matches within the batch by the second, and asks outside it", () => {
        const { answers, calls } = setup();
        answers.next("* * * * *", undefined, 1, new Date(FIRST - 1000));
        expect(answers.match("* * * * *", undefined, new Date(FIRST + 5 * MINUTE + 999))).toBe(
            true,
        );
        expect(answers.match("* * * * *", undefined, new Date(FIRST + 5 * MINUTE + 1000))).toBe(
            false,
        );
        expect(calls()).toHaveLength(1);
        // FIRST - 1000 itself is not in the batch: it holds firings after it.
        expect(answers.match("* * * * *", undefined, new Date(FIRST - 1000))).toBe(false);
        expect(calls()[1]).toBe(
            "sdk-cron --json --count 1 --after 2026-01-02T03:00:58.000Z -- * * * * *",
        );
    });
});
