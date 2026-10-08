// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { LINE_HEIGHT } from "./wrap-layout.svelte.js";

type FetchLineHistory = (lineNum: number) => Promise<string[][]>;

/** Padding above and below the block content, px. */
export const FRAME_BLOCK_PAD = 8;
/** Gap between consecutive frames, px. */
export const FRAME_GAP = 6;
// The block scrolls internally beyond this height, px.
const FRAME_BLOCK_MAX = 400;

/**
 * Inline expansion of a settled progress bar / multi-line redraw: the prior
 * whole-region frames it passed through, shown under its committed line. One
 * expansion at a time.
 */
export class FrameHistory {
    /** Absolute line number whose history block is open. */
    line = $state<number | null>(null);
    /** The fetched frames; null while loading. */
    frames = $state<string[][] | null>(null);
    failed = $state(false);
    readonly #fetch: () => FetchLineHistory | undefined;

    constructor(fetch: () => FetchLineHistory | undefined) {
        this.#fetch = fetch;
    }

    /**
     * Height the open block occupies in the virtual surface. Later lines are
     * shifted down by exactly this, so the layout math stays a single offset.
     */
    readonly blockHeight = $derived.by(() => {
        if (this.line === null) return 0;
        if (!this.frames) return LINE_HEIGHT * 2; // loading / error placeholder
        let h = FRAME_BLOCK_PAD * 2;
        for (const frame of this.frames) {
            h += LINE_HEIGHT; // per-frame label
            h += frame.length * LINE_HEIGHT; // the frame's rows
        }
        if (this.frames.length > 1) h += FRAME_GAP * (this.frames.length - 1);
        return Math.min(h, FRAME_BLOCK_MAX);
    });

    collapse(): void {
        this.line = null;
        this.frames = null;
        this.failed = false;
    }

    async toggle(lineNum: number): Promise<void> {
        if (this.line === lineNum) {
            this.collapse();
            return;
        }
        this.line = lineNum;
        this.frames = null;
        this.failed = false;
        const fetchHistory = this.#fetch();
        if (!fetchHistory) return;
        try {
            const frames = await fetchHistory(lineNum);
            if (this.line === lineNum) this.frames = frames;
        } catch {
            if (this.line === lineNum) this.failed = true;
        }
    }
}
