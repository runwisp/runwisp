// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createLoggerFactory } from "@runwisp/common";
import { SvelteSet } from "svelte/reactivity";
import type { LogCache } from "./LogCache.svelte.js";
import type { FetchLogsFn } from "./types.js";
import { isLogEvent } from "./types.js";

const createLogger = createLoggerFactory();
const logger = createLogger("LogFetcher");

/**
 * LogFetcher manages on-demand data loading for the LogConsole.
 *
 * It uses a chunk-based state machine to manage requests:
 * - Maps lines to chunk indexes to naturally deduplicate overlapping ranges.
 * - Schedules fetches using pending/in-flight chunk Sets.
 * - Throttles and prioritizes requests to prevent viewport jitter.
 */
export class LogFetcher {
    private readonly cache: LogCache;
    private fetchLogsFn: FetchLogsFn | undefined;
    private chunkSize: number;

    readonly MAX_INFLIGHT = 2;
    readonly MIN_REQUEST_INTERVAL_MS = 140;

    isFetching = $state(false);

    private readonly pendingChunks = new SvelteSet<number>();
    private readonly inFlightChunks = new SvelteSet<number>();
    private fetchTimer: ReturnType<typeof setTimeout> | null = null;
    private lastRequestAt = 0;

    private readonly onDataLoaded: ((min: number, max: number) => void) | undefined;

    constructor(
        cache: LogCache,
        fetchLogsFn: FetchLogsFn | undefined,
        chunkSize: number = 4096,
        onDataLoaded?: (min: number, max: number) => void,
    ) {
        this.cache = cache;
        this.fetchLogsFn = fetchLogsFn;
        this.chunkSize = Math.max(1, Math.floor(chunkSize));
        this.onDataLoaded = onDataLoaded;
    }

    setFetchLogsFn(fn: FetchLogsFn | undefined) {
        this.fetchLogsFn = fn;
    }

    setChunkSize(size: number) {
        this.chunkSize = Math.max(1, Math.floor(size));
    }

    enqueue(from: number, to: number) {
        if (!this.fetchLogsFn) return;

        const limit = this.cache.totalLines > 0 ? this.cache.totalLines : this.chunkSize;
        const minLine = this.cache.firstAvailableLine;
        const maxLine = limit - 1;

        const clampedFrom = Math.max(from, minLine);
        const clampedTo = Math.min(to, Math.max(clampedFrom, maxLine));

        const startChunk = Math.floor(clampedFrom / this.chunkSize);
        const endChunk = Math.floor(clampedTo / this.chunkSize);

        let queuedAny = false;
        for (let i = startChunk; i <= endChunk; i++) {
            if (this.inFlightChunks.has(i) || this.pendingChunks.has(i)) continue;

            const chunkFrom = i * this.chunkSize;
            const chunkTo = Math.min((i + 1) * this.chunkSize - 1, maxLine);
            if (this.cache.isRangeComplete(chunkFrom, chunkTo)) continue;

            this.pendingChunks.add(i);
            queuedAny = true;
        }

        if (queuedAny) {
            this.scheduleFetch();
        }
    }

    maybeRequestMissing(visibleStart: number, visibleEnd: number, maxChunks = 12) {
        if (!this.fetchLogsFn || this.cache.totalLines <= 0 || visibleEnd < visibleStart) return;

        const startChunk = Math.floor(visibleStart / this.chunkSize);
        const endChunk = Math.floor(visibleEnd / this.chunkSize);

        let scanned = 0;
        for (let i = startChunk; i <= endChunk; i++) {
            if (scanned++ >= maxChunks) break;

            const chunkFrom = i * this.chunkSize;
            const chunkTo = Math.min((i + 1) * this.chunkSize - 1, this.cache.totalLines - 1);
            if (this.cache.isRangeComplete(chunkFrom, chunkTo)) continue;

            this.enqueue(chunkFrom, chunkTo);
        }
    }

    private scheduleFetch() {
        if (!this.fetchLogsFn || this.fetchTimer || this.pendingChunks.size === 0) return;

        const now = Date.now();
        const wait = Math.max(0, this.MIN_REQUEST_INTERVAL_MS - (now - this.lastRequestAt));
        this.fetchTimer = setTimeout(() => {
            this.fetchTimer = null;
            void this.runFetchPump();
        }, wait);
    }

    pruneQueue(renderedStart: number, renderedEnd: number) {
        const centerLine = (renderedStart + renderedEnd) / 2;
        const centerChunk = Math.floor(centerLine / this.chunkSize);
        const radius = 2;

        for (const chunk of this.pendingChunks) {
            if (Math.abs(chunk - centerChunk) > radius) {
                this.pendingChunks.delete(chunk);
            }
        }
    }

    private async runFetchPump() {
        if (!this.fetchLogsFn || this.pendingChunks.size === 0) return;

        if (this.inFlightChunks.size >= this.MAX_INFLIGHT) {
            this.scheduleFetch();
            return;
        }

        // Just pick the first pending chunk (pruneQueue guarantees relevance)
        const nextChunk = this.pendingChunks.values().next().value;
        if (nextChunk === undefined) return;

        this.pendingChunks.delete(nextChunk);
        this.inFlightChunks.add(nextChunk);

        this.lastRequestAt = Date.now();
        this.isFetching = true;

        const limit = this.cache.totalLines > 0 ? this.cache.totalLines : this.chunkSize;
        const maxLine = limit - 1;
        const chunkFrom = nextChunk * this.chunkSize;
        const chunkTo = Math.min((nextChunk + 1) * this.chunkSize - 1, maxLine);

        try {
            const res = await this.fetchLogsFn(chunkFrom, chunkTo);
            if (res) {
                const merged = isLogEvent(res)
                    ? this.cache.applyEvent(res)
                    : this.cache.mergeSlice(res);

                if (merged.touched && this.onDataLoaded) {
                    this.onDataLoaded(merged.min, merged.max);
                }
            }
        } catch (e) {
            logger.error(`Failed to fetch chunk ${String(nextChunk)}`, e);
        } finally {
            this.inFlightChunks.delete(nextChunk);
            this.isFetching = this.inFlightChunks.size > 0;
            this.scheduleFetch();
        }
    }

    destroy() {
        if (this.fetchTimer) clearTimeout(this.fetchTimer);
        this.fetchTimer = null;
        this.pendingChunks.clear();
        this.inFlightChunks.clear();
        this.isFetching = false;
    }
}
