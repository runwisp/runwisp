// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createLogger } from "$lib/utils/logger";
import { runUpdateEventSchema } from "$lib/types";
import { browserAuthEventSourceFactory } from "$lib/adapters/browser";
import { getApiUrl } from "$lib/utils/env";
import { connectSSE, type SSEConnection } from "$lib/utils/sse";
import type { RunUpdateEvent, RunUpdateEventType, RunUpdateHandler } from "$lib/types";

export type { RunUpdateEvent, RunUpdateHandler };

class RunUpdateManager {
    private connection: SSEConnection | null = null;
    private readonly handlers = new Set<RunUpdateHandler>();
    private readonly logger = createLogger("RunUpdateManager");

    connect(): void {
        if (this.connection) return;

        // Auth is handled by the event source factory (Bearer header or cookie).
        // The caller (layout) only calls connect() when authenticated.

        const eventTypes: RunUpdateEventType[] = [
            "run.created",
            "run.started",
            "run.completed",
            "run.failed",
            "run.updated",
        ];

        this.connection = connectSSE({
            path: "/api/runs/stream",
            eventTypes,
            onOpen: () => {
                this.logger.info("SSE connection established");
            },
            onError: (info) => {
                this.logger.warn(
                    `SSE connection error: ${info.message ?? "unknown"}`,
                    info.status !== undefined ? `(HTTP ${info.status.toString()})` : "",
                );
            },
            onEvent: (eventType, data) => {
                try {
                    const parsed: unknown = JSON.parse(data);
                    this.logger.debug("SSE raw event", eventType, JSON.stringify(parsed, null, 2));
                    const envelope = {
                        type: eventType,
                        timestamp: new Date().toISOString(),
                        data: parsed,
                    };
                    const result = runUpdateEventSchema.safeParse(envelope);
                    if (!result.success) {
                        this.logger.error(
                            "Invalid SSE event",
                            result.error.message,
                            "raw envelope:",
                            JSON.stringify(envelope, null, 2),
                        );
                        return;
                    }
                    this.logger.debug("SSE event validated OK", eventType, result.data.data.run.id);
                    for (const handler of this.handlers) handler(result.data);
                } catch (e) {
                    this.logger.error("Malformed SSE event JSON", data, e);
                }
            },
            deps: {
                createEventSource: browserAuthEventSourceFactory,
                getApiUrl,
            },
        });
    }

    disconnect(): void {
        if (this.connection) {
            this.connection.disconnect();
            this.connection = null;
        }
    }

    subscribeToUpdates(handler: RunUpdateHandler): () => void {
        this.handlers.add(handler);
        this.logger.debug("Handler subscribed, total:", this.handlers.size);

        if (this.handlers.size === 1 && !this.connection) {
            this.connect();
        }

        return () => {
            this.handlers.delete(handler);
            this.logger.debug("Handler unsubscribed, remaining:", this.handlers.size);
        };
    }
}

export const runUpdatesStore = new RunUpdateManager();
