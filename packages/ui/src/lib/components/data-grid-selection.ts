// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export interface SelectionState {
    allSelected: boolean;
    someSelected: boolean;
}

/**
 * Header checkbox state for `pagedRows`: fully checked only when every row
 * currently on the page is selected, indeterminate when some (but not all)
 * of them are. Matches rows by `rowKey`, not by count: the selection can
 * hold rows from a page the grid has since moved away from.
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
