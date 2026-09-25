// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { debounce } from "./debounce.js";

describe("debounce", () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it("collapses rapid calls into one trailing call with the last args", () => {
        const fn = vi.fn();
        const d = debounce(fn, 20);
        d(1);
        d(2);
        d(3);
        expect(fn).not.toHaveBeenCalled();
        vi.advanceTimersByTime(20);
        expect(fn).toHaveBeenCalledTimes(1);
        expect(fn).toHaveBeenCalledWith(3);
    });

    it("cancel drops the pending call", () => {
        const fn = vi.fn();
        const d = debounce(fn, 20);
        d();
        d.cancel();
        vi.advanceTimersByTime(40);
        expect(fn).not.toHaveBeenCalled();
    });
});
