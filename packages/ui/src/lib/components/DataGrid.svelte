<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module lang="ts">
    import type { Snippet } from "svelte";

    export interface Column<T> {
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
        onSort?: (key: string) => void;
        filterable?: boolean;
        filterPlaceholder?: string;
        filterKeys?: string[];
        filterQuery?: string;
        paginate?: boolean;
        page?: number;
        pageSize?: number;
        pageSizeOptions?: number[];
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
        class?: string;
        wrapperClass?: string;
    }
</script>

<script lang="ts" generics="T extends object">
    import { ArrowUp, ArrowDown, ArrowUpDown } from "@lucide/svelte";
    import Checkbox from "./Checkbox.svelte";
    import Input from "./Input.svelte";
    import Pagination from "./Pagination.svelte";
    import Fuse from "fuse.js";

    import dlv from "dlv";

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

    const totalItems = $derived(total ?? filteredData.length);
    const totalPages = $derived(Math.max(1, Math.ceil(totalItems / Math.max(1, pageSize))));

    $effect(() => {
        if (!paginate) return;
        if (page < 1) page = 1;
        if (totalItems > 0 && page > totalPages) page = totalPages;
    });

    const pagedData = $derived.by(() => {
        if (typeof total === "number") return filteredData;
        if (!paginate) return filteredData;
        const start = (page - 1) * pageSize;
        return filteredData.slice(start, start + pageSize);
    });

    let allSelected = $derived(pagedData.length > 0 && selectedRows.length === pagedData.length);
    let someSelected = $derived(selectedRows.length > 0 && selectedRows.length < pagedData.length);
</script>

<div
    class="relative flex flex-col rounded-[4px] border border-outline bg-surface-raised shadow-sm {className}"
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
                    <svg class="h-5 w-5 animate-spin text-primary" viewBox="0 0 24 24" fill="none">
                        <circle
                            class="opacity-25"
                            cx="12"
                            cy="12"
                            r="10"
                            stroke="currentColor"
                            stroke-width="4"
                        ></circle>
                        <path
                            class="opacity-75"
                            fill="currentColor"
                            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                        ></path>
                    </svg>
                    <span class="font-mono text-sm text-on-surface-muted">Loading...</span>
                </div>
            </div>
        {/if}

        <table class="w-full text-sm">
            <thead
                class={stickyHeader
                    ? "sticky top-0 z-10 bg-surface-sunken/95 backdrop-blur-sm"
                    : "bg-surface-sunken/50"}
            >
                <tr class="border-b border-outline text-left">
                    {#if selectable}
                        <th class="w-12 px-4 py-3">
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
							    group px-4 {compact ? 'py-2' : 'py-3'}
							    font-mono text-xs tracking-wide text-on-surface-faint uppercase
							    {column.align === 'center' ? 'text-center' : ''}
							    {column.align === 'right' ? 'text-right' : ''}
						    "
                            style={column.width ? `width: ${column.width}` : ""}
                        >
                            {#if column.sortable}
                                <button
                                    onclick={() => handleSort(column.key as string)}
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
                        <th class="w-16 px-4 py-3"></th>
                    {/if}
                </tr>
            </thead>
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
                    {#each pagedData as row, idx (idx)}
                        <tr
                            class="
							    border-b border-outline last:border-b-0
							    {striped && idx % 2 === 1 ? 'bg-surface-sunken/30' : ''}
							    {hoverable ? 'hover:bg-surface-sunken' : ''}
							    {isSelected(row) ? 'bg-primary-soft/30' : ''}
							    {onRowClick ? 'cursor-pointer' : ''}
							    						    "
                            onclick={() => onRowClick?.(row)}
                        >
                            {#if selectable}
                                <td
                                    class="px-4 {compact ? 'py-2' : 'py-3'} cursor-pointer"
                                    onclick={(e) => {
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
                            {#each columns as column (column.key)}
                                <td
                                    class="
									    px-4 {compact ? 'py-2' : 'py-3'} text-on-surface-muted
									    {column.align === 'center' ? 'text-center' : ''}
									    {column.align === 'right' ? 'text-right' : ''}
								    "
                                >
                                    {#if column.render}
                                        {@render column.render(row)}
                                    {:else}
                                        <!-- Raw field value: a token, so mono. Snippet-rendered
                                             cells choose their own voice. -->
                                        <span class="font-mono"
                                            >{dlv(row, column.key as string) ?? "-"}</span
                                        >
                                    {/if}
                                </td>
                            {/each}
                            {#if rowAction}
                                <td class="px-4 {compact ? 'py-2' : 'py-3'} text-right">
                                    {@render rowAction(row)}
                                </td>
                            {/if}
                        </tr>
                    {/each}
                {/if}
            </tbody>
        </table>
    </div>

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
