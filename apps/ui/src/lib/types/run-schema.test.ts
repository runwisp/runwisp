// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { runUpdateEventSchema, type Run } from "./index";

describe("runUpdateEventSchema", () => {
    // runSchema (module-private) is piped through z.custom<Run>() rather than
    // typed as z.ZodType<Run> — Zod's `.optional()` types a field as
    // `T | undefined`, which this project's exactOptionalPropertyTypes then
    // rejects against Run's `field?: T` — so z.custom sidesteps a mismatch
    // that isn't real drift. That means TypeScript won't catch runSchema
    // falling behind the generated Run type on its own: this fixture is typed
    // as Run with every optional field populated, so a field added to Run
    // without a matching runSchema update fails to compile here, and parsing
    // it confirms the schema still accepts the full shape at runtime.
    it("accepts every field the generated Run type declares", () => {
        const run: Run = {
            id: "01J0000000000000000000000",
            executionId: "exec-1",
            taskName: "backup-db",
            status: "running",
            endReason: "succeeded",
            exitCode: 0,
            startedAt: "2026-05-05T12:00:00.000Z",
            endedAt: "2026-05-05T12:00:01.000Z",
            triggeredBy: "api",
            createdAt: "2026-05-05T12:00:00.000Z",
            retryAttempt: 0,
            retryOfRunId: "01J0000000000000000000001",
            params: { TARGET: "prod" },
            isFailure: false,
            instanceIndex: 0,
        };

        const result = runUpdateEventSchema.safeParse({
            type: "run.created",
            timestamp: "2026-05-05T12:00:00.000Z",
            data: { run },
        });

        expect(result.success).toBe(true);
    });

    it("preserves per-run params on a run.created event", () => {
        const envelope = {
            type: "run.created",
            timestamp: "2026-05-05T12:00:00.000Z",
            data: {
                run: {
                    id: "01J0000000000000000000000",
                    taskName: "backup-db",
                    status: "running",
                    exitCode: 0,
                    triggeredBy: "api",
                    createdAt: "2026-05-05T12:00:00.000Z",
                    retryAttempt: 0,
                    params: { TARGET: "prod", DRY_RUN: "false" },
                    isFailure: false,
                    instanceIndex: 0,
                },
            },
        };

        const result = runUpdateEventSchema.safeParse(envelope);

        expect(result.success).toBe(true);
        if (!result.success) return;
        if (result.data.type === "run.deleted") throw new Error("unexpected deleted event");
        expect(result.data.data.run.params).toEqual({ TARGET: "prod", DRY_RUN: "false" });
    });
});
