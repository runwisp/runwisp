// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { arrival, leave, prefersReducedMotion } from "./row-motion.js";

const LEAVE_MS = 160;
const CLOSE_GAP_DELAY_MS = 110;
const SHIFT_MS = 200;
const SHIFT_DELAY = "--rw-shift-delay";

// The helpers read the reduced-motion preference off `matchMedia` and write to
// the narrow element slices they declare, so a stubbed global and plain objects
// stand in for the browser (this repo's vitest env has no jsdom).
function setReducedMotion(reduce: boolean): void {
    vi.stubGlobal("matchMedia", (media: string) => ({ media, matches: reduce }));
}

/** A list element recording the custom properties a leaving row sets on it. */
function listNode() {
    const props = new Map<string, string>();
    return {
        props,
        style: {
            setProperty: (name: string, value: string | null) => {
                props.set(name, value ?? "");
            },
            removeProperty: (name: string) => {
                const previous = props.get(name) ?? "";
                props.delete(name);
                return previous;
            },
        },
    };
}

describe("prefersReducedMotion", () => {
    afterEach(() => vi.unstubAllGlobals());

    it("reports what the reduced-motion media query matches", () => {
        setReducedMotion(true);
        expect(prefersReducedMotion()).toBe(true);
        setReducedMotion(false);
        expect(prefersReducedMotion()).toBe(false);
    });
});

describe("arrival", () => {
    it("plays the arrival cue on a row mounting for a fresh run", () => {
        const added: string[] = [];
        arrival({ classList: { add: (token: string) => added.push(token) } }, true);
        expect(added).toEqual(["run-arrive"]);
    });

    it("leaves a row that mounted for an already-known run alone", () => {
        const added: string[] = [];
        arrival({ classList: { add: (token: string) => added.push(token) } }, false);
        expect(added).toEqual([]);
    });
});

describe("leave", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        setReducedMotion(false);
    });
    afterEach(() => {
        vi.useRealTimers();
        vi.unstubAllGlobals();
    });

    it("fades out a run removed live and delays the rows closing its gap", () => {
        const list = listNode();
        const config = leave({ dataset: { runId: "run-1" }, parentElement: list }, () => true);

        expect(config.duration).toBe(LEAVE_MS);
        expect(list.props.get(SHIFT_DELAY)).toBe("110ms");

        // Halfway through: half faded, halfway back to its resting offset.
        expect(config.css?.(0.5, 0.5)).toBe("opacity: 0.5; translate: -6px 0");

        // The delay is a one-shot for this removal, not a lasting list style.
        vi.advanceTimersByTime(CLOSE_GAP_DELAY_MS + SHIFT_MS);
        expect(list.props.has(SHIFT_DELAY)).toBe(false);
    });

    it("still animates a row with no list element around it", () => {
        const config = leave({ dataset: { runId: "run-1" }, parentElement: null }, () => true);
        expect(config.duration).toBe(LEAVE_MS);
    });

    it("goes instantly when the run left for any reason other than a live removal", () => {
        const list = listNode();
        const scrolledOut = leave(
            { dataset: { runId: "run-1" }, parentElement: list },
            () => false,
        );
        const noTracker = leave({ dataset: { runId: "run-1" }, parentElement: list }, undefined);
        const noRunId = leave({ dataset: {}, parentElement: list }, () => true);

        expect(scrolledOut.duration).toBe(0);
        expect(noTracker.duration).toBe(0);
        expect(noRunId.duration).toBe(0);
        expect(list.props.has(SHIFT_DELAY)).toBe(false);
    });

    it("goes instantly when the operator prefers reduced motion", () => {
        setReducedMotion(true);
        const list = listNode();
        const config = leave({ dataset: { runId: "run-1" }, parentElement: list }, () => true);

        expect(config.duration).toBe(0);
        expect(list.props.has(SHIFT_DELAY)).toBe(false);
    });
});
