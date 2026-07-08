// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TickingNow } from "./ticking-now.svelte.js";

describe("TickingNow", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date("2024-06-15T12:00:00.000Z"));
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it("reports the current time before it starts ticking", () => {
        const clock = new TickingNow();
        expect(clock.now.getTime()).toBe(new Date("2024-06-15T12:00:00.000Z").getTime());
    });

    it("advances on its default 30s interval", () => {
        const clock = new TickingNow();
        const stop = clock.start();
        vi.advanceTimersByTime(30_000);
        expect(clock.now.getTime()).toBe(new Date("2024-06-15T12:00:30.000Z").getTime());
        stop();
    });

    it("advances on a custom interval", () => {
        const clock = new TickingNow(1000);
        const stop = clock.start();
        vi.advanceTimersByTime(3000);
        expect(clock.now.getTime()).toBe(new Date("2024-06-15T12:00:03.000Z").getTime());
        stop();
    });

    it("stops advancing once the cleanup is called", () => {
        const clock = new TickingNow(1000);
        const stop = clock.start();
        vi.advanceTimersByTime(1000);
        stop();
        vi.advanceTimersByTime(9000);
        expect(clock.now.getTime()).toBe(new Date("2024-06-15T12:00:01.000Z").getTime());
    });
});
