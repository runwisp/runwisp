// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import createClient, { type Middleware } from "openapi-fetch";
import { z } from "zod";
import type { APIPaths, APIOperations, AuthStatusBody, RunSelector } from "@runwisp/common";
import { chapResponse } from "./chap";
import { HTTP_STATUS } from "./config/constants";
import { handleUnauthorized } from "./utils/auth-required";
import type { LogPage } from "./logs";
import { authChallengeResponseSchema, authStatusResponseSchema } from "./types";
import { isRecord } from "./utils/parse";

export class AuthRequiredError extends Error {
    constructor() {
        super("Authentication required");
        this.name = "AuthRequiredError";
    }
}

// Raised when the daemon's login rate limiter (shared by /challenge and /auth)
// returns 429. Distinct from a bad password so the UI can tell the operator to
// wait rather than mislabel a throttled-but-correct password as "invalid".
export class RateLimitedError extends Error {
    constructor() {
        super("Too many attempts");
        this.name = "RateLimitedError";
    }
}

// The browser session is authenticated by the HttpOnly cookie, which the
// browser attaches automatically to same-origin requests, so there is no
// JS-readable token to set as a Bearer header. This middleware reacts to a 401
// by driving the login modal, and throws on any other non-ok response so every
// generated-client call rejects on failure instead of resolving with `{error}`.
// The thrown message is the server's huma `detail` when it sent one, so an
// operator-actionable reason (a rejected reload, a refused trigger) reaches the
// toast instead of a bare status line.
const errorMiddleware: Middleware = {
    async onResponse({ response }) {
        if (response.ok) return response;
        if (response.status === HTTP_STATUS.UNAUTHORIZED) {
            handleUnauthorized();
            throw new AuthRequiredError();
        }
        throw await requestError(response);
    },
};

/** The Error for a failed (non-401) response: huma's `detail`, else the status line. */
export async function requestError(response: Response): Promise<Error> {
    try {
        const body: unknown = await response.clone().json();
        if (isRecord(body) && typeof body.detail === "string" && body.detail) {
            return new Error(body.detail);
        }
    } catch {
        // Not a JSON problem body; fall back to the status line.
    }
    return new Error(`Request failed: ${String(response.status)} ${response.statusText}`);
}

const apiClient = createClient<APIPaths>();
apiClient.use(errorMiddleware);

// Narrows a generated-client response's `data` (typed possibly-undefined
// because the schema allows empty bodies, e.g. 204) now that errorMiddleware
// guarantees any resolved call already succeeded. Response bodies here are
// always objects, never a meaningful falsy value, so `!data` is a safe presence
// check.
function unwrap<T extends object>(data: T | undefined): T {
    if (!data) throw new Error("Empty API response");
    return data;
}

// The /api/runs query shape, sourced from the generated client so it can never
// drift from what the server actually accepts.
type RunsQueryParams = NonNullable<APIOperations["listRuns"]["parameters"]["query"]>;
type LogPageQuery = NonNullable<APIOperations["getLogPage"]["parameters"]["query"]>;
type LogSearchQuery = NonNullable<APIOperations["searchLogs"]["parameters"]["query"]>;

// The bodiless per-task POST actions. errorMiddleware throws on any failure.
type TaskActionPath =
    | "/api/tasks/{taskName}/restart"
    | "/api/tasks/{taskName}/stop"
    | "/api/tasks/{taskName}/pause"
    | "/api/tasks/{taskName}/resume";

async function postTaskAction(path: TaskActionPath, taskName: string): Promise<void> {
    await apiClient.POST(path, { params: { path: { taskName } } });
}

export const authApi = {
    login: async (password: string): Promise<void> => {
        const challengeRes = await fetch("/api/auth/challenge");
        if (challengeRes.status === HTTP_STATUS.TOO_MANY_REQUESTS) throw new RateLimitedError();
        if (!challengeRes.ok) throw new Error("Failed to get auth challenge");
        const { nonce } = authChallengeResponseSchema.parse(await challengeRes.json());

        const response = await chapResponse(password, nonce);

        const res = await fetch("/api/auth/login", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ nonce, response }),
        });
        if (res.status === HTTP_STATUS.TOO_MANY_REQUESTS) throw new RateLimitedError();
        // The response sets the HttpOnly session cookie; the body isn't needed.
        if (!res.ok) throw new Error("Authentication failed");
    },

    status: async (): Promise<AuthStatusBody> => {
        const res = await fetch("/api/auth/status");
        if (!res.ok) throw new Error("Failed to check auth status");
        return authStatusResponseSchema.parse(await res.json());
    },
};

