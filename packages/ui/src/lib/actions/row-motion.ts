// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { cubicOut } from "svelte/easing";
import { flip, type AnimationConfig } from "svelte/animate";
import type { TransitionConfig } from "svelte/transition";

const LEAVE_MS = 160;
// Rows closing the gap wait for most of the fade, so they don't slide over a
// row that is still visible.
const CLOSE_GAP_DELAY_MS = 110;
const SHIFT_MS = 200;

export function prefersReducedMotion(): boolean {
    return matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/**
 * Plays the live-arrival cue (`.run-arrive` in theme.css) on a list row that
 * mounts for a freshly arrived run. Mount-only, so the run's next re-render
 * (pending → running) can't cut the animation short.
 */
export function arrival(node: HTMLElement, fresh: boolean) {
    if (fresh) node.classList.add("run-arrive");
}

/**
 * Outro for a run row (`out:leave={motion?.removed}`): fades and sweeps the
 * row out when `removed` says its run (read from `data-run-id`) left live.
 * Rows leaving for any other reason (scrolled out of the virtual window, a
 * filter change) go instantly. Also sets `--rw-shift-delay` on the list so
 * rows sliding up with a CSS transition start after the fade.
 */
export function leave(
    node: HTMLElement,
    removed: ((runId: string) => boolean) | undefined,
): TransitionConfig {
    const runId = node.dataset.runId;
    if (!runId || removed?.(runId) !== true || prefersReducedMotion()) return { duration: 0 };
    const list = node.parentElement;
    if (list) {
        list.style.setProperty("--rw-shift-delay", `${String(CLOSE_GAP_DELAY_MS)}ms`);
        setTimeout(
            () => list.style.removeProperty("--rw-shift-delay"),
            CLOSE_GAP_DELAY_MS + SHIFT_MS,
        );
    }
    return {
        duration: LEAVE_MS,
        easing: cubicOut,
        css: (t) => `opacity: ${String(t)}; translate: ${String((1 - t) * -12)}px 0`,
    };
}

/**
 * `animate:` for plain (non-virtual) run lists: flip, except rows moving up to
 * close a removed row's gap wait for its fade.
 */
export function shift(node: Element, rects: { from: DOMRect; to: DOMRect }): AnimationConfig {
    if (prefersReducedMotion()) return { duration: 0 };
    return flip(node, rects, {
        duration: SHIFT_MS,
        delay: rects.to.top < rects.from.top ? CLOSE_GAP_DELAY_MS : 0,
    });
}
