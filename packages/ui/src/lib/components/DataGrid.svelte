<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module lang="ts">
    import type { Snippet } from "svelte";
    import { resolvePath } from "../utils/filter.js";

    export interface Column<T> {
        /** Field shown and sorted on. Dot-paths ("user.name") read nested fields. */
        key: keyof T | string;
        label: string;
        sortable?: boolean;
        width?: string;
        align?: "left" | "center" | "right";
        render?: Snippet<[T]>;
    }

    export interface DataGridProps<T> {
        columns: Column<T>[];
        data: T[];
        selectable?: boolean;
        selectedRows?: T[];
        sortKey?: string;
        sortDirection?: "asc" | "desc";
        /** Take over sorting (e.g. server-side). When set, the grid only reports
         *  the clicked key and never reorders `data` itself. */
        onSort?: (key: string) => void;
        paginate?: boolean;
        page?: number;
        pageSize?: number;
        pageSizeOptions?: number[];
        /** Server mode: the full row count. `data` is then the current page as
         *  delivered, so the grid skips its own sort and slicing. Filter with
         *  `FilterBar` + `applyFilters` before handing rows to the grid. */
        total?: number;
        onPageChange?: (page: number) => void;
        onPageSizeChange?: (pageSize: number) => void;
        onSelect?: (rows: T[]) => void;
        emptyState?: Snippet;
        emptyMessage?: string;
        striped?: boolean;
        hoverable?: boolean;
        compact?: boolean;
        rowKey?: keyof T;
        rowAction?: Snippet<[T]>;
        onRowClick?: (row: T) => void;
        loading?: boolean;
        stickyHeader?: boolean;
        /** Hide the header row entirely, for single-column feeds (e.g. an
         *  inbox) where a column label would be noise. */
        showHeader?: boolean;
        /** Rendered below the table body, inside the frame, e.g. a cursor
         *  "Load more" button for feeds that don't use offset pagination. */
        footer?: Snippet;
        /** Drop the outer border/shadow/bg so the grid sits flush inside a Card
         *  or panel that already supplies its own frame. */
        bare?: boolean;
        /** Extra classes per row, e.g. a left accent bar for a failing row.
         *  Rows always carry `group`, so a `render`/`rowAction` snippet can use
         *  `group-hover:` to reveal on-hover controls. */
        rowClass?: (row: T) => string;
        class?: string;
        wrapperClass?: string;
    }

    // Type-aware comparator. Nullish sorts last; numbers numerically; dates by
    // epoch; everything else by locale with numeric-aware string compare.
    // Returns 0 for non-comparable/equal, so a column whose key maps to no real
    // field (a display-only key) leaves order untouched, sort stays inert there.
    function compareValues(a: unknown, b: unknown): number {
        if (a == null && b == null) return 0;
        if (a == null) return 1;
        if (b == null) return -1;
        if (typeof a === "number" && typeof b === "number") return a - b;
        if (a instanceof Date && b instanceof Date) return a.getTime() - b.getTime();
        return String(a).localeCompare(String(b), undefined, { numeric: true });
    }
</script>

