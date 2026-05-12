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
import { logPageSchema, type LogPage } from "./logs";
import {
    authStatusResponseSchema,
    srpStartResponseSchema,
    srpFinishResponseSchema,
    type AuthStatusResponse,
} from "./types";
import { Client as SRPClient, RFC5054Group4096, type Params } from "@mzattahri/srp";

// SRP_IDENTITY must match the Go-side datadir.SRPIdentity constant. RunWisp
// has no user model — the SRP "username" is a placeholder needed only to bind
// the verifier and proofs to a constant value.
const SRP_IDENTITY = "runwisp";

// SRP_PBKDF2_ITERATIONS must match the Go-side datadir.pbkdf2Iterations
// constant. Changing it on either side breaks existing verifiers.
const SRP_PBKDF2_ITERATIONS = 600_000;

function concatU8(...parts: Uint8Array[]): Uint8Array {
    const len = parts.reduce((n, p) => n + p.length, 0);
    const out = new Uint8Array(len);
    let off = 0;
    for (const p of parts) {
        out.set(p, off);
        off += p.length;
    }
    return out;
}

function hexToU8(hex: string): Uint8Array {
    if (hex.length % 2 !== 0) throw new Error("invalid hex");
    const out = new Uint8Array(hex.length / 2);
    for (let i = 0; i < out.length; i++) {
        out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    }
    return out;
}

function u8ToHex(buf: Uint8Array): string {
    return Array.from(buf)
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
}

// srpParams mirrors the Go-side datadir.srpParams: RFC5054 group 16 (4096-bit),
// SHA-256 hash, PBKDF2-SHA256 (600 000 iter) KDF. Drift on either side breaks
// authentication.
const srpParams: Params = {
    name: "DH16-SHA256-PBKDF2-600k",
    group: RFC5054Group4096,
    hash: async (...inputs: Uint8Array[]) => {
        const data = concatU8(...inputs);
        // Copy into a fresh ArrayBuffer so WebCrypto's BufferSource type
        // accepts it across all browsers; some Uint8Array variants are typed
        // over SharedArrayBuffer which subtle.digest disallows.
        const buf = new ArrayBuffer(data.byteLength);
        new Uint8Array(buf).set(data);
        return new Uint8Array(await crypto.subtle.digest("SHA-256", buf));
    },
    kdf: async (_username: string, password: string, salt: Uint8Array) => {
        // Match the Go KDF exactly: ignore username, pass password bytes
        // (not username:password) into PBKDF2-SHA256 with the server-provided
        // salt and a fixed iteration count.
        const pwBytes = new TextEncoder().encode(password);
        const pwBuf = new ArrayBuffer(pwBytes.byteLength);
        new Uint8Array(pwBuf).set(pwBytes);
        const passwordKey = await crypto.subtle.importKey("raw", pwBuf, { name: "PBKDF2" }, false, [
            "deriveBits",
        ]);
        const saltBuf = new ArrayBuffer(salt.byteLength);
        new Uint8Array(saltBuf).set(salt);
        const bits = await crypto.subtle.deriveBits(
            {
                name: "PBKDF2",
                hash: "SHA-256",
                salt: saltBuf,
                iterations: SRP_PBKDF2_ITERATIONS,
            },
            passwordKey,
            32 * 8,
        );
        return new Uint8Array(bits);
    },
};

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
        // 1. Ask the server for a fresh SRP session: salt, B, sessionID.
        const startRes = await fetch(`${API_BASE_URL}/api/auth/srp/start`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: "{}",
        });
        if (!startRes.ok) throw new Error("Failed to start SRP session");
        const start = srpStartResponseSchema.parse(await startRes.json());

        // 2. PBKDF2-stretch the password against the server's salt (~300–500ms
        //    in modern browsers), construct an SRP client, and compute M1.
        const salt = hexToU8(start.salt);
        const B = hexToU8(start.B);
        const srpClient = await SRPClient.initialize(srpParams, SRP_IDENTITY, password, salt);
        await srpClient.setB(B);
        const M1 = srpClient.M1;
        const A = srpClient.A;

        // 3. Send A + M1 against the sessionID. Receive M2 + JWT.
        const finishRes = await fetch(`${API_BASE_URL}/api/auth/srp/finish`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
                sessionID: start.sessionID,
                A: u8ToHex(A),
                M1: u8ToHex(M1),
            }),
        });
        if (!finishRes.ok) throw new Error("Authentication failed");
        const finish = srpFinishResponseSchema.parse(await finishRes.json());

        // 4. Verify the server's proof M2 — mutual authentication. If this
        //    fails the daemon is impersonating, never use the token.
        const M2 = hexToU8(finish.M2);
        if (!srpClient.checkM2(M2)) {
            throw new Error("Server proof verification failed");
        }

        return { token: finish.token };
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
