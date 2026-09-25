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
        /** Built-in fuzzy search box above the table. For typed filters use
         *  `FilterBar` + `applyFilters` outside the grid instead. */
        filterable?: boolean;
        filterPlaceholder?: string;
        filterKeys?: string[];
        filterQuery?: string;
        paginate?: boolean;
        page?: number;
        pageSize?: number;
        pageSizeOptions?: number[];
        /** Server mode: the full row count. `data` is then the current page as
         *  delivered, so the grid skips its own filter, sort, and slicing. */
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
        /** Hide the header row entirely — for single-column feeds (e.g. an
         *  inbox) where a column label would be noise. */
        showHeader?: boolean;
        /** Rendered below the table body, inside the frame — e.g. a cursor
         *  "Load more" button for feeds that don't use offset pagination. */
        footer?: Snippet;
        /** Drop the outer border/shadow/bg so the grid sits flush inside a Card
         *  or panel that already supplies its own frame. */
        bare?: boolean;
        /** Extra classes per row — e.g. a left accent bar for a failing row.
         *  Rows always carry `group`, so a `render`/`rowAction` snippet can use
         *  `group-hover:` to reveal on-hover controls. */
        rowClass?: (row: T) => string;
        class?: string;
        wrapperClass?: string;
    }

    // Type-aware comparator. Nullish sorts last; numbers numerically; dates by
    // epoch; everything else by locale with numeric-aware string compare.
    // Returns 0 for non-comparable/equal, so a column whose key maps to no real
    // field (a display-only key) leaves order untouched — sort stays inert there.
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
    import Input from "./Input.svelte";
    import Pagination from "./Pagination.svelte";
    import Spinner from "./Spinner.svelte";
    import Fuse from "fuse.js";
    import { selectionState } from "./data-grid-selection.js";

    let {
        columns,
        data,
        selectable = false,
        selectedRows = $bindable([]),
        sortKey = $bindable(undefined),
        sortDirection = $bindable(undefined),
        onSort,
        filterable = false,
        filterPlaceholder = "Filter…",
        filterKeys,
        filterQuery = $bindable(""),
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

    const searchableKeys = $derived(
        filterKeys?.length ? filterKeys : columns.map((c) => String(c.key)),
    );

    const fuse = $derived(
        new Fuse(data, {
            keys: searchableKeys,
            threshold: 0.3,
            ignoreLocation: true,
        }),
    );

    const filteredData = $derived.by(() => {
        if (typeof total === "number") return data;

        if (!filterable || !filterQuery.trim()) return data;

        return fuse.search(filterQuery).map((r) => r.item);
    });

    // Client-side sort. Off in server mode (`total` set) or when the parent
    // drives sorting via `onSort`; otherwise reorder by the active key so a
    // sortable header actually sorts. Stable + inert on display-only keys.
    const sortedData = $derived.by(() => {
        if (onSort || typeof total === "number" || !sortKey || !sortDirection) {
            return filteredData;
        }
        const dir = sortDirection === "desc" ? -1 : 1;
        const key = sortKey;
        return [...filteredData].sort(
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

    // Cell rhythm — airy by default, tight when compact. Header + body share it.
    const cellPad = $derived(compact ? "px-3 py-2" : "px-5 py-3.5");
    // Hover reveals a teal accent rail on the leftmost cell (see .group on <tr>).
    const railHover = "group-hover:shadow-[inset_3px_0_0_var(--color-primary)]";
</script>

<div
    class="relative flex flex-col {bare
        ? ''
        : 'rounded-[4px] border border-outline bg-surface-raised shadow-sm'} {className}"
>
    {#if filterable}
        <div class="border-b border-outline p-3">
            <Input
                value={filterQuery}
                oninput={(e) => {
                    filterQuery = e.currentTarget.value;
                    if (paginate && !total) page = 1;
                }}
                type="search"
                placeholder={filterPlaceholder}
                size="sm"
                class="max-w-sm"
            />
        </div>
    {/if}

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

        <table class="w-full text-sm">
            {#if showHeader}
                <thead
                    class={stickyHeader
                        ? "sticky top-0 z-10 bg-surface-raised/95 backdrop-blur-sm"
                        : ""}
                >
                    <tr class="border-b border-outline text-left">
                        {#if selectable}
                            <th class="w-12 {cellPad}">
                                <Checkbox
                                    checked={allSelected}
                                    indeterminate={someSelected}
                                    onchange={toggleAll}
                                    size="sm"
                                />
                            </th>
                        {/if}
                        {#each columns as column (column.key)}
                            <th
                                class="
                                    group {cellPad}
                                    font-mono text-xs tracking-[0.08em] text-on-surface-faint uppercase
                                    {column.align === 'center' ? 'text-center' : ''}
                                    {column.align === 'right' ? 'text-right' : ''}
                                    {sortKey === column.key
                                    ? 'text-on-surface shadow-[inset_0_-2px_0_var(--color-primary)]'
                                    : ''}
                                "
                                style={column.width ? `width: ${column.width}` : ""}
                            >
                                {#if column.sortable}
                                    <button
                                        onclick={() => handleSort(String(column.key))}
                                        class="
                                            inline-flex items-center gap-1 group-hover:text-on-surface
                                            {sortKey === column.key ? 'text-on-surface' : ''}
                                        "
                                    >
                                        {column.label}
                                        {#if sortKey === column.key}
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
                            </th>
                        {/each}
                        {#if rowAction}
                            <th class="w-16 {cellPad}"></th>
                        {/if}
                    </tr>
                </thead>
            {/if}
            <tbody>
                {#if pagedData.length === 0 && !loading}
                    <tr>
                        <td
                            colspan={columns.length + (selectable ? 1 : 0) + (rowAction ? 1 : 0)}
                            class="px-4 py-12 text-center text-on-surface-muted"
                        >
                            {#if emptyState}
                                {@render emptyState()}
                            {:else}
                                {emptyMessage}
                            {/if}
                        </td>
                    </tr>
                {:else}
                    {#each pagedData as row, idx (row[rowKey])}
                        <tr
                            class="
                                group border-b border-outline-faint last:border-b-0
                                {striped && idx % 2 === 1 ? 'bg-surface-sunken/30' : ''}
                                {hoverable ? 'hover:bg-surface-sunken' : ''}
                                {isSelected(row) ? 'bg-primary-soft/30' : ''}
                                {onRowClick ? 'cursor-pointer' : ''}
                                {rowClass ? rowClass(row) : ''}
                            "
                            onclick={() => onRowClick?.(row)}
                        >
                            {#if selectable}
                                <!-- stopPropagation: a selection click must never also
                                     fire onRowClick (which usually navigates away). -->
                                <td
                                    class="{cellPad} cursor-pointer {hoverable ? railHover : ''}"
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
                                </td>
                            {/if}
                            {#each columns as column, ci (column.key)}
                                <td
                                    class="
                                        {cellPad} text-on-surface-muted
                                        {column.align === 'center' ? 'text-center' : ''}
                                        {column.align === 'right' ? 'text-right' : ''}
                                        {ci === 0 && !selectable && hoverable ? railHover : ''}
                                    "
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
                                </td>
                            {/each}
                            {#if rowAction}
                                <td class="{cellPad} text-right">
                                    {@render rowAction(row)}
                                </td>
                            {/if}
                        </tr>
                    {/each}
                {/if}
            </tbody>
        </table>
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
