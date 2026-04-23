// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import createClient, { type Middleware } from "openapi-fetch";
import { z } from "zod";
import type { APIPaths } from "@runwisp/common";
import { browser } from "$app/environment";
import { getApiUrl } from "./utils/env";
import { HTTP_STATUS } from "./config/constants";
import { browserTokenStorage, browserAuthEventBus } from "$lib/adapters/browser";
import { authStore } from "./stores/auth.svelte";
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
        if (!challengeRes.ok) throw new Error("Failed to get auth challenge");
        const { nonce } = authChallengeResponseSchema.parse(await challengeRes.json());

        const enc = new TextEncoder();
        const hashBuf = await globalThis.crypto.subtle.digest(
            "SHA-256",
            enc.encode(password + ":" + nonce),
        );
        const response = Array.from(new Uint8Array(hashBuf))
            .map((b) => b.toString(16).padStart(2, "0"))
            .join("");

        const res = await fetch(`${API_BASE_URL}/api/auth`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ nonce, response }),
        });
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
            sort_field?: "task_name" | "status" | "start_at" | "exit_code" | "duration" | "";
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

    triggerRun: async (taskName: string) => {
        const { data, error } = await apiClient.POST("/api/tasks/{taskName}/run", {
            params: { path: { taskName } },
        });
        if (error) throw new Error("Failed to trigger run");
        return data;
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

    getLog: async (
        taskName: string,
        runId: string,
        lines?: { start: number; end: number },
    ): Promise<{ content: string; totalLines?: number; firstAvailableLine?: number }> => {
        let url =
            API_BASE_URL +
            "/api/tasks/" +
            encodeURIComponent(taskName) +
            "/runs/" +
            encodeURIComponent(runId) +
            "/log";
        if (lines) {
            url += "?start_line=" + String(lines.start) + "&end_line=" + String(lines.end);
        }
        const headers: HeadersInit = {};
        const auth = authHeader();
        if (auth) headers["Authorization"] = auth;

        const response = await fetch(url, { headers });
        if (!response.ok) throw new Error("Log fetch failed: " + String(response.status));

        const content = await response.text();
        const totalLinesHeader = response.headers.get("x-total-lines");
        const firstAvailableHeader = response.headers.get("x-first-available-line");

        const result: { content: string; totalLines?: number; firstAvailableLine?: number } = {
            content,
        };
        if (totalLinesHeader) result.totalLines = parseInt(totalLinesHeader, 10);
        if (firstAvailableHeader) result.firstAvailableLine = parseInt(firstAvailableHeader, 10);
        return result;
    },
};

export const runsApi = {
    getAll: async (params?: {
        limit?: number;
        offset?: number;
        status?: string;
        task_name?: string;
        sort_field?: "task_name" | "status" | "start_at" | "exit_code" | "duration" | "";
        sort_direction?: "asc" | "desc" | "";
        search?: string;
    }) => {
        const { data, error } = await apiClient.GET("/api/runs", {
            ...(params ? { params: { query: params } } : {}),
        });
        if (error) throw new Error("Failed to fetch runs");
        return { runs: data.runs ?? [], total: data.total };
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

const metricsSampleSchema = z.object({
    ts: z.number(),
    cpu: z.number(),
    mem: z.number(),
    mem_used: z.number(),
    mem_total: z.number(),
});
const metricsSamplesSchema = z.array(metricsSampleSchema);

export type MetricsSample = z.infer<typeof metricsSampleSchema>;
