// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { z } from "zod";
import {
    RUN_PHASES,
    END_REASONS,
    TRIGGERS,
    type Run as CommonRun,
    type Task as CommonTask,
} from "@runwisp/common";

export type Task = CommonTask;
export type Run = CommonRun;

export interface AuthState {
    required: boolean;
    loaded: boolean;
    authenticated: boolean;
}

export const authChallengeResponseSchema = z.object({ nonce: z.string() });
export type AuthChallengeResponse = z.infer<typeof authChallengeResponseSchema>;

export const authStatusResponseSchema = z.object({
    auth_required: z.boolean(),
    authenticated: z.boolean(),
});
export type AuthStatusResponse = z.infer<typeof authStatusResponseSchema>;

export const authLoginResponseSchema = z.object({ token: z.string() });
export type AuthLoginResponse = z.infer<typeof authLoginResponseSchema>;

const runPhaseSchema = z.enum(RUN_PHASES);
const endReasonSchema = z.enum(END_REASONS);

const runSchema = z
    .object({
        id: z.string(),
        external_execution_id: z.string().optional(),
        task_name: z.string(),
        status: runPhaseSchema,
        end_reason: endReasonSchema.optional(),
        exit_code: z.number(),
        start_at: z.string().optional(),
        end_at: z.string().optional(),
        triggered_by: z.enum(TRIGGERS),
        created_at: z.string(),
        retry_attempt: z.number(),
        retry_of_run_id: z.string().optional(),
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
        run_id: z.string(),
        task_name: z.string(),
    }),
});

export const runUpdateEventSchema = z.union([runMutationEventSchema, runDeletedEventSchema]);

export type RunMutationEvent = z.infer<typeof runMutationEventSchema>;
export type RunDeletedEvent = z.infer<typeof runDeletedEventSchema>;
export type RunUpdateEvent = z.infer<typeof runUpdateEventSchema>;
export type RunUpdateEventType = RunUpdateEvent["type"];
export type RunUpdateHandler = (event: RunUpdateEvent) => void;