export const tasksApi = {
    getAll: async () => {
        const { data } = await apiClient.GET("/api/tasks");
        return unwrap(data).items ?? [];
    },

    triggerRun: async (taskName: string, params?: Record<string, string | null>) => {
        const { data } = await apiClient.POST("/api/tasks/{taskName}/run", {
            params: { path: { taskName }, query: { via: "ui" } },
            ...(params && Object.keys(params).length > 0 ? { body: { params } } : {}),
        });
        return unwrap(data);
    },

    restartService: (taskName: string) => postTaskAction("/api/tasks/{taskName}/restart", taskName),
    stopService: (taskName: string) => postTaskAction("/api/tasks/{taskName}/stop", taskName),
    pauseSchedule: (taskName: string) => postTaskAction("/api/tasks/{taskName}/pause", taskName),
    resumeSchedule: (taskName: string) => postTaskAction("/api/tasks/{taskName}/resume", taskName),

    stopRun: async (runId: string): Promise<void> => {
        await apiClient.POST("/api/runs/{runId}/stop", {
            params: { path: { runId } },
        });
    },

    getLogPage: async (runId: string, query: LogPageQuery = {}): Promise<LogPage> => {
        const { data } = await apiClient.GET("/api/runs/{runId}/log", {
            params: { path: { runId }, query },
        });
        return unwrap(data);
    },

    /** Matching lines across a task's runs, newest run first. */
    searchLogs: async (taskName: string, query: LogSearchQuery) => {
        const { data } = await apiClient.GET("/api/tasks/{taskName}/log/search", {
            params: { path: { taskName }, query },
        });
        return unwrap(data).items ?? [];
    },

    getLogLineHistory: async (runId: string, lineNumber: number): Promise<string[][]> => {
        const { data } = await apiClient.GET("/api/runs/{runId}/log/line/{lineNumber}/history", {
            params: { path: { runId, lineNumber } },
        });
        return (unwrap(data).frames ?? []).map((frame) => frame ?? []);
    },
};

export const runsApi = {
    getAll: async (params?: RunsQueryParams) => {
        const { data } = await apiClient.GET("/api/runs", {
            ...(params ? { params: { query: params } } : {}),
        });
        const runs = unwrap(data);
        return { runs: runs.items ?? [], total: runs.total };
    },

    // Fetch one run by its (globally unique) ULID — no task name needed. Lets
    // the cross-task /runs view restore a deep-linked run that isn't on the
    // currently loaded page.
    getById: async (runId: string) => {
        const { data } = await apiClient.GET("/api/runs/{runId}", {
            params: { path: { runId } },
        });
        return unwrap(data);
    },

    bulkDelete: async (selector: RunSelector): Promise<number> => {
        const { data } = await apiClient.POST("/api/runs/bulk/delete", {
            body: selector,
        });
        return unwrap(data).affected;
    },

    bulkRestore: async (selector: RunSelector): Promise<number> => {
        const { data } = await apiClient.POST("/api/runs/bulk/restore", {
            body: selector,
        });
        return unwrap(data).affected;
    },

    bulkCancel: async (selector: RunSelector): Promise<number> => {
        const { data } = await apiClient.POST("/api/runs/bulk/stop", {
            body: selector,
        });
        return unwrap(data).affected;
    },

    bulkRerun: async (
        selector: RunSelector,
    ): Promise<{ triggered: { taskName: string; runId: string }[] }> => {
        const { data } = await apiClient.POST("/api/runs/bulk/rerun", {
            body: selector,
        });
        return { triggered: unwrap(data).triggered ?? [] };
    },
};

export const systemApi = {
    getInfo: async () => {
        const { data } = await apiClient.GET("/api/daemon");
        return unwrap(data);
    },

    getStats: async () => {
        const { data } = await apiClient.GET("/api/system");
        return unwrap(data);
    },

    getMetricsHistory: async (): Promise<MetricsSample[]> => {
        const { data } = await apiClient.GET("/api/system/metrics");
        return unwrap(data).items ?? [];
    },

    // Reload re-reads runwisp.toml and reconciles the live task set. A rejected
    // reload rejects with the daemon's reason (see errorMiddleware).
    reload: async () => {
        const { data } = await apiClient.POST("/api/daemon/reload");
        return unwrap(data);
    },
};

const metricsSampleSchema = z.object({
    timestamp: z.number(),
    cpuUsage: z.number(),
    memUsage: z.number(),
    memUsed: z.number(),
    memTotal: z.number(),
});

export type MetricsSample = z.infer<typeof metricsSampleSchema>;

// Payloads pushed over the unified /api/events/stream feed (mirrors the server's
// SystemSampleSSEEvent / ConfigStaleSSEEvent), so dashboards never poll
// /api/system or /api/daemon on a timer.
const resourceUsageSchema = z.object({
    cpuPercent: z.number(),
    memoryBytes: z.number(),
});

export const systemEventSchema = z.object({
    sample: metricsSampleSchema,
    uptime: z.string(),
    tasks: z.record(z.string(), resourceUsageSchema).optional(),
    runs: z.record(z.string(), resourceUsageSchema).optional(),
});

export const configStaleEventSchema = z.object({
    stale: z.boolean(),
});
