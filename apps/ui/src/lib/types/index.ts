// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { z } from "zod";
import {
    RUN_PHASES,
    END_REASONS,
    TRIGGERS,
    type Run,
    type AuthChallengeBody,
    type AuthStatusBody,
} from "@runwisp/common";
import { safeParseJSON } from "$lib/utils/parse";

export interface AuthState {
    required: boolean;
    loaded: boolean;
    authenticated: boolean;
}

// The auth endpoints use raw fetch (see api.ts) so a wrong-password 401 doesn't
// trip the shared client's "auth required" middleware; each untyped response is
// validated and piped to its server-generated type.
export const authChallengeResponseSchema = z
    .object({ nonce: z.string() })
    .pipe(z.custom<AuthChallengeBody>());

export const authStatusResponseSchema = z
    .object({ authRequired: z.boolean(), authenticated: z.boolean() })
    .pipe(z.custom<AuthStatusBody>());

const runPhaseSchema = z.enum(RUN_PHASES);
const endReasonSchema = z.enum(END_REASONS);

const runSchema = z
    .object({
        id: z.string(),
        executionId: z.string().optional(),
        taskName: z.string(),
        status: runPhaseSchema,
        endReason: endReasonSchema.optional(),
        exitCode: z.number(),
        startedAt: z.string().optional(),
        endedAt: z.string().optional(),
        triggeredBy: z.enum(TRIGGERS),
        createdAt: z.string(),
        retryAttempt: z.number(),
        retryOfRunId: z.string().optional(),
        params: z.record(z.string(), z.string()).optional(),
        isFailure: z.boolean(),
        instanceIndex: z.number().int(),
        peakMemoryBytes: z.number().optional(),
        cpuTimeMs: z.number().optional(),
    })
    .pipe(z.custom<Run>());

const RUN_MUTATION_EVENT_TYPES = [
    "run.created",
    "run.started",
    "run.completed",
    "run.failed",
    "run.updated",
] as const;

export const RUN_EVENT_TYPES = [...RUN_MUTATION_EVENT_TYPES, "run.deleted"] as const;

const runMutationPayloadSchema = z.object({ run: runSchema });
const runDeletedPayloadSchema = z.object({ runId: z.string(), taskName: z.string() });

export type RunUpdateEvent =
    | {
          type: (typeof RUN_MUTATION_EVENT_TYPES)[number];
          data: z.infer<typeof runMutationPayloadSchema>;
      }
    | { type: "run.deleted"; data: z.infer<typeof runDeletedPayloadSchema> };
export type RunUpdateEventType = RunUpdateEvent["type"];
export type RunUpdateHandler = (event: RunUpdateEvent) => void;

/** Validate a run event's SSE payload against the schema for its type. */
export function parseRunUpdate(
    type: RunUpdateEventType,
    data: string,
): { success: true; data: RunUpdateEvent } | { success: false; error: unknown } {
    if (type === "run.deleted") {
        const result = safeParseJSON(data, runDeletedPayloadSchema);
        return result.success ? { success: true, data: { type, data: result.data } } : result;
    }
    const result = safeParseJSON(data, runMutationPayloadSchema);
    return result.success ? { success: true, data: { type, data: result.data } } : result;
}
