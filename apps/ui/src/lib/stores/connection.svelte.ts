// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { SvelteSet } from "svelte/reactivity";
import { systemApi, AuthRequiredError } from "$lib/api";
import { createLogger } from "$lib/utils/logger";

export type ConnectionStatus = "connecting" | "connected" | "disconnected";

const INITIAL_RETRY_DELAY_MS = 2000;
const MAX_RETRY_DELAY_MS = 30000;

const logger = createLogger("ConnectionStore");

type Listener = () => void;

function createConnectionStore() {
    let status = $state<ConnectionStatus>("connecting");
    let lastConnectedAt = $state<number | null>(null);
    let disconnectedSince = $state<number | null>(null);
    let nextRetryAt = $state<number | null>(null);
    let retryAttempts = $state(0);
    let lastError = $state<string | null>(null);
    let now = $state(Date.now());

    let tickTimer: ReturnType<typeof setInterval> | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let retryDelay = INITIAL_RETRY_DELAY_MS;
    let pingInFlight = false;
    const reconnectListeners = new SvelteSet<Listener>();

    function startTick() {
        if (tickTimer) return;
        now = Date.now();
        tickTimer = setInterval(() => {
            now = Date.now();
        }, 1000);
    }

    function stopTick() {
        if (tickTimer) {
            clearInterval(tickTimer);
            tickTimer = null;
        }
    }

    function cancelRetry() {
        if (retryTimer) {
            clearTimeout(retryTimer);
            retryTimer = null;
        }
        nextRetryAt = null;
    }

    function scheduleRetry(delayMs: number = retryDelay) {
        cancelRetry();
        const bounded = Math.min(Math.max(delayMs, INITIAL_RETRY_DELAY_MS), MAX_RETRY_DELAY_MS);
        nextRetryAt = Date.now() + bounded;
        retryTimer = setTimeout(() => {
            void attemptReconnect();
        }, bounded);
        retryDelay = Math.min(bounded * 2, MAX_RETRY_DELAY_MS);
    }

    function markConnected() {
        // Only fire reconnect listeners when we've actually recovered from a
        // previous successful connection — not on the very first success.
        const wasDown = lastConnectedAt !== null && status !== "connected";
        status = "connected";
        lastConnectedAt = Date.now();
        disconnectedSince = null;
        retryAttempts = 0;
        lastError = null;
        retryDelay = INITIAL_RETRY_DELAY_MS;
        cancelRetry();
        stopTick();
        if (wasDown) {
            for (const listener of reconnectListeners) {
                try {
                    listener();
                } catch (err) {
                    logger.warn("reconnect listener threw", err);
                }
            }
        }
    }

    function markDisconnected(error?: unknown) {
        if (status === "connected") {
            disconnectedSince = Date.now();
            retryDelay = INITIAL_RETRY_DELAY_MS;
        }
        status = "disconnected";
        lastError = formatError(error);
        startTick();
        scheduleRetry();
    }

    async function attemptReconnect(): Promise<boolean> {
        if (pingInFlight) return false;
        pingInFlight = true;
        cancelRetry();
        if (status !== "connected") {
            status = "connecting";
            startTick();
        }
        retryAttempts++;
        try {
            await systemApi.getStats();
            markConnected();
            return true;
        } catch (err) {
            if (err instanceof AuthRequiredError) {
                // Server responded (just needs auth) — it's reachable.
                markConnected();
                return true;
            }
            markDisconnected(err);
            return false;
        } finally {
            pingInFlight = false;
        }
    }

    function onReconnect(listener: Listener): () => void {
        reconnectListeners.add(listener);
        return () => reconnectListeners.delete(listener);
    }

    function reportFetchError(err: unknown): boolean {
        if (isConnectionError(err)) {
            markDisconnected(err);
            return true;
        }
        return false;
    }

    return {
        get status() {
            return status;
        },
        get lastConnectedAt() {
            return lastConnectedAt;
        },
        get disconnectedSince() {
            return disconnectedSince;
        },
        get nextRetryAt() {
            return nextRetryAt;
        },
        get retryAttempts() {
            return retryAttempts;
        },
        get lastError() {
            return lastError;
        },
        get now() {
            return now;
        },
        get isRetrying() {
            return pingInFlight;
        },
        markConnected,
        markDisconnected,
        reportFetchError,
        retryNow: attemptReconnect,
        onReconnect,
    };
}

function isConnectionError(err: unknown): boolean {
    if (err instanceof AuthRequiredError) return false;
    const msg = formatError(err);
    if (msg === null) return false;
    return /networkerror|failed to fetch|load failed|network request failed|typeerror.*fetch/i.test(
        msg,
    );
}

function formatError(err: unknown): string | null {
    if (err === undefined || err === null) return null;
    if (err instanceof Error) return err.message;
    if (typeof err === "string") return err;
    if (typeof err === "number" || typeof err === "boolean") return String(err);
    try {
        return JSON.stringify(err);
    } catch {
        return "Unknown error";
    }
}

export const connectionStore = createConnectionStore();
