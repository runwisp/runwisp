// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { untrack } from "svelte";
import { visibleColumns } from "./ansi.js";
import type { LogCache } from "./LogCache.svelte.js";

/** Pixels per rendered row. */
export const LINE_HEIGHT = 20;

// Right padding on the text cell (pr-4); reserved when computing how many
// columns a wrapped line can use.
const TEXT_PADDING = 16;

interface WrapLayoutInputs {
    wrap: () => boolean;
    /** Width of the scroll container in px. */
    width: () => number;
    gutterWidth: () => number;
    /** Width of one monospace column in px. */
    charWidth: () => number;
}

/**
 * Row geometry for the virtualized log. With wrapping off every line is one
 * row. With wrapping on, each line occupies as many rows as its text wraps
 * into at the current column width, so positions come from a prefix sum of the
 * per-line row counts instead of a multiplication. Construct during component
 * init: it registers effects.
 */
export class WrapLayout {
    // rowCounts remembers the wrapped row count for every line whose text has
    // been observed; lines never loaded (pruned or out-of-window) count as 1.
    // A plain Map is deliberate: reactivity is driven by the `#version` counter
    // (bumped on any change), not by per-key subscriptions, iterating a
    // SvelteMap of tens of thousands of lines on every streamed line would be
    // wasteful.
    // eslint-disable-next-line svelte/prefer-svelte-reactivity
    readonly #rowCounts = new Map<number, number>();
    #version = $state(0);
    readonly #cache: LogCache;
    readonly #inputs: WrapLayoutInputs;

    // How many columns a wrapped line can use at the current width.
    get #availableColumns(): number {
        const { wrap, width, gutterWidth, charWidth } = this.#inputs;
        if (!wrap() || charWidth() <= 0) return 1;
        return Math.max(1, Math.floor((width() - gutterWidth() - TEXT_PADDING) / charWidth()));
    }

    // prefixSums[i] is the total rows consumed by lines
    // [firstAvailableLine, firstAvailableLine + i). Empty when wrapping is off
    // (the fixed-height path ignores it).
    readonly #prefixSums = $derived.by(() => {
        const first = this.#cache.firstAvailableLine;
        const total = this.#cache.totalLines;
        // Subscribe to the counter that signals rowCounts changed.
        // eslint-disable-next-line @typescript-eslint/no-meaningless-void-operator
        void this.#version;
        if (!this.#inputs.wrap()) return [];
        const count = Math.max(0, total - first);
        const sums = new Array<number>(count + 1);
        sums[0] = 0;
        let acc = 0;
        for (let i = 0; i < count; i++) {
            acc += this.#rowCounts.get(first + i) ?? 1;
            sums[i + 1] = acc;
        }
        return sums;
    });

    constructor(cache: LogCache, inputs: WrapLayoutInputs) {
        this.#cache = cache;
        this.#inputs = inputs;

        // Full remeasure of every loaded line, needed only when wrapping turns
        // on or the available column width changes (wrap width affects every
        // line's row count). Line appends/backfills are measured incrementally
        // via measureRange; reading `cache.lines` in this effect's tracked scope
        // would resubscribe it to the SvelteMap's shared version signal and
        // re-run this O(n) walk on every appended line, hence `untrack`. Pruned
        // lines keep their last-computed count so scroll geometry stays stable.
        $effect(() => {
            if (!this.#inputs.wrap()) return;
            const cols = this.#availableColumns;
            untrack(() => {
                let changed = false;
                for (const [num, text] of this.#cache.lines) {
                    changed = this.#measure(num, text, cols) || changed;
                }
                if (changed) this.#version++;
            });
        });

        // Drop wrap geometry when wrapping turns off so memory doesn't linger.
        $effect(() => {
            if (!this.#inputs.wrap()) this.#rowCounts.clear();
        });
    }

    /**
     * Measure the wrapped row counts of the lines in [min, max]. Called where
     * new line text lands in the cache (streamed append, on-demand backfill),
     * each of which knows the touched range, so one appended line costs O(1).
     */
    measureRange(min: number, max: number): void {
        if (!this.#inputs.wrap() || min > max) return;
        const cols = this.#availableColumns;
        let changed = false;
        for (let num = min; num <= max; num++) {
            changed = this.#measure(num, this.#cache.lines.get(num), cols) || changed;
        }
        if (changed) this.#version++;
    }

    /** Rows a line occupies. */
    rowCount(lineNum: number): number {
        return this.#inputs.wrap() ? (this.#rowCounts.get(lineNum) ?? 1) : 1;
    }

    /** Rows consumed by the lines before `lineNum`. */
    rowsAbove(lineNum: number): number {
        const i = lineNum - this.#cache.firstAvailableLine;
        if (!this.#inputs.wrap()) return i;
        const sums = this.#prefixSums;
        if (i <= 0) return 0;
        if (i >= sums.length) return sums.at(-1) ?? 0;
        return sums[i] ?? 0;
    }

    /** Rows consumed by every loaded line. */
    get totalRows(): number {
        const { firstAvailableLine, totalLines } = this.#cache;
        if (!this.#inputs.wrap()) return totalLines - firstAvailableLine;
        return this.#prefixSums.at(-1) ?? Math.max(0, totalLines - firstAvailableLine);
    }

    /** Offset from firstAvailableLine of the line whose rows cover `yRows`. */
    offsetAt(yRows: number): number {
        if (!this.#inputs.wrap()) return Math.floor(yRows);
        const sums = this.#prefixSums;
        if (sums.length === 0) return 0;
        if (yRows <= (sums[0] ?? 0)) return 0;
        const last = sums.length - 1;
        if (yRows >= (sums[last] ?? Number.POSITIVE_INFINITY)) return last;
        let lo = 0;
        let hi = last;
        while (lo < hi) {
            const mid = (lo + hi + 1) >> 1;
            if ((sums[mid] ?? Number.POSITIVE_INFINITY) <= yRows) lo = mid;
            else hi = mid - 1;
        }
        return lo;
    }

    /** Offset of the first line starting at or past `limitRows`, scanning on from `yRows`. */
    offsetEnd(yRows: number, limitRows: number): number {
        if (!this.#inputs.wrap()) return Math.ceil(limitRows);
        const sums = this.#prefixSums;
        const count = sums.length - 1;
        let end = this.offsetAt(yRows);
        while (end < count && (sums[end] ?? Number.POSITIVE_INFINITY) < limitRows) end++;
        return Math.max(0, end);
    }

    #measure(num: number, text: string | undefined, cols: number): boolean {
        const rows = text && cols > 1 ? Math.max(1, Math.ceil(visibleColumns(text) / cols)) : 1;
        if (this.#rowCounts.get(num) === rows) return false;
        this.#rowCounts.set(num, rows);
        return true;
    }
}
