// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { selectionState } from "./data-grid-selection.js";

interface Row {
    id: string;
}

const rows = (...ids: string[]): Row[] => ids.map((id) => ({ id }));

describe("selectionState", () => {
    it("is unchecked when nothing is selected", () => {
        expect(selectionState(rows("a", "b", "c"), [], "id")).toEqual({
            allSelected: false,
            someSelected: false,
        });
    });

    it("is checked when every paged row is selected", () => {
        const paged = rows("a", "b", "c");
        expect(selectionState(paged, paged, "id")).toEqual({
            allSelected: true,
            someSelected: false,
        });
    });

    it("is indeterminate when some but not all paged rows are selected", () => {
        const paged = rows("a", "b", "c");
        expect(selectionState(paged, rows("b"), "id")).toEqual({
            allSelected: false,
            someSelected: true,
        });
    });

    it("is NOT checked when selectedRows matches the paged count but is a different set of rows", () => {
        // Regression: page 1 selected [a, b, c]; the grid then paged/filtered
        // to a different page showing [d, e, f]. A count comparison
        // (selectedRows.length === pagedRows.length) would wrongly report
        // "all selected" here even though none of the visible rows are
        // members of the selection.
        const paged = rows("d", "e", "f");
        const selected = rows("a", "b", "c");
        expect(selectionState(paged, selected, "id")).toEqual({
            allSelected: false,
            someSelected: false,
        });
    });

    it("is indeterminate, not checked, when the overlap coincidentally matches the paged count minus one", () => {
        const paged = rows("a", "b", "c");
        // Carried-over selection from another page plus one row that is
        // actually on this page.
        const selected = rows("x", "y", "b");
        expect(selectionState(paged, selected, "id")).toEqual({
            allSelected: false,
            someSelected: true,
        });
    });

    it("is unchecked when there are no paged rows, regardless of selection", () => {
        expect(selectionState([], rows("a"), "id")).toEqual({
            allSelected: false,
            someSelected: false,
        });
    });
});
