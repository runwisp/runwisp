// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RunMotion } from "./run-motion.js";

const WINDOW_MS = 2000;

describe("RunMotion", () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it("reports nothing for a run it was never told about", () => {
        const motion = new RunMotion();
        expect(motion.arrived("run-1")).toBe(false);
        expect(motion.removed("run-1")).toBe(false);
    });

    it("keeps arrivals and removals apart", () => {
        const motion = new RunMotion();
        motion.markArrived("arrived-run");
        motion.markRemoved("removed-run");
        expect(motion.arrived("arrived-run")).toBe(true);
        expect(motion.removed("arrived-run")).toBe(false);
        expect(motion.removed("removed-run")).toBe(true);
        expect(motion.arrived("removed-run")).toBe(false);
    });

    it("forgets a run once the window passes, so a later click doesn't animate it", () => {
        const motion = new RunMotion();
        motion.markArrived("run-1");
        motion.markRemoved("run-2");
        vi.advanceTimersByTime(WINDOW_MS - 1);
        expect(motion.arrived("run-1")).toBe(true);
        expect(motion.removed("run-2")).toBe(true);
        vi.advanceTimersByTime(1);
        expect(motion.arrived("run-1")).toBe(false);
        expect(motion.removed("run-2")).toBe(false);
    });

    it("prunes expired runs when a later one is marked", () => {
        const motion = new RunMotion();
        motion.markArrived("old-run");
        vi.advanceTimersByTime(WINDOW_MS);
        motion.markArrived("new-run");
        expect(motion.arrived("old-run")).toBe(false);
        expect(motion.arrived("new-run")).toBe(true);
    });

    it("re-marking a run restarts its window", () => {
        const motion = new RunMotion();
        motion.markArrived("run-1");
        vi.advanceTimersByTime(WINDOW_MS - 1);
        motion.markArrived("run-1");
        vi.advanceTimersByTime(WINDOW_MS - 1);
        expect(motion.arrived("run-1")).toBe(true);
    });

    it("clear forgets everything", () => {
        const motion = new RunMotion();
        motion.markArrived("arrived-run");
        motion.markRemoved("removed-run");
        motion.clear();
        expect(motion.arrived("arrived-run")).toBe(false);
        expect(motion.removed("removed-run")).toBe(false);
    });
});
