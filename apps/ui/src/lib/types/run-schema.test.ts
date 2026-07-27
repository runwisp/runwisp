// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { runUpdateEventSchema } from "./index";

describe("runUpdateEventSchema", () => {
    it("preserves per-run params on a run.created event", () => {
        const envelope = {
            type: "run.created",
            timestamp: "2026-05-05T12:00:00.000Z",
            data: {
                run: {
                    id: "01J0000000000000000000000",
                    task_name: "backup-db",
                    status: "running",
                    exit_code: 0,
                    triggered_by: "api",
                    created_at: "2026-05-05T12:00:00.000Z",
                    retry_attempt: 0,
                    params: { TARGET: "prod", DRY_RUN: "false" },
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