<script lang="ts" generics="T extends object">
    import { ArrowUp, ArrowDown, ArrowUpDown } from "@lucide/svelte";
    import Checkbox from "./Checkbox.svelte";
    import Pagination from "./Pagination.svelte";
    import Spinner from "./Spinner.svelte";
    import Table from "./Table.svelte";
    import TableBody from "./TableBody.svelte";
    import TableCell from "./TableCell.svelte";
    import TableHead from "./TableHead.svelte";
    import TableRow from "./TableRow.svelte";
    import { selectionState } from "./data-grid-selection.js";

    let {
        columns,
        data,
        selectable = false,
        selectedRows = $bindable([]),
        sortKey = $bindable(undefined),
        sortDirection = $bindable(undefined),
        onSort,
        paginate = false,
        page = $bindable(1),
        pageSize = $bindable(20),
        pageSizeOptions,
        total,
        onPageChange,
        onPageSizeChange,
        onSelect,
        emptyState,
        emptyMessage = "No data available",
        striped = false,
        hoverable = true,
        compact = false,
        rowKey = "id" as keyof T,
        rowAction,
        onRowClick,
        loading = false,
        stickyHeader = false,
        showHeader = true,
        footer,
        bare = false,
        rowClass,
        class: className = "",
        wrapperClass,
    }: DataGridProps<T> = $props();

    function toggleAll() {
        if (allSelected) {
            selectedRows = [];
        } else {
            selectedRows = [...pagedData];
        }
        onSelect?.(selectedRows);
    }

    function toggleRow(row: T) {
        const idx = selectedRows.findIndex((r) => r[rowKey] === row[rowKey]);
        if (idx >= 0) {
            selectedRows = selectedRows.filter((_, i) => i !== idx);
        } else {
            selectedRows = [...selectedRows, row];
        }
        onSelect?.(selectedRows);
    }

    function isSelected(row: T): boolean {
        return selectedRows.some((r) => r[rowKey] === row[rowKey]);
    }

    function handleSort(key: string) {
        if (onSort) {
            onSort(key);
            return;
        }

        if (sortKey === key) {
            sortDirection = sortDirection === "asc" ? "desc" : "asc";
        } else {
            sortKey = key;
            sortDirection = "asc";
        }
    }

    // Client-side sort. Off in server mode (`total` set) or when the parent
    // drives sorting via `onSort`; otherwise reorder by the active key so a
    // sortable header actually sorts. Stable + inert on display-only keys.
    const sortedData = $derived.by(() => {
        if (onSort || typeof total === "number" || !sortKey || !sortDirection) {
            return data;
        }
        const dir = sortDirection === "desc" ? -1 : 1;
        const key = sortKey;
        return [...data].sort(
            (a, b) => dir * compareValues(resolvePath(a, key), resolvePath(b, key)),
        );
    });

    const totalItems = $derived(total ?? sortedData.length);
    const totalPages = $derived(Math.max(1, Math.ceil(totalItems / Math.max(1, pageSize))));

    $effect(() => {
        if (!paginate) return;
        if (page < 1) page = 1;
        if (totalItems > 0 && page > totalPages) page = totalPages;
    });

    const pagedData = $derived.by(() => {
        if (typeof total === "number" || !paginate) return sortedData;
        const start = (page - 1) * pageSize;
        return sortedData.slice(start, start + pageSize);
    });

    const selection = $derived(selectionState(pagedData, selectedRows, rowKey));
    let allSelected = $derived(selection.allSelected);
    let someSelected = $derived(selection.someSelected);

    // Cell rhythm, airy by default, tight when compact. Header + body share it.
    const density = $derived(compact ? "compact" : "comfortable");
    // Hover reveals a teal accent rail on the leftmost cell (see .group on <tr>).
    const railHover = "group-hover:shadow-[inset_3px_0_0_var(--color-primary)]";
</script>

<div
    class="relative flex flex-col {bare
        ? ''
        : 'rounded-[4px] border border-outline bg-surface-raised shadow-sm'} {className}"
