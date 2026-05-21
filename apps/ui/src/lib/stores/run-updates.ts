// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createLogger } from "$lib/utils/logger";
import { runUpdateEventSchema } from "$lib/types";
import { EventManager } from "./event-manager.svelte";
import { connectionStore } from "./connection.svelte";
import type { RunUpdateEventType, RunUpdateHandler } from "$lib/types";

export type { RunUpdateEvent, RunUpdateHandler } from "$lib/types";

const RUN_EVENT_TYPES: RunUpdateEventType[] = [
    "run.created",
    "run.started",
    "run.completed",
    "run.failed",
    "run.updated",
    "run.deleted",
];

const SOURCE_ID = "run-updates";

class RunUpdateManager {
    private readonly events = new EventManager({ path: "/api/runs/stream" });
    private readonly handlers = new Set<RunUpdateHandler>();
    private readonly unsubscribes: (() => void)[] = [];
    private readonly logger = createLogger("RunUpdateManager");
    private connected = false;

    connect(): void {
        if (this.connected) return;
        this.connected = true;

        this.unsubscribes.push(
            this.events.onOpen(() => {
                this.logger.info("SSE connection established");
                connectionStore.reportSourceUp(SOURCE_ID);
            }),
            this.events.onError((info) => {
                this.logger.warn(
                    `SSE connection error: ${info.message ?? "unknown"}`,
                    info.status === undefined ? "" : `(HTTP ${info.status.toString()})`,
                );
                // 401 means the daemon is up but rejected our auth — not a connection loss.
                if (info.status !== 401) {
                    connectionStore.reportSourceDown(
                        SOURCE_ID,
                        info.message ?? "SSE connection error",
                    );
                }
            }),
        );

        for (const eventType of RUN_EVENT_TYPES) {
            const dispatchType: RunUpdateEventType = eventType;
            this.unsubscribes.push(
                this.events.subscribe(eventType, (data) => {
                    this.dispatch(dispatchType, data);
                }),
            );
        }
    }

    private dispatch(eventType: RunUpdateEventType, data: string): void {
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
            const identity =
                result.data.type === "run.deleted"
                    ? result.data.data.run_id
                    : result.data.data.run.id;
            this.logger.debug("SSE event validated OK", eventType, identity);
            for (const handler of this.handlers) handler(result.data);
        } catch (e) {
            this.logger.error("Malformed SSE event JSON", data, e);
        }
    }

    disconnect(): void {
        for (const off of this.unsubscribes) off();
        this.unsubscribes.length = 0;
        this.connected = false;
        connectionStore.reportSourceDown(SOURCE_ID);
    }

    subscribeToUpdates(handler: RunUpdateHandler): () => void {
        this.handlers.add(handler);
        this.logger.debug("Handler subscribed, total:", this.handlers.size);

        if (this.handlers.size === 1) {
            this.connect();
        }

        return () => {
            this.handlers.delete(handler);
            this.logger.debug("Handler unsubscribed, remaining:", this.handlers.size);
        };
    }
}

export const runUpdatesStore = new RunUpdateManager();
