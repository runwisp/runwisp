// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { sortByCreatedAtDesc } from "./sort.js";

describe("sortByCreatedAtDesc", () => {
    it("returns newest-first ordering", () => {
        const items = [
            { createdAt: "2024-01-01T00:00:00Z", id: "a" },
            { createdAt: "2024-03-01T00:00:00Z", id: "c" },
            { createdAt: "2024-02-01T00:00:00Z", id: "b" },
        ];
        const sorted = sortByCreatedAtDesc(items);
        expect(sorted.map((x) => x.id)).toEqual(["c", "b", "a"]);
    });

    it("does not mutate the input array", () => {
        const items = [
            { createdAt: "2024-01-01T00:00:00Z", id: "a" },
            { createdAt: "2024-02-01T00:00:00Z", id: "b" },
        ];
        const originalOrder = items.map((x) => x.id);
        sortByCreatedAtDesc(items);
        expect(items.map((x) => x.id)).toEqual(originalOrder);
    });

    it("handles an empty array", () => {
        expect(sortByCreatedAtDesc([])).toEqual([]);
    });

    it("preserves a single item", () => {
        const items = [{ createdAt: "2024-01-01T00:00:00Z", id: "only" }];
        expect(sortByCreatedAtDesc(items).map((x) => x.id)).toEqual(["only"]);
    });
});
