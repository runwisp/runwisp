// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { createLogger } from "@runwisp/common";
import { RUN_EVENT_TYPES, parseRunUpdate } from "$lib/types";
import { appEventStream } from "./app-stream";
import { connectionStore, trackStreamHealth } from "./connection.svelte";
import type { RunUpdateEventType, RunUpdateHandler } from "$lib/types";

const SOURCE_ID = "run-updates";

class RunUpdateManager {
    private readonly events = appEventStream;
    private readonly handlers = new Set<RunUpdateHandler>();
    private readonly unsubscribes: (() => void)[] = [];
    private readonly logger = createLogger("RunUpdateManager");
    private connected = false;

    connect(): void {
        if (this.connected) return;
        this.connected = true;

        this.unsubscribes.push(trackStreamHealth(this.events, SOURCE_ID));

        for (const eventType of RUN_EVENT_TYPES) {
            this.unsubscribes.push(
                this.events.subscribe(eventType, (data) => {
                    this.dispatch(eventType, data);
                }),
            );
        }
    }

    private dispatch(eventType: RunUpdateEventType, data: string): void {
        const result = parseRunUpdate(eventType, data);
        if (!result.success) {
            this.logger.error("Invalid SSE event", eventType, data, result.error);
            return;
        }
        for (const handler of this.handlers) handler(result.data);
    }

    disconnect(): void {
        for (const off of this.unsubscribes) off();
        this.unsubscribes.length = 0;
        this.connected = false;
        connectionStore.releaseSource(SOURCE_ID);
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
