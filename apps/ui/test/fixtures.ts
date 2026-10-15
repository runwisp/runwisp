// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Run } from "@runwisp/common";

// makeRun is a finished, successful run of "backup-db" for unit tests.
export function makeRun(id: string, overrides: Partial<Run> = {}): Run {
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
