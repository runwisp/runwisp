// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { SvelteSet } from "svelte/reactivity";
import { systemApi, AuthRequiredError } from "$lib/api";
import { createLogger } from "@runwisp/common";
import { extractErrorMessage } from "@runwisp/ui";
import { isRecord } from "$lib/utils/parse";
import { handleUnauthorized } from "$lib/utils/auth-required";
import { HTTP_STATUS } from "$lib/config/constants";
import type { AppEventStream } from "./event-manager";

export type ConnectionStatus = "connecting" | "connected" | "disconnected" | "stalled";

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
    const upSources = new SvelteSet<string>();
    // Sources whose stream is stuck CONNECTING (browser connection cap full),
    // distinct from sources that are truly down. Tracked separately so a stall
    // doesn't masquerade as an offline daemon.
    const stalledSources = new SvelteSet<string>();

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

    function scheduleRetry() {
        cancelRetry();
        const bounded = Math.min(Math.max(retryDelay, INITIAL_RETRY_DELAY_MS), MAX_RETRY_DELAY_MS);
        nextRetryAt = Date.now() + bounded;
        retryTimer = setTimeout(() => {
            void attemptReconnect();
        }, bounded);
    }

    function markConnected() {
        // Only fire reconnect listeners when we've actually recovered from a
        // previous successful connection, not on the very first success.
        const wasDown = Boolean(lastConnectedAt) && status !== "connected";
        status = "connected";
        lastConnectedAt = Date.now();
        disconnectedSince = null;
        retryAttempts = 0;
        lastError = null;
        retryDelay = INITIAL_RETRY_DELAY_MS;
        stalledSources.clear();
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
        if (!retryTimer && !pingInFlight) {
            scheduleRetry();
        }
    }

    function markStalled() {
        status = "stalled";
        lastError = null;
        retryAttempts = 0;
        // A stall recovers on its own: the browser opens the queued EventSource
        // once a connection slot frees, firing `open` → reportSourceUp →
        // markConnected. So no fetch-ping retry here, a ping could reach the
        // daemon and wrongly flip us to "connected" while no live events flow,
        // and no "down for" tick, because we are not down.
        cancelRetry();
        stopTick();
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
                // Server responded (just needs auth), it's reachable.
                markConnected();
                return true;
            }
            retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY_MS);
            status = "disconnected";
            lastError = formatError(err);
            scheduleRetry();
            return false;
        } finally {
            pingInFlight = false;
        }
    }

    function onReconnect(listener: Listener): () => void {
        reconnectListeners.add(listener);
        return () => reconnectListeners.delete(listener);
    }

    function reportSourceUp(id: string) {
        upSources.add(id);
        stalledSources.delete(id);
        markConnected();
    }

    function reportSourceStalled(id: string) {
        upSources.delete(id);
        stalledSources.add(id);
        if (upSources.size === 0) markStalled();
    }

    function reportSourceDown(id: string, err?: unknown) {
        upSources.delete(id);
        stalledSources.delete(id);
        if (upSources.size === 0 && stalledSources.size === 0) {
            markDisconnected(err);
        } else if (upSources.size === 0) {
            // No live source left, but another is stalled (waiting for a
            // connection slot): reflect "updates paused" rather than keeping the
            // stale prior status. Latent today, the per-domain cap that
            // populates stalledSources isn't reached with only two streams, but
            // it strands the UI on "connected" once a third stream is added.
            markStalled();
        }
    }

    // A source the app closed on purpose (logout, unmount) is not a lost
    // connection: forget it without flipping status or scheduling a ping.
    // Reporting it down instead would make a logged-out page ping /api/system every
    // 2 s, and each 401 would re-open the login modal (clearing the password field).
    function releaseSource(id: string) {
        upSources.delete(id);
        stalledSources.delete(id);
    }

    function reportFetchError(err: unknown): boolean {
        if (isConnectionError(err)) {
            markDisconnected(err);
            return true;
        }
        return false;
    }

    /** The message to show for a failed REST fetch, or null for a 401 (the
     * login flow owns that). A connection-shaped failure also marks the
     * daemon unreachable. */
    function fetchErrorMessage(err: unknown, fallback?: string): string | null {
        if (err instanceof AuthRequiredError) return null;
        return reportFetchError(err) ? "Connection lost" : extractErrorMessage(err, fallback);
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
        fetchErrorMessage,
        reportSourceUp,
        reportSourceStalled,
        reportSourceDown,
        releaseSource,
        retryNow: attemptReconnect,
        onReconnect,
    };
}

function isConnectionError(err: unknown): boolean {
    if (err instanceof AuthRequiredError) return false;
    const msg = formatError(err);
    if (!msg) return false;
    return /networkerror|failed to fetch|load failed|network request failed|typeerror.*fetch/i.test(
        msg,
    );
}

function formatError(err: unknown): string | null {
    if (err instanceof Error) return err.message;
    if (typeof err === "string") return err;
    if (typeof err === "number" || typeof err === "boolean") return String(err);
    if (!isRecord(err)) return null;
    try {
        return JSON.stringify(err);
    } catch {
        return "Unknown error";
    }
}

export const connectionStore = createConnectionStore();

/**
 * Mirror a live stream's health into {@link connectionStore} under `id`: open
 * reports the source up, a stall reports it stalled, and an error reports it
 * down. A 401 is an expired session on a reachable daemon, not a lost
 * connection, so it routes to the login flow instead. Returns the unsubscribe.
 */
export function trackStreamHealth(events: AppEventStream, id: string): () => void {
    const offs = [
        events.onOpen(() => {
            connectionStore.reportSourceUp(id);
        }),
        events.onError((info) => {
            if (info.status === HTTP_STATUS.UNAUTHORIZED) {
                handleUnauthorized();
            } else {
                connectionStore.reportSourceDown(id, info.message ?? "Event stream error");
            }
        }),
        events.onStall(() => {
            connectionStore.reportSourceStalled(id);
        }),
    ];
    return () => {
        for (const off of offs) off();
    };
}
