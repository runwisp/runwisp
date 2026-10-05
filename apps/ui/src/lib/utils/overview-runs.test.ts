// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Run } from "@runwisp/common";
import { mergeRecentRuns, mergeRunningRuns, upsertRun } from "./overview-runs";

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

describe("mergeRecentRuns", () => {
    // An SSE event advanced a run to "ended"; a snapshot fetched
    // before that landed still carries it as "running". Merging the snapshot
    // must not revert the run's phase.
    it("does not regress an SSE-advanced run to an older phase", () => {
        const live = [makeRun("r1", { status: "ended", endReason: "succeeded" })];
        const snapshot = [makeRun("r1", { status: "running" })];

        const merged = mergeRecentRuns(live, snapshot, 16);
        expect(merged).toHaveLength(1);
        expect(merged[0]?.status).toBe("ended");
    });

    it("seeds from a snapshot when the live list is empty", () => {
        const snapshot = [
            makeRun("a", { createdAt: "2026-06-22T12:00:00.000Z" }),
            makeRun("b", { createdAt: "2026-06-22T13:00:00.000Z" }),
        ];
        const merged = mergeRecentRuns([], snapshot, 16);
        // Newest first.
        expect(merged.map((r) => r.id)).toEqual(["b", "a"]);
    });
});

describe("mergeRunningRuns", () => {
    it("drops a stale snapshot's running copy of a finished run", () => {
        const live = [makeRun("r1", { status: "ended", endReason: "succeeded" })];
        const snapshot = [makeRun("r1", { status: "running" })];

        const merged = mergeRunningRuns(live, snapshot, 8);
        expect(merged).toHaveLength(0);
    });

    it("keeps genuinely running runs", () => {
        const live: Run[] = [];
        const snapshot = [makeRun("r1", { status: "running" })];
        const merged = mergeRunningRuns(live, snapshot, 8);
        expect(merged.map((r) => r.id)).toEqual(["r1"]);
    });
});

describe("upsertRun", () => {
    it("inserts a new run at the requested end", () => {
        const a = makeRun("a");
        const b = makeRun("b");

        expect(upsertRun([a], b, "start").map((r) => r.id)).toEqual(["b", "a"]);
        expect(upsertRun([a], b, "end").map((r) => r.id)).toEqual(["a", "b"]);
    });

    it("updates an existing run in place when its status advances", () => {
        const existing = makeRun("a", { status: "running" });
        const advanced = makeRun("a", { status: "ended", endReason: "succeeded" });

        const result = upsertRun([existing], advanced, "start");

        expect(result).toHaveLength(1);
        expect(result[0]?.status).toBe("ended");
        expect(result[0]?.endReason).toBe("succeeded");
    });

    it("rejects a status regression (stale update arriving after a later phase)", () => {
        const existing = makeRun("a", { status: "running" });
        const stale = makeRun("a", { status: "pending" });

        const result = upsertRun([existing], stale, "start");

        expect(result[0]?.status).toBe("running");
    });
});