>
    <div
        class="relative min-h-0 flex-1 rounded-[4px] {wrapperClass ??
            (stickyHeader ? '' : 'overflow-x-auto')}"
    >
        {#if loading}
            <div
                class="absolute inset-0 z-10 flex items-center justify-center bg-surface-raised/50 backdrop-blur-[1px]"
            >
                <div
                    class="flex items-center gap-3 rounded-full border border-outline bg-surface-raised px-4 py-2 shadow-md"
                >
                    <Spinner size="sm" class="text-primary" />
                    <span class="font-mono text-sm text-on-surface-muted">Loading...</span>
                </div>
            </div>
        {/if}

        <Table bare>
            {#if showHeader}
                <TableHead
                    class={stickyHeader
                        ? "sticky top-0 z-10 bg-surface-raised/95 backdrop-blur-sm"
                        : ""}
                >
                    {#if selectable}
                        <TableCell header {density} class="w-12">
                            <Checkbox
                                checked={allSelected}
                                indeterminate={someSelected}
                                onchange={toggleAll}
                                size="sm"
                            />
                        </TableCell>
                    {/if}
                    {#each columns as column (column.key)}
                        {@const sorted = sortKey === column.key}
                        <TableCell
                            header
                            {density}
                            align={column.align}
                            aria-sort={sorted
                                ? sortDirection === "asc"
                                    ? "ascending"
                                    : "descending"
                                : undefined}
                            class="group"
                            style={column.width ? `width: ${column.width}` : undefined}
                        >
                            {#if column.sortable}
                                <button
                                    onclick={() => handleSort(String(column.key))}
                                    class="
                                        inline-flex items-center gap-1 group-hover:text-on-surface
                                        {sorted ? 'text-on-surface' : ''}
                                    "
                                >
                                    {column.label}
                                    {#if sorted}
                                        {#if sortDirection === "asc"}
                                            <ArrowUp size={14} class="text-primary" />
                                        {:else}
                                            <ArrowDown size={14} class="text-primary" />
                                        {/if}
                                    {:else}
                                        <ArrowUpDown
                                            size={14}
                                            class="text-on-surface-faint opacity-0 group-hover:opacity-100"
                                        />
                                    {/if}
                                </button>
                            {:else}
                                {column.label}
                            {/if}
                        </TableCell>
                    {/each}
                    {#if rowAction}
                        <TableCell header {density} class="w-16" />
                    {/if}
                </TableHead>
            {/if}
            <TableBody>
                {#if pagedData.length === 0 && !loading}
                    <TableRow border="none">
                        <TableCell
                            colspan={columns.length + (selectable ? 1 : 0) + (rowAction ? 1 : 0)}
                            density="none"
                            align="center"
                            class="px-4 py-12 text-on-surface-muted"
                        >
                            {#if emptyState}
                                {@render emptyState()}
                            {:else}
                                {emptyMessage}
                            {/if}
                        </TableCell>
                    </TableRow>
                {:else}
                    {#each pagedData as row, idx (row[rowKey])}
                        <TableRow
                            border="faint"
                            {hoverable}
                            class="
                                group
                                {striped && idx % 2 === 1 ? 'bg-surface-sunken/30' : ''}
                                {isSelected(row) ? 'bg-primary-soft/30' : ''}
                                {onRowClick ? 'cursor-pointer' : ''}
                                {rowClass ? rowClass(row) : ''}
                            "
                            onclick={() => onRowClick?.(row)}
                        >
                            {#if selectable}
                                <!-- stopPropagation: a selection click must never also
                                     fire onRowClick (which usually navigates away). -->
                                <TableCell
                                    {density}
                                    class="cursor-pointer {hoverable ? railHover : ''}"
                                    onclick={(e) => {
                                        e.stopPropagation();
                                        if (!(e.target instanceof HTMLInputElement)) toggleRow(row);
                                    }}
                                >
                                    <Checkbox
                                        checked={isSelected(row)}
                                        size="sm"
                                        class="pointer-events-none"
                                    />
                                </TableCell>
                            {/if}
                            {#each columns as column, ci (column.key)}
                                <TableCell
                                    {density}
                                    align={column.align}
                                    class="text-on-surface-muted {ci === 0 &&
                                    !selectable &&
                                    hoverable
                                        ? railHover
                                        : ''}"
                                >
                                    {#if column.render}
                                        {@render column.render(row)}
                                    {:else}
                                        <!-- Raw field value: a token, so mono. Snippet-rendered
                                             cells choose their own voice. -->
                                        <span class="font-mono"
                                            >{resolvePath(row, String(column.key)) ?? "-"}</span
                                        >
                                    {/if}
                                </TableCell>
                            {/each}
                            {#if rowAction}
                                <TableCell {density} align="right">
                                    {@render rowAction(row)}
                                </TableCell>
                            {/if}
                        </TableRow>
                    {/each}
                {/if}
            </TableBody>
        </Table>
    </div>

    {#if footer}
        <div class="border-t border-outline p-3">
            {@render footer()}
        </div>
    {/if}

    {#if paginate}
        <div class="border-t border-outline p-3">
            <Pagination
                {page}
                {pageSize}
                {pageSizeOptions}
                {totalItems}
                onPageChange={(p) => {
                    page = p;
                    onPageChange?.(p);
                }}
                onPageSizeChange={(s) => {
                    pageSize = s;
                    page = 1;
                    onPageSizeChange?.(s);
                }}
            />
        </div>
    {/if}
</div>
