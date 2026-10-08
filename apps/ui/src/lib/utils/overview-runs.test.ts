// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Run } from "@runwisp/common";
import { mergeRuns } from "./overview-runs";

function makeRun(id: string, overrides: Partial<Run> = {}): Run {
    return {
        id,
        taskName: "backup-db",
        createdAt: "2026-06-22T12:00:00.000Z",
        status: "ended",
        endReason: "succeeded",
        triggeredBy: "api",
        exitCode: 0,
        instanceIndex: 0,
        retryAttempt: 0,
        isFailure: false,
        ...overrides,
    };
}

const isRunning = (run: Run) => run.status === "running";

describe("mergeRuns", () => {
    // An SSE event advanced a run to "ended"; a snapshot fetched
    // before that landed still carries it as "running". Merging the snapshot
    // must not revert the run's phase.
    it("does not regress an SSE-advanced run to an older phase", () => {
        const live = [makeRun("r1", { status: "ended", endReason: "succeeded" })];
        const snapshot = [makeRun("r1", { status: "running" })];

        const merged = mergeRuns(live, snapshot, 16);
        expect(merged).toHaveLength(1);
        expect(merged[0]?.status).toBe("ended");
    });

    it("returns newest first, capped at the limit", () => {
        const snapshot = [
            makeRun("a", { createdAt: "2026-06-22T12:00:00.000Z" }),
            makeRun("c", { createdAt: "2026-06-22T14:00:00.000Z" }),
            makeRun("b", { createdAt: "2026-06-22T13:00:00.000Z" }),
        ];
        expect(mergeRuns([], snapshot, 16).map((r) => r.id)).toEqual(["c", "b", "a"]);
        expect(mergeRuns([], snapshot, 2).map((r) => r.id)).toEqual(["c", "b"]);
    });

    it("updates an existing run in place when its status advances", () => {
        const existing = makeRun("a", { status: "running" });
        const advanced = makeRun("a", { status: "ended", endReason: "succeeded" });

        const result = mergeRuns([existing], [advanced], 16);

        expect(result).toHaveLength(1);
        expect(result[0]?.status).toBe("ended");
        expect(result[0]?.endReason).toBe("succeeded");
    });

    it("rejects a status regression (stale update arriving after a later phase)", () => {
        const existing = makeRun("a", { status: "running" });
        const stale = makeRun("a", { status: "pending" });

        expect(mergeRuns([existing], [stale], 16)[0]?.status).toBe("running");
    });

    it("drops a stale snapshot's running copy of a finished run under keep", () => {
        const live = [makeRun("r1", { status: "ended", endReason: "succeeded" })];
        const snapshot = [makeRun("r1", { status: "running" })];

        expect(mergeRuns(live, snapshot, 8, isRunning)).toHaveLength(0);
    });

    it("keeps genuinely running runs under keep", () => {
        const snapshot = [makeRun("r1", { status: "running" })];
        expect(mergeRuns([], snapshot, 8, isRunning).map((r) => r.id)).toEqual(["r1"]);
    });

    it("does not mutate the input list", () => {
        const live = [
            makeRun("a", { createdAt: "2026-06-22T12:00:00.000Z" }),
            makeRun("b", { createdAt: "2026-06-22T13:00:00.000Z" }),
        ];
        mergeRuns(live, [], 16);
        expect(live.map((r) => r.id)).toEqual(["a", "b"]);
    });
});
