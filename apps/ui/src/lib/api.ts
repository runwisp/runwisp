// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import createClient, { type Middleware } from "openapi-fetch";
import { z } from "zod";
import type { APIPaths, RunSelector } from "@runwisp/common";
import { browser } from "$app/environment";
import { getApiUrl } from "./utils/env";
import { chapResponse } from "./chap";
import { HTTP_STATUS } from "./config/constants";
import { browserTokenStorage, browserAuthEventBus } from "$lib/adapters/browser";
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

const authMiddleware: Middleware = {
    onRequest({ request }) {
        if (browser) {
            const token = browserTokenStorage.getToken();
            if (token) {
                request.headers.set("Authorization", `Bearer ${token}`);
            }
            // When no localStorage token exists, the browser still sends the
            // HttpOnly cookie automatically for same-origin requests.
        }
        return request;
    },
    onResponse({ response }) {
        if (response.status === HTTP_STATUS.UNAUTHORIZED && browser) {
            browserTokenStorage.removeToken();
            authStore.markUnauthenticated();
            browserAuthEventBus.emitAuthRequired();
            throw new AuthRequiredError();
        }
        return response;
    },
};

const apiClient = createClient<APIPaths>({ baseUrl: API_BASE_URL });
apiClient.use(authMiddleware);

function authHeader(): string | undefined {
    if (!browser) return undefined;
    const token = browserTokenStorage.getToken();
    return token ? `Bearer ${token}` : undefined;
}

