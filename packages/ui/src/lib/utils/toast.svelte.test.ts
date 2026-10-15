// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "./toast.svelte";

beforeEach(() => {
    vi.useFakeTimers();
});

afterEach(() => {
    for (const t of toast.items) toast.remove(t.id);
    vi.useRealTimers();
});

describe("toast", () => {
    it("expires after its duration", () => {
        toast.success("Saved", { duration: 1000 });
        expect(toast.items.map((t) => [t.type, t.message])).toEqual([["success", "Saved"]]);
        vi.advanceTimersByTime(999);
        expect(toast.items).toHaveLength(1);
        vi.advanceTimersByTime(1);
        expect(toast.items).toHaveLength(0);
    });

    it("folds a repeat into the shown toast and restarts its clock", () => {
        const first = toast.error("Run failed", { duration: 1000 });
        vi.advanceTimersByTime(800);
        expect(toast.error("Run failed", { duration: 1000 })).toBe(first);
        vi.advanceTimersByTime(800);
        expect(toast.items).toHaveLength(1);
        vi.advanceTimersByTime(200);
        expect(toast.items).toHaveLength(0);
    });

    it("keeps a zero-duration toast until it is removed", () => {
        const onClick = vi.fn();
        const id = toast.info("Deleted run", { duration: 0, action: { label: "Undo", onClick } });
        toast.warning("Disk almost full");
        vi.advanceTimersByTime(60_000);
        expect(toast.items.map((t) => t.id)).toEqual([id]);
        expect(toast.items[0]?.action?.label).toBe("Undo");
        toast.remove(id);
        expect(toast.items).toHaveLength(0);
    });
});
