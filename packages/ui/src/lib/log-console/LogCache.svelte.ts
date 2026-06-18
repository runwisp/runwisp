// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { LogSlice, LogEvent } from "./types.js";
import { SvelteMap } from "svelte/reactivity";
import { visibleColumns } from "./ansi.js";

/**
 * LogCache provides high-performance storage and pruning for log data.
 *
 * To handle up to 1 million lines without crashing the browser's memory,
 * the cache must aggressively prune data that is far from the current viewport.
 * It uses a SvelteMap for reactive, efficient line lookups and updates.
 */
export class LogCache {
    lines = $state(new SvelteMap<number, string>());
    totalLines = $state(0);
    totalBytes = $state(0);
    finished = $state(false);
    firstAvailableLine = $state(0);
    // Widest visible line seen so far, in monospace columns. Monotonic so the
    // horizontal scroll surface never shrinks as windowed lines are pruned.
    maxLineColumns = $state(0);

    get size() {
        return this.lines.size;
    }

    readonly MAX_CACHE_SIZE = 50_000;
    readonly PRUNE_THRESHOLD = 60_000;

    mergeSlice(slice: LogSlice) {
        let min = Number.POSITIVE_INFINITY;
        let max = Number.NEGATIVE_INFINITY;
        let touched = false;

        for (const [k, v] of Object.entries(slice)) {
            const line = Number(k);
            if (!Number.isFinite(line) || line < 0) continue;
            min = Math.min(min, line);
            max = Math.max(max, line);
            const prev = this.lines.get(line);
            if (prev !== v) {
                this.lines.set(line, v);
                this.maxLineColumns = Math.max(this.maxLineColumns, visibleColumns(v));
                touched = true;
            }
        }

        return { touched, min, max };
    }

    applyEvent(event: LogEvent) {
        this.totalLines = Math.max(this.totalLines, Math.floor(event.sizeLines));
        this.totalBytes = Math.max(this.totalBytes, Math.floor(event.sizeBytes ?? 0));
        this.finished = event.finished;
        if (event.firstAvailableLine !== undefined && event.firstAvailableLine > 0) {
            this.firstAvailableLine = Math.max(this.firstAvailableLine, event.firstAvailableLine);
        }

        return this.mergeSlice(event.lines);
    }

    prune(visibleStart: number, visibleEnd: number) {
        if (this.lines.size < this.MAX_CACHE_SIZE) return;

        const center = (visibleStart + visibleEnd) / 2;
        const chunk = this.MAX_CACHE_SIZE / 2;
        const low = center - chunk;
        const high = center + chunk;

        for (const key of this.lines.keys()) {
            if (key < low || key > high) {
                this.lines.delete(key);
            }
        }
    }

    reset() {
        this.lines.clear();
        this.totalLines = 0;
        this.totalBytes = 0;
        this.finished = false;
        this.firstAvailableLine = 0;
        this.maxLineColumns = 0;
    }

    isRangeComplete(start: number, end: number) {
        const max = Math.max(0, this.totalLines - 1);
        const min = this.firstAvailableLine;
        const s = Math.max(min, Math.min(max, start));
        const e = Math.max(s, Math.min(max, end));

        for (let line = s; line <= e; line++) {
            if (!this.lines.has(line)) return false;
        }
        return true;
    }
}
