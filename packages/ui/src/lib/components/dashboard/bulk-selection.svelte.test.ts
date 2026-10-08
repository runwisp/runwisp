// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { BulkSelection } from "./bulk-selection.svelte.js";

describe("BulkSelection", () => {
    it("tracks explicitly picked rows", () => {
        const sel = new BulkSelection();
        sel.toggle("a");
        sel.toggle("b");
        sel.toggle("a");
        expect(sel.isSelected("a")).toBe(false);
        expect(sel.isSelected("b")).toBe(true);
        expect(sel.allSelected).toBe(false);
        expect(sel.selector({}, ["b"])).toEqual({ matchAll: false, ids: ["b"] });
    });

    it("selects everything except opt-outs in all mode", () => {
        const sel = new BulkSelection();
        sel.toggle("x");
        sel.selectAll();
        expect(sel.isSelected("x")).toBe(true);
        expect(sel.allSelected).toBe(true);
        sel.toggle("x");
        expect(sel.isSelected("x")).toBe(false);
        expect(sel.allSelected).toBe(false);
        expect(sel.selector({ taskName: "t" }, ["ignored"])).toEqual({
            matchAll: true,
            filter: { taskName: "t" },
            exceptIds: ["x"],
        });
    });

    it("clears back to nothing selected", () => {
        const sel = new BulkSelection();
        sel.selectAll();
        sel.clear();
        expect(sel.isSelected("a")).toBe(false);
        expect(sel.allSelected).toBe(false);
    });
});
