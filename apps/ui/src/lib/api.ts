// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import createClient, { type Middleware } from "openapi-fetch";
import { z } from "zod";
import type { APIPaths, APIOperations, RunSelector } from "@runwisp/common";
import { browser } from "$app/environment";
import { getApiUrl } from "./utils/env";
import { chapResponse } from "./chap";
import { HTTP_STATUS } from "./config/constants";
import { browserAuthEventBus } from "$lib/adapters/browser";
import { authStore } from "./stores/auth.svelte";
import {
    logPageSchema,
    type LogPage,
    logSearchResponseSchema,
    type LogSearchResponse,
    logLineHistorySchema,
} from "./logs";
import {
    authChallengeResponseSchema,
    authLoginResponseSchema,
    authStatusResponseSchema,
    type AuthLoginResponse,
    type AuthStatusResponse,
} from "./types";

export * from "./types";

const API_BASE_URL = getApiUrl();

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
// browser attaches automatically to same-origin requests — there is no
// JS-readable token to set as a Bearer header. This middleware reacts to a 401
// by driving the login modal, and throws on any other non-ok response so every
// generated-client call rejects on failure instead of resolving with `{error}`
// — callers never have to check `error` themselves.
const errorMiddleware: Middleware = {
    onResponse({ response }) {
        if (response.ok) return response;
        if (response.status === HTTP_STATUS.UNAUTHORIZED && browser) {
            authStore.markUnauthenticated();
            browserAuthEventBus.emitAuthRequired();
            throw new AuthRequiredError();
        }
        throw new Error(`Request failed: ${String(response.status)} ${response.statusText}`);
    },
};

const apiClient = createClient<APIPaths>({ baseUrl: API_BASE_URL });
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

// The one params shape shared by /api/runs and /api/tasks/{taskName}/runs —
// sourced from the generated client so it can never drift from what the
// server actually accepts (it used to be two hand-written copies, missing
// the server's `isFailure` filter).
type RunsQueryParams = NonNullable<APIOperations["listRuns"]["parameters"]["query"]>;

export const authApi = {
    login: async (password: string): Promise<AuthLoginResponse> => {
        const challengeRes = await fetch(`${API_BASE_URL}/api/auth/challenge`);
        if (challengeRes.status === HTTP_STATUS.TOO_MANY_REQUESTS) throw new RateLimitedError();
        if (!challengeRes.ok) throw new Error("Failed to get auth challenge");
        const { nonce } = authChallengeResponseSchema.parse(await challengeRes.json());

        const response = await chapResponse(password, nonce);

        const res = await fetch(`${API_BASE_URL}/api/auth/login`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ nonce, response }),
        });
        if (res.status === HTTP_STATUS.TOO_MANY_REQUESTS) throw new RateLimitedError();
        if (!res.ok) throw new Error("Authentication failed");

        return authLoginResponseSchema.parse(await res.json());
    },

    status: async (): Promise<AuthStatusResponse> => {
        const res = await fetch(`${API_BASE_URL}/api/auth/status`);
        if (!res.ok) throw new Error("Failed to check auth status");
        return authStatusResponseSchema.parse(await res.json());
    },
};

export const tasksApi = {
    getAll: async () => {
        const { data } = await apiClient.GET("/api/tasks");
        return unwrap(data).items ?? [];
    },

    getRuns: async (taskName: string, params?: RunsQueryParams) => {
        const { data } = await apiClient.GET("/api/runs", {
            params: { query: { taskName, ...params } },
        });
        const runs = unwrap(data);
        return { runs: runs.items ?? [], total: runs.total };
    },

    triggerRun: async (taskName: string, params?: Record<string, string | null>) => {
        const { data } = await apiClient.POST("/api/tasks/{taskName}/run", {
            params: { path: { taskName }, query: { via: "ui" } },
            ...(params && Object.keys(params).length > 0 ? { body: { params } } : {}),
        });
        return unwrap(data);
    },

    restartService: async (taskName: string): Promise<void> => {
        await apiClient.POST("/api/tasks/{taskName}/restart", {
            params: { path: { taskName } },
        });
    },

    stopService: async (taskName: string): Promise<void> => {
        await apiClient.POST("/api/tasks/{taskName}/stop", {
            params: { path: { taskName } },
        });
    },

    getRun: async (_taskName: string, runId: string) => {
        const { data } = await apiClient.GET("/api/runs/{runId}", {
            params: { path: { runId } },
        });
        return unwrap(data);
    },

    deleteRun: async (runId: string): Promise<void> => {
        await apiClient.DELETE("/api/runs/{runId}", {
            params: { path: { runId } },
        });
    },

    stopRun: async (runId: string): Promise<void> => {
        await apiClient.POST("/api/runs/{runId}/stop", {
            params: { path: { runId } },
        });
    },

    getLogPage: async (
        runId: string,
        options?: { from?: number; limit?: number },
    ): Promise<LogPage> => {
        const params = new URLSearchParams();
        if (options?.from !== undefined) params.set("from", String(options.from));
        if (options?.limit !== undefined) params.set("limit", String(options.limit));
        const qs = params.toString();
        const url =
            API_BASE_URL + "/api/runs/" + encodeURIComponent(runId) + "/log" + (qs ? "?" + qs : "");

        const response = await fetch(url, { headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error("Log page fetch failed: " + String(response.status));
        return logPageSchema.parse(await response.json());
    },

    searchLogs: async (
        taskName: string,
        options: {
            q: string;
            regex: boolean;
            case: boolean;
            runId?: string;
            limit?: number;
            cursor?: string;
        },
    ): Promise<LogSearchResponse> => {
        const params = new URLSearchParams();
        params.set("q", options.q);
        if (options.regex) params.set("regex", "true");
        if (options.case) params.set("case", "true");
        if (options.runId) params.set("runId", options.runId);
        if (options.limit !== undefined) params.set("limit", String(options.limit));
        if (options.cursor) params.set("cursor", options.cursor);

        const url =
            API_BASE_URL +
            "/api/tasks/" +
            encodeURIComponent(taskName) +
            "/log/search?" +
            params.toString();

        const response = await fetch(url, { headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error("Log search failed: " + String(response.status));
        return logSearchResponseSchema.parse(await response.json());
    },

    getLogLineHistory: async (runId: string, lineNum: number): Promise<string[][]> => {
        const url =
            API_BASE_URL +
            "/api/runs/" +
            encodeURIComponent(runId) +
            "/log/line/" +
            String(lineNum) +
            "/history";

        const response = await fetch(url, { headers: { Accept: "application/json" } });
        if (!response.ok)
            throw new Error("Log line history fetch failed: " + String(response.status));
        return logLineHistorySchema.parse(await response.json()).frames;
    },

    getLogRaw: async (runId: string): Promise<string> => {
        const url = API_BASE_URL + "/api/runs/" + encodeURIComponent(runId) + "/log/raw";

        const response = await fetch(url);
        if (!response.ok) throw new Error("Raw log fetch failed: " + String(response.status));
        return await response.text();
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
export const systemEventSchema = z.object({
    sample: metricsSampleSchema,
    uptime: z.string(),
});

export const configStaleEventSchema = z.object({
    stale: z.boolean(),
});
