<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts" module>
    export type RunsListSortDirection = "asc" | "desc" | "";

    export interface RunsListFilters {
        search: string;
        status: string;
        sort_direction: RunsListSortDirection;
    }
</script>

<script lang="ts">
    import {
        Clock,
        Timer,
        ArrowUpDown,
        Search,
        Funnel,
        Square,
        Trash2,
        RotateCw,
    } from "@lucide/svelte";
    import { untrack } from "svelte";
    import { SvelteSet } from "svelte/reactivity";
    import { createVirtualizer } from "@tanstack/svelte-virtual";
    import Button from "../Button.svelte";
    import Badge from "../Badge.svelte";
    import EmptyState from "../EmptyState.svelte";
    import type { Run } from "./types.js";
    import type { RunSelector } from "@runwisp/common";
    import { getRunStatusConfig, runDisplayStatus } from "./status-config.js";
    import { runDuration, formatTriggeredByLabel, runRetryLabel } from "./run-helpers.js";
    import { formatRelativeTime, formatDateTime, formatFullDateTime } from "../../utils/format.js";

    type BulkHandler = (selector: RunSelector, affected: Run[]) => void;

    let {
        items,
        total,
        loading = false,
        filters = $bindable(),
        onLoadMore,
        selectedRunId = $bindable(null),
        onselect,
        showFilters = false,
        showTaskName = false,
        taskNameFilter,
        headerLabel = "Run History",
        emptyText = "No runs yet",
        emptyDescription,
        bulkActions = false,
        onBulkCancel,
        onBulkDelete,
        onBulkRerun,
    }: {
        items: Run[];
        total: number;
        loading?: boolean;
        filters: RunsListFilters;
        onLoadMore?: () => void;
        selectedRunId?: string | null;
        onselect?: (runId: string) => void;
        showFilters?: boolean;
        showTaskName?: boolean;
        taskNameFilter?: string;
        headerLabel?: string;
        emptyText?: string;
        emptyDescription?: string;
        bulkActions?: boolean;
        onBulkCancel?: BulkHandler;
        onBulkDelete?: BulkHandler;
        onBulkRerun?: BulkHandler;
    } = $props();

    const ROW_HEIGHT = 76;
    const OVERSCAN = 8;
    const LOAD_AHEAD = 10;
    const SEARCH_DEBOUNCE_MS = 250;

    // Selection model: two modes —
    //   1. explicit:  user picked specific rows. explicitIds holds them.
    //   2. selectAll: "all matching the current filter". exceptIds holds opt-outs.
    let selectAllMode = $state(false);
    const explicitIds = new SvelteSet<string>();
    const exceptIds = new SvelteSet<string>();

    let scrollElement: HTMLDivElement | undefined = $state();
    let searchInput = $state(filters.search);

    // Debounce typing in the search box before pushing into shared filters,
    // so we don't fire a network request on every keystroke.
    $effect(() => {
        const next = searchInput;
        const id = setTimeout(() => {
            if (filters.search !== next) filters.search = next;
        }, SEARCH_DEBOUNCE_MS);
        return () => clearTimeout(id);
    });

    const virtualizer = createVirtualizer<HTMLDivElement, HTMLDivElement>({
        count: 0,
        getScrollElement: () => scrollElement ?? null,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    // `setOptions` always notifies the store (see @tanstack/svelte-virtual src),
    // so reading $virtualizer here would loop. Untrack the store read.
    $effect(() => {
        const count = items.length;
        untrack(() => $virtualizer.setOptions({ count }));
    });

    $effect(() => {
        const visible = $virtualizer.getVirtualItems();
        const last = visible.at(-1);
        if (!last) return;
        if (loading) return;
        if (items.length >= total) return;
        if (last.index >= items.length - LOAD_AHEAD) onLoadMore?.();
    });

    function isRowSelected(id: string): boolean {
        return selectAllMode ? !exceptIds.has(id) : explicitIds.has(id);
    }

    function toggleRow(id: string) {
        if (selectAllMode) {
            if (exceptIds.has(id)) exceptIds.delete(id);
            else exceptIds.add(id);
        } else {
            if (explicitIds.has(id)) explicitIds.delete(id);
            else explicitIds.add(id);
        }
    }

    function clearSelection() {
        selectAllMode = false;
        explicitIds.clear();
        exceptIds.clear();
    }

    function selectAllVisible() {
        selectAllMode = true;
        explicitIds.clear();
        exceptIds.clear();
    }

    function selectRun(runId: string) {
        selectedRunId = runId;
        onselect?.(runId);
    }

    let selectedRuns = $derived(items.filter((r: Run) => isRowSelected(r.id)));
    let selectionCount = $derived(selectedRuns.length);
    let hasSelection = $derived(selectionCount > 0);
    let allSelected = $derived(selectAllMode && exceptIds.size === 0);

    let anyRunning = $derived(selectedRuns.some((r: Run) => r.status === "running"));
    let anyTerminal = $derived(
        selectedRuns.some((r: Run) => r.status !== "running" && r.status !== "pending"),
    );

    function buildSelector(): RunSelector {
        if (!selectAllMode) {
            return { match_all: false, ids: [...explicitIds] };
        }
        const filter: { status?: string; task_name?: string; search?: string } = {};
        if (taskNameFilter) filter.task_name = taskNameFilter;
        if (showFilters) {
            if (filters.status && filters.status !== "all") filter.status = filters.status;
            const query = filters.search.trim();
            if (query) filter.search = query;
        }
        return {
            match_all: true,
            filter,
            except_ids: [...exceptIds],
        };
    }

    function emitBulk(handler: BulkHandler | undefined, predicate: (r: Run) => boolean) {
        if (!handler) return;
        const affected = selectedRuns.filter(predicate);
        if (affected.length === 0) return;
        const sel = buildSelector();
        // Narrow the selector to the affected predicate when in explicit mode
        // so we don't ask the server to operate on rows the UI excluded.
        const narrowed: RunSelector = sel.match_all
            ? sel
            : { match_all: false, ids: affected.map((r) => r.id) };
        handler(narrowed, affected);
        clearSelection();
    }

    function handleMasterToggle() {
        if (hasSelection) clearSelection();
        else selectAllVisible();
    }

    function toggleSortDirection() {
        filters.sort_direction = filters.sort_direction === "asc" ? "desc" : "asc";
    }

    let masterCheckboxRef: HTMLInputElement | undefined = $state();
    $effect(() => {
        if (!masterCheckboxRef) return;
        masterCheckboxRef.indeterminate = hasSelection && !allSelected;
    });
</script>

<div
    class="flex flex-col overflow-hidden rounded-xl border border-outline bg-surface-raised shadow-sm md:col-span-4 lg:col-span-3"
>
    <!-- Inline heading: master checkbox, label, selection count or controls -->
    <div
        class="flex shrink-0 items-center gap-2 border-b border-outline-faint px-3 py-2 {hasSelection
            ? 'bg-primary-soft/40'
            : 'bg-surface-sunken'}"
    >
        {#if bulkActions}
            <label
                class="flex shrink-0 cursor-pointer items-center"
                title={hasSelection ? "Clear selection" : "Select all"}
            >
                <input
                    bind:this={masterCheckboxRef}
                    type="checkbox"
                    checked={allSelected}
                    onchange={handleMasterToggle}
                    class="h-3.5 w-3.5 cursor-pointer rounded border-outline accent-primary"
                    aria-label={hasSelection ? "Clear selection" : "Select all"}
                />
            </label>
        {/if}

        {#if hasSelection}
            <span class="text-xs font-medium text-on-surface">
                {selectionCount} selected
            </span>
            <div class="ml-auto flex items-center gap-1">
                {#if onBulkRerun}
                    <Button
                        variant="ghost"
                        size="xs"
                        class="h-7 w-7 px-0"
                        onclick={() => emitBulk(onBulkRerun, () => true)}
                        title="Re-run task{selectionCount === 1 ? '' : 's'}"
                    >
                        {#snippet icon()}<RotateCw size={14} />{/snippet}
                    </Button>
                {/if}
                {#if onBulkCancel && anyRunning}
                    <Button
                        variant="ghost"
                        size="xs"
                        class="h-7 w-7 px-0"
                        onclick={() => emitBulk(onBulkCancel, (r) => r.status === "running")}
                        title="Cancel running run{selectionCount === 1 ? '' : 's'}"
                    >
                        {#snippet icon()}<Square size={14} />{/snippet}
                    </Button>
                {/if}
                {#if onBulkDelete && anyTerminal}
                    <Button
                        variant="danger"
                        size="xs"
                        class="h-7 w-7 px-0"
                        onclick={() =>
                            emitBulk(
                                onBulkDelete,
                                (r) => r.status !== "running" && r.status !== "pending",
                            )}
                        title="Delete run{selectionCount === 1 ? '' : 's'}"
                    >
                        {#snippet icon()}<Trash2 size={14} />{/snippet}
                    </Button>
                {/if}
            </div>
        {:else}
            <span class="text-xs font-semibold tracking-wider text-on-surface-muted uppercase">
                {headerLabel}
            </span>
            <Badge variant="default" size="sm">{items.length} of {total}</Badge>
            <div class="ml-auto flex items-center gap-1">
                <Button
                    variant="ghost"
                    size="xs"
                    class="h-7 w-7 px-0"
                    onclick={toggleSortDirection}
                    title="Toggle sort order"
                >
                    {#snippet icon()}
                        <ArrowUpDown
                            size={14}
                            class="text-on-surface-muted {filters.sort_direction === 'asc'
                                ? 'rotate-180'
                                : ''}"
                        />
                    {/snippet}
                </Button>
            </div>
        {/if}
    </div>

    {#if showFilters}
        <!-- Filter controls (search + status) -->
        <div class="shrink-0 space-y-2 border-b border-outline-faint bg-surface-sunken px-3 py-2">
            <div class="relative">
                <Search
                    class="absolute top-1/2 left-2.5 -translate-y-1/2 text-on-surface-faint"
                    size={14}
                />
                <input
                    type="text"
                    bind:value={searchInput}
                    placeholder="Search task or ID..."
                    class="h-8 w-full rounded-md border border-outline bg-surface-raised pr-3 pl-8 text-sm transition-all placeholder:text-on-surface-faint focus:border-ring focus:ring-2 focus:ring-ring/20 focus:outline-none"
                />
            </div>
            <div class="relative">
                <select
                    bind:value={filters.status}
                    class="h-7 w-full appearance-none rounded-md border border-outline bg-surface-raised pr-6 pl-2 text-xs text-on-surface-muted focus:border-ring focus:outline-none"
                >
                    <option value="all">All Statuses</option>
                    <option value="running">Running</option>
                    <option value="success">Success</option>
                    <option value="failed">Failed</option>
                    <option value="crashed">Crashed</option>
                    <option value="skipped">Skipped</option>
                </select>
                <Funnel
                    class="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-on-surface-faint"
                    size={12}
                />
            </div>
        </div>
    {/if}

    <div bind:this={scrollElement} class="min-h-0 flex-1 overflow-y-auto p-2">
        {#if items.length === 0 && !loading}
            <EmptyState
                title={emptyText}
                description={emptyDescription}
                icon={Clock}
                iconSize={32}
                class="py-8"
            />
        {:else}
            <div
                style:height="{$virtualizer.getTotalSize()}px"
                style:width="100%"
                style:position="relative"
            >
                {#each $virtualizer.getVirtualItems() as row (items[row.index]?.id ?? row.index)}
                    {@const run = items[row.index]}
                    {#if run}
                        {@const isActive = selectedRunId === run.id}
                        {@const isChecked = isRowSelected(run.id)}
                        {@const config = getRunStatusConfig(runDisplayStatus(run))}
                        {@const Icon = config.icon}
                        {@const duration = runDuration(run)}
                        {@const retry = runRetryLabel(run)}
                        {@const startedAt = run.start_at ?? run.created_at}
                        <div
                            class="flex items-stretch gap-1"
                            style:position="absolute"
                            style:top="0"
                            style:left="0"
                            style:width="100%"
                            style:height="{row.size}px"
                            style:transform="translateY({row.start}px)"
                        >
                            {#if bulkActions}
                                <label
                                    class="flex shrink-0 cursor-pointer items-center px-1.5"
                                    aria-label={`Select run from ${formatDateTime(run.start_at ?? run.created_at)}`}
                                >
                                    <input
                                        type="checkbox"
                                        checked={isChecked}
                                        onchange={() => toggleRow(run.id)}
                                        onclick={(e) => e.stopPropagation()}
                                        class="h-3.5 w-3.5 cursor-pointer rounded border-outline accent-primary"
                                    />
                                </label>
                            {/if}
                            <button
                                class="btn-scale group duration-normal relative w-full rounded-lg border p-3 text-left transition-all select-none
                                {isActive
                                    ? 'border-primary-soft bg-primary-soft/50 shadow-sm'
                                    : 'border-transparent bg-surface-raised hover:border-outline hover:bg-surface-sunken'}"
                                onclick={() => selectRun(run.id)}
                                onkeydown={(e) => e.key === "Enter" && selectRun(run.id)}
                            >
                                {#if showTaskName}
                                    <!-- Runs variant: task name as primary, shortId secondary -->
                                    <div class="mb-1 flex items-center justify-between">
                                        <div class="flex min-w-0 items-center gap-2">
                                            <div class="{config.color} shrink-0">
                                                <Icon
                                                    size={16}
                                                    class={run.status === "running"
                                                        ? "animate-spin"
                                                        : ""}
                                                />
                                            </div>
                                            <span
                                                class="truncate text-sm font-semibold text-on-surface"
                                            >
                                                {run.task_name}{#if run.instance_index > 0}<span
                                                        class="text-on-surface-muted"
                                                        >#{run.instance_index}</span
                                                    >{/if}
                                            </span>
                                        </div>
                                        <span
                                            class="shrink-0 text-2xs {isActive
                                                ? 'font-medium text-primary'
                                                : 'text-on-surface-faint'}"
                                            title={formatFullDateTime(startedAt)}
                                        >
                                            {formatRelativeTime(startedAt)}
                                        </span>
                                    </div>

                                    <div class="flex items-center justify-between pl-6 text-xs">
                                        <div
                                            class="flex min-w-0 items-center gap-1.5 text-2xs text-on-surface-muted"
                                        >
                                            <span class="truncate">{formatDateTime(startedAt)}</span
                                            >
                                            <span class="text-on-surface-faint"
                                                >· {formatTriggeredByLabel(run.triggered_by)}</span
                                            >
                                            {#if retry}
                                                <span
                                                    class="shrink-0 rounded bg-surface-sunken px-1 font-mono text-2xs text-on-surface-faint"
                                                    >{retry}</span
                                                >
                                            {/if}
                                        </div>
                                        {#if duration}
                                            <div
                                                class="flex items-center gap-1 text-on-surface-faint {isActive
                                                    ? 'text-primary'
                                                    : ''}"
                                            >
                                                <Timer size={10} />
                                                <span class="font-mono">{duration}</span>
                                            </div>
                                        {/if}
                                    </div>
                                {:else}
                                    <!-- Task variant: when it ran as primary, outcome secondary -->
                                    <div class="mb-1.5 flex items-center justify-between">
                                        <div class="flex min-w-0 items-center gap-2">
                                            <div class="{config.color} shrink-0">
                                                <Icon
                                                    size={16}
                                                    class={run.status === "running"
                                                        ? "animate-spin"
                                                        : ""}
                                                />
                                            </div>
                                            <span
                                                class="truncate text-sm font-semibold text-on-surface"
                                            >
                                                {formatDateTime(startedAt)}
                                            </span>
                                        </div>
                                        <span
                                            class="shrink-0 text-2xs {isActive
                                                ? 'font-medium text-primary'
                                                : 'text-on-surface-faint'}"
                                            title={formatFullDateTime(startedAt)}
                                        >
                                            {formatRelativeTime(startedAt)}
                                        </span>
                                    </div>

                                    <div class="flex items-center justify-between pl-6 text-xs">
                                        <div
                                            class="flex min-w-0 items-center gap-2 text-on-surface-muted"
                                        >
                                            <span class="capitalize">{runDisplayStatus(run)}</span>
                                            <span class="text-on-surface-faint"
                                                >· {formatTriggeredByLabel(run.triggered_by)}</span
                                            >
                                            {#if retry}
                                                <span
                                                    class="shrink-0 rounded bg-surface-sunken px-1 font-mono text-2xs text-on-surface-faint"
                                                    >{retry}</span
                                                >
                                            {/if}
                                            {#if run.instance_index > 0}
                                                <span
                                                    class="font-mono text-2xs text-on-surface-faint"
                                                    >instance #{run.instance_index}</span
                                                >
                                            {/if}
                                        </div>
                                        {#if duration}
                                            <div
                                                class="flex items-center gap-1 text-on-surface-faint {isActive
                                                    ? 'text-primary'
                                                    : ''}"
                                            >
                                                <Timer size={10} />
                                                <span class="font-mono">{duration}</span>
                                            </div>
                                        {/if}
                                    </div>
                                {/if}

                                <div
                                    class="duration-normal absolute top-1/2 left-0 h-8 w-1 -translate-y-1/2 rounded-r-full bg-primary transition-all {isActive
                                        ? 'opacity-100'
                                        : 'opacity-0'}"
                                    aria-hidden="true"
                                ></div>
                            </button>
                        </div>
                    {/if}
                {/each}
            </div>
        {/if}
    </div>
</div>
