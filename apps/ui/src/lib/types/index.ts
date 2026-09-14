// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { z } from "zod";
import {
    RUN_PHASES,
    END_REASONS,
    TRIGGERS,
    type Run as CommonRun,
    type Task as CommonTask,
    type AuthChallengeBody,
    type AuthStatusBody,
    type AuthLoginBody,
} from "@runwisp/common";

export type Task = CommonTask;
export type Run = CommonRun;

export interface AuthState {
    required: boolean;
    loaded: boolean;
    authenticated: boolean;
}

// The auth endpoints are called via raw fetch (see api.ts) rather than the
// shared openapi-fetch client, so a wrong-password 401 doesn't trip the
// client's global "auth required" interceptor. That means their responses
// arrive untyped, so each is validated against its shape and piped to the
// server-generated type (not a hand-maintained duplicate) as the source of
// truth.
export const authChallengeResponseSchema = z
    .object({ nonce: z.string() })
    .pipe(z.custom<AuthChallengeBody>());
export type AuthChallengeResponse = AuthChallengeBody;

export const authStatusResponseSchema = z
    .object({ authRequired: z.boolean(), authenticated: z.boolean() })
    .pipe(z.custom<AuthStatusBody>());
export type AuthStatusResponse = AuthStatusBody;

export const authLoginResponseSchema = z
    .object({ token: z.string() })
    .pipe(z.custom<AuthLoginBody>());
export type AuthLoginResponse = AuthLoginBody;

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
    })
    .pipe(z.custom<Run>());

const runMutationEventSchema = z.object({
    type: z.enum(["run.created", "run.started", "run.completed", "run.failed", "run.updated"]),
    timestamp: z.string(),
    data: z.object({
        run: runSchema,
        error: z.string().optional(),
    }),
});

const runDeletedEventSchema = z.object({
    type: z.literal("run.deleted"),
    timestamp: z.string(),
    data: z.object({
        runId: z.string(),
        taskName: z.string(),
    }),
});

export const runUpdateEventSchema = z.union([runMutationEventSchema, runDeletedEventSchema]);

export type RunMutationEvent = z.infer<typeof runMutationEventSchema>;
export type RunDeletedEvent = z.infer<typeof runDeletedEventSchema>;
export type RunUpdateEvent = z.infer<typeof runUpdateEventSchema>;
export type RunUpdateEventType = RunUpdateEvent["type"];
export type RunUpdateHandler = (event: RunUpdateEvent) => void;