export const authApi = {
    login: async (password: string): Promise<{ token: string }> => {
        const challengeRes = await fetch(`${API_BASE_URL}/api/auth/challenge`);
        if (challengeRes.status === HTTP_STATUS.TOO_MANY_REQUESTS) throw new RateLimitedError();
        if (!challengeRes.ok) throw new Error("Failed to get auth challenge");
        const { nonce } = authChallengeResponseSchema.parse(await challengeRes.json());

        const response = await chapResponse(password, nonce);

        const res = await fetch(`${API_BASE_URL}/api/auth`, {
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
        const { data, error } = await apiClient.GET("/api/tasks");
        if (error) throw new Error("Failed to fetch tasks");
        return data ?? [];
    },

    getRuns: async (
        taskName: string,
        params?: {
            limit?: number;
            offset?: number;
            status?: string;
            task_name?: string;
            triggered_by?: "cron" | "api" | "cloud" | "service" | "startup";
            created_after?: string;
            created_before?: string;
            exit_code_min?: string;
            exit_code_max?: string;
            retries_only?: boolean;
            sort_field?:
                | "task_name"
                | "status"
                | "start_at"
                | "exit_code"
                | "duration"
                | "created_at"
                | "";
            sort_direction?: "asc" | "desc" | "";
            search?: string;
        },
    ) => {
        const { data, error } = await apiClient.GET("/api/tasks/{taskName}/runs", {
            params: { path: { taskName }, ...(params ? { query: params } : {}) },
        });
        if (error) throw new Error("Failed to fetch task runs");
        return { runs: data.runs ?? [], total: data.total };
    },

    triggerRun: async (taskName: string, params?: Record<string, string | null>) => {
        const { data, error } = await apiClient.POST("/api/tasks/{taskName}/run", {
            params: { path: { taskName } },
            ...(params && Object.keys(params).length > 0 ? { body: { params } } : {}),
        });
        if (error) throw new Error("Failed to trigger run");
        return data;
    },

    restartService: async (taskName: string): Promise<void> => {
        const { error } = await apiClient.POST("/api/tasks/{taskName}/restart", {
            params: { path: { taskName } },
        });
        if (error) throw new Error("Failed to restart service");
    },

    stopService: async (taskName: string): Promise<void> => {
        const { error } = await apiClient.POST("/api/tasks/{taskName}/stop", {
            params: { path: { taskName } },
        });
        if (error) throw new Error("Failed to stop service");
    },

    getRun: async (taskName: string, runId: string) => {
        const { data, error } = await apiClient.GET("/api/tasks/{taskName}/runs/{runId}", {
            params: { path: { taskName, runId } },
        });
        if (error) throw new Error("Failed to fetch run");
        return data;
    },

    deleteRun: async (taskName: string, runId: string): Promise<void> => {
        const { error } = await apiClient.DELETE("/api/tasks/{taskName}/runs/{runId}", {
            params: { path: { taskName, runId } },
        });
        if (error) throw new Error("Failed to delete run");
    },

    stopRun: async (taskName: string, runId: string): Promise<void> => {
        const { error } = await apiClient.POST("/api/tasks/{taskName}/runs/{runId}/stop", {
            params: { path: { taskName, runId } },
        });
        if (error) throw new Error("Failed to stop run");
    },

    getLogPage: async (
        taskName: string,
        runId: string,
        options?: { from?: number; limit?: number },
    ): Promise<LogPage> => {
        const params = new URLSearchParams();
        if (options?.from !== undefined) params.set("from", String(options.from));
        if (options?.limit !== undefined) params.set("limit", String(options.limit));
        const qs = params.toString();
        const url =
            API_BASE_URL +
            "/api/tasks/" +
            encodeURIComponent(taskName) +
            "/runs/" +
            encodeURIComponent(runId) +
            "/log" +
            (qs ? "?" + qs : "");
        const headers: HeadersInit = { Accept: "application/json" };
        const auth = authHeader();
        if (auth) headers["Authorization"] = auth;

        const response = await fetch(url, { headers });
        if (!response.ok) throw new Error("Log page fetch failed: " + String(response.status));
        return logPageSchema.parse(await response.json());
    },

    searchLogs: async (
        taskName: string,
        options: {
            q: string;
            regex: boolean;
            case: boolean;
            run_id?: string;
            limit?: number;
            cursor?: string;
        },
    ): Promise<LogSearchResponse> => {
        const params = new URLSearchParams();
        params.set("q", options.q);
        if (options.regex) params.set("regex", "true");
        if (options.case) params.set("case", "true");
        if (options.run_id) params.set("run_id", options.run_id);
        if (options.limit !== undefined) params.set("limit", String(options.limit));
        if (options.cursor) params.set("cursor", options.cursor);

        const url =
            API_BASE_URL +
            "/api/tasks/" +
            encodeURIComponent(taskName) +
            "/log/search?" +
            params.toString();
        const headers: HeadersInit = { Accept: "application/json" };
        const auth = authHeader();
        if (auth) headers["Authorization"] = auth;

        const response = await fetch(url, { headers });
        if (!response.ok) throw new Error("Log search failed: " + String(response.status));
        return logSearchResponseSchema.parse(await response.json());
    },

    getLogLineHistory: async (
        taskName: string,
        runId: string,
        lineNum: number,
    ): Promise<string[][]> => {
        const url =
            API_BASE_URL +
            "/api/tasks/" +
            encodeURIComponent(taskName) +
            "/runs/" +
            encodeURIComponent(runId) +
            "/log/line/" +
            String(lineNum) +
            "/history";
        const headers: HeadersInit = { Accept: "application/json" };
        const auth = authHeader();
        if (auth) headers["Authorization"] = auth;

        const response = await fetch(url, { headers });
        if (!response.ok)
            throw new Error("Log line history fetch failed: " + String(response.status));
        return logLineHistorySchema.parse(await response.json()).frames;
    },

    getLogRaw: async (taskName: string, runId: string): Promise<string> => {
        const url =
            API_BASE_URL +
            "/api/tasks/" +
            encodeURIComponent(taskName) +
            "/runs/" +
            encodeURIComponent(runId) +
            "/log/raw";
        const headers: HeadersInit = {};
        const auth = authHeader();
        if (auth) headers["Authorization"] = auth;

        const response = await fetch(url, { headers });
        if (!response.ok) throw new Error("Raw log fetch failed: " + String(response.status));
        return await response.text();
    },
};

export const runsApi = {
    getAll: async (params?: {
        limit?: number;
        offset?: number;
        status?: string;
        task_name?: string;
        triggered_by?: "cron" | "api" | "cloud" | "service" | "startup";
        created_after?: string;
        created_before?: string;
        exit_code_min?: string;
        exit_code_max?: string;
        retries_only?: boolean;
        sort_field?:
            | "task_name"
            | "status"
            | "start_at"
            | "exit_code"
            | "duration"
            | "created_at"
            | "";
        sort_direction?: "asc" | "desc" | "";
        search?: string;
    }) => {
        const { data, error } = await apiClient.GET("/api/runs", {
            ...(params ? { params: { query: params } } : {}),
        });
        if (error) throw new Error("Failed to fetch runs");
        return { runs: data.runs ?? [], total: data.total };
    },

    // Fetch one run by its (globally unique) ULID — no task name needed. Lets
    // the cross-task /runs view restore a deep-linked run that isn't on the
    // currently loaded page.
    getById: async (runId: string) => {
        const { data, error } = await apiClient.GET("/api/runs/{runId}", {
            params: { path: { runId } },
        });
        if (error) throw new Error("Failed to fetch run");
        return data;
    },

    bulkDelete: async (selector: RunSelector): Promise<number> => {
        const { data, error } = await apiClient.POST("/api/runs/bulk/delete", {
            body: selector,
        });
        if (error) throw new Error("Failed to delete runs");
        return data.affected;
    },

    bulkRestore: async (selector: RunSelector): Promise<number> => {
        const { data, error } = await apiClient.POST("/api/runs/bulk/restore", {
            body: selector,
        });
        if (error) throw new Error("Failed to restore runs");
        return data.affected;
    },

    bulkCancel: async (selector: RunSelector): Promise<number> => {
        const { data, error } = await apiClient.POST("/api/runs/bulk/cancel", {
            body: selector,
        });
        if (error) throw new Error("Failed to cancel runs");
        return data.affected;
    },

    bulkRerun: async (
        selector: RunSelector,
    ): Promise<{ triggered: { task_name: string; run_id: string }[] }> => {
        const { data, error } = await apiClient.POST("/api/runs/bulk/rerun", {
            body: selector,
        });
        if (error) throw new Error("Failed to re-run tasks");
        return { triggered: data.triggered ?? [] };
    },
};

export const systemApi = {
    getInfo: async () => {
        const { data, error } = await apiClient.GET("/api/info");
        if (error) throw new Error("Failed to fetch daemon info");
        return data;
    },

    getStats: async () => {
        const { data, error } = await apiClient.GET("/api/system");
        if (error) throw new Error("Failed to fetch system stats");
        return data;
    },

    getMetricsHistory: async (): Promise<MetricsSample[]> => {
        const headers: HeadersInit = {};
        const auth = authHeader();
        if (auth) headers["Authorization"] = auth;

        const response = await fetch(`${API_BASE_URL}/api/system/history`, { headers });
        if (!response.ok) throw new Error("Failed to fetch metrics history");
        return metricsSamplesSchema.parse(await response.json());
    },
};

export const metricsSampleSchema = z.object({
    ts: z.number(),
    cpu: z.number(),
    mem: z.number(),
    mem_used: z.number(),
    mem_total: z.number(),
});
const metricsSamplesSchema = z.array(metricsSampleSchema);

export type MetricsSample = z.infer<typeof metricsSampleSchema>;

// Payloads pushed over the unified /api/stream feed (mirrors the server's
// SystemSampleSSEEvent / ConfigStaleSSEEvent), so dashboards never poll
// /api/system or /api/info on a timer.
export const systemEventSchema = z.object({
    sample: metricsSampleSchema,
    uptime: z.string(),
});
export type SystemEvent = z.infer<typeof systemEventSchema>;

export const configStaleEventSchema = z.object({
    stale: z.boolean(),
});
