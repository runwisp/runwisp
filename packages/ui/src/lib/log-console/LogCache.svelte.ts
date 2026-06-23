// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { LogSlice, LogEvent, RegionUpdate } from "./types.js";
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
    // Live-region overlay, keyed by stream (stdout/stderr). Each stream has at
    // most one active region; the frame is replaced wholesale on each update and
    // never persisted. Rendered below the committed lines as an in-place tail.
    regions = $state(new SvelteMap<string, { epoch: number; rows: string[] }>());
    // Per-line frame-history counts. A non-zero entry marks a settled progress
    // bar / multi-line redraw whose prior frames can be fetched on click.
    frameCounts = $state(new SvelteMap<number, number>());
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

    // Flattened overlay rows for rendering, stdout before stderr (then any
    // other streams, sorted) for stable ordering. Reactive: reads the regions
    // map so it recomputes on update.
    get overlayRows(): string[] {
        const preferred = ["stdout", "stderr"];
        const rest = [...this.regions.keys()]
            .filter((s) => !preferred.includes(s))
            .sort((a, b) => a.localeCompare(b));
        const out: string[] = [];
        for (const stream of [...preferred, ...rest]) {
            const region = this.regions.get(stream);
            if (region) out.push(...region.rows);
        }
        return out;
    }

    applyRegion(update: RegionUpdate) {
        if (update.rows.length === 0) {
            this.regions.delete(update.stream);
            return;
        }
        this.regions.set(update.stream, { epoch: update.epoch, rows: update.rows });
        for (const row of update.rows) {
            this.maxLineColumns = Math.max(this.maxLineColumns, visibleColumns(row));
        }
    }

    applyEvent(event: LogEvent) {
        if (event.region) {
            this.applyRegion(event.region);
            return { touched: false, min: Number.POSITIVE_INFINITY, max: Number.NEGATIVE_INFINITY };
        }
        this.totalLines = Math.max(this.totalLines, Math.floor(event.sizeLines));
        this.totalBytes = Math.max(this.totalBytes, Math.floor(event.sizeBytes ?? 0));
        this.finished = event.finished;
        // A finished run has no live region; drop any lingering overlay so a
        // dropped clear-frame can't leave a stale tail painted forever.
        if (event.finished && this.regions.size > 0) this.regions.clear();
        if (event.firstAvailableLine !== undefined && event.firstAvailableLine > 0) {
            this.firstAvailableLine = Math.max(this.firstAvailableLine, event.firstAvailableLine);
        }
        if (event.frameCounts) {
            for (const [k, v] of Object.entries(event.frameCounts)) {
                const line = Number(k);
                if (Number.isFinite(line) && line >= 0 && v > 0) this.frameCounts.set(line, v);
            }
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
        this.regions.clear();
        this.frameCounts.clear();
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
