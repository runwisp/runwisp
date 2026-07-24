// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import type { Run } from "@runwisp/common";
import { mergeRecentRuns, mergeRunningRuns } from "./overview-runs";

function makeRun(id: string, overrides: Partial<Run> = {}): Run {
    return {
        id,
        task_name: "backup-db",
        created_at: "2026-06-22T12:00:00.000Z",
        status: "ended",
        end_reason: "success",
        triggered_by: "api",
        exit_code: 0,
        instance_index: 0,
        retry_attempt: 0,
        ...overrides,
    };
}

describe("mergeRecentRuns", () => {
    // Guards M6: an SSE event advanced a run to "ended"; a snapshot fetched
    // before that landed still carries it as "running". Merging the snapshot
    // must not revert the run's phase.
    it("does not regress an SSE-advanced run to an older phase", () => {
        const live = [makeRun("r1", { status: "ended", end_reason: "success" })];
        const snapshot = [makeRun("r1", { status: "running", end_reason: undefined })];

        const merged = mergeRecentRuns(live, snapshot, 16);
        expect(merged).toHaveLength(1);
        expect(merged[0]?.status).toBe("ended");
    });

    it("seeds from a snapshot when the live list is empty", () => {
        const snapshot = [
            makeRun("a", { created_at: "2026-06-22T12:00:00.000Z" }),
            makeRun("b", { created_at: "2026-06-22T13:00:00.000Z" }),
        ];
        const merged = mergeRecentRuns([], snapshot, 16);
        // Newest first.
        expect(merged.map((r) => r.id)).toEqual(["b", "a"]);
    });
});

describe("mergeRunningRuns", () => {
    it("drops a stale snapshot's running copy of a finished run", () => {
        const live = [makeRun("r1", { status: "ended", end_reason: "success" })];
        const snapshot = [makeRun("r1", { status: "running", end_reason: undefined })];

        const merged = mergeRunningRuns(live, snapshot, 8);
        expect(merged).toHaveLength(0);
    });

    it("keeps genuinely running runs", () => {
        const live: Run[] = [];
        const snapshot = [makeRun("r1", { status: "running", end_reason: undefined })];
        const merged = mergeRunningRuns(live, snapshot, 8);
        expect(merged.map((r) => r.id)).toEqual(["r1"]);
    });
});
