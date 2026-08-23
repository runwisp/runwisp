// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// DataGrid's header checkbox (checked / indeterminate / unchecked) must
// reflect which rows on the current page are ACTUALLY selected, not merely
// how many rows are selected. A bare count comparison is wrong the moment
// `selectedRows` holds a different set of rows than `pagedRows` but happens
// to be the same size — e.g. rows selected on a page the grid has since
// paged/filtered away from. Extracted so the membership check is unit
// testable without a component-render harness.

export interface SelectionState {
    allSelected: boolean;
    someSelected: boolean;
}

/**
 * Header checkbox state for `pagedRows`: fully checked only when every row
 * currently on the page is selected, indeterminate when some (but not all)
 * of them are. Matches rows by `rowKey`, not by array position or count.
 */
export function selectionState<T>(
    pagedRows: readonly T[],
    selectedRows: readonly T[],
    rowKey: keyof T,
): SelectionState {
    if (pagedRows.length === 0) return { allSelected: false, someSelected: false };
    const selectedKeys = new Set(selectedRows.map((row) => row[rowKey]));
    const selectedOnPage = pagedRows.filter((row) => selectedKeys.has(row[rowKey])).length;
    return {
        allSelected: selectedOnPage === pagedRows.length,
        someSelected: selectedOnPage > 0 && selectedOnPage < pagedRows.length,
    };
}
