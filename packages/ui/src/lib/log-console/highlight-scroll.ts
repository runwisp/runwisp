// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { untrack } from "svelte";

export interface HighlightScrollOptions {
    /** Reactive getter for the line to reveal; null/undefined means none. */
    line: () => number | null | undefined;
    /** Reactive getter: true once the layout can place `line` (container measured, line exists). */
    ready: (line: number) => boolean;
    /** Scrolls to `line`. Runs untracked, so layout reads here never re-trigger the sync. */
    reveal: (line: number) => void;
    /** Called when the highlight is cleared. */
    clear: () => void;
}

/**
 * Returns a function to call from inside an `$effect`. It reveals each
 * highlighted line exactly once: after the first reveal, layout changes (new
 * log lines, resizes) no longer re-run the scroll, so the operator can scroll
 * away from a deep-linked search hit and stay there. It waits until `ready`,
 * and a changed or re-set `line` reveals again.
 */
export function createHighlightScroll(options: HighlightScrollOptions): () => void {
    let revealed: number | null = null;
    return () => {
        const target = options.line();
        if (target === null || target === undefined) {
            revealed = null;
            options.clear();
            return;
        }
        // Checked before `ready` so a revealed line leaves no layout dependency behind.
        if (revealed === target || !options.ready(target)) return;
        revealed = target;
        untrack(() => {
            options.reveal(target);
        });
    };
}
