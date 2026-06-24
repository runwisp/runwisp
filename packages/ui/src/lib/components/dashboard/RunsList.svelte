<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts" module>
    export type RunsListSortDirection = "asc" | "desc" | "";

    export interface RunsListFilters {
        search: string;
        status: string;
        sort_direction: RunsListSortDirection;
    }

    /** A single output-search hit surfaced under its run in the history rail. */
    export interface RunOutputMatch {
        line: number;
        text: string;
    }
</script>

<script lang="ts">
    import { Clock, ArrowUpDown, Funnel, Square, Trash2, RotateCw } from "@lucide/svelte";
    import { untrack } from "svelte";
    import { SvelteSet } from "svelte/reactivity";
    import { createVirtualizer } from "@tanstack/svelte-virtual";
    import Button from "../Button.svelte";
    import EmptyState from "../EmptyState.svelte";
    import type { Run } from "./types.js";
    import type { RunSelector, RunStatus } from "@runwisp/common";
    import { getRunStatusConfig, runDisplayStatus } from "./status-config.js";
    import {
        runDuration,
        formatTriggeredByLabel,
        runRetryLabel,
        instanceSuffix,
    } from "./run-helpers.js";
    import {
        formatDateTime,
        formatFullDateTime,
        formatTimeHM,
        formatDayMonth,
    } from "../../utils/format.js";

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
        getInstanceCount = () => 1,
        flush = false,
        outputSearch = false,
        outputQuery = "",
        outputMatches = null,
        outputSearchPending = false,
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
        // Resolves a task's currently configured instance count so multi-instance
        // services render a 1-based #N suffix. Defaults to single-instance.
        getInstanceCount?: (taskName: string) => number;
        // Flush rail mode (task detail page): render as a borderless rail that
        // fills its column and is divided from the detail panel by a single right
        // border — no card chrome of its own. Default renders the standalone card
        // the cross-task /runs grid expects.
        flush?: boolean;
        // Output search (history rail): filters runs by what they printed. The
        // search box lives in the app header now; the parent owns the box, the
        // query, and the async log search. This component just renders the
        // matched rows. `outputSearch` enables the mode; `outputQuery` is the
        // live query (drives the active state and snippet highlighting).
        outputSearch?: boolean;
        outputQuery?: string;
        // run id → first matching line, supplied by the parent after a search.
        // null = no active search; the full list shows.
        outputMatches?: Map<string, RunOutputMatch> | null;
        // True while a query is typed but its results aren't in yet (debounce
        // window or request in flight) — the rail shows its searching shimmer.
        outputSearchPending?: boolean;
    } = $props();

    // Task-rail rows are a single dense line (artifact ".run", 46px); the
    // cross-task /runs view adds the task name on a second line (64px).
    const rowHeight = $derived(showTaskName ? 64 : 44);
    const OVERSCAN = 8;
    const LOAD_AHEAD = 10;

    // Selection model: two modes —
    //   1. explicit:  user picked specific rows. explicitIds holds them.
    //   2. selectAll: "all matching the current filter". exceptIds holds opt-outs.
    let selectAllMode = $state(false);
    const explicitIds = new SvelteSet<string>();
    const exceptIds = new SvelteSet<string>();

    let scrollElement: HTMLDivElement | undefined = $state();

    // Output search filters the rail by what each run printed. The query lives
    // in the app header now; this component only renders against it.
    const outputSearchActive = $derived(outputSearch && outputQuery.trim().length > 0);

    // Loaded runs that matched the output search, in list order. A match in a
    // not-yet-loaded run can't be shown until the rail scrolls far enough to
    // load it — the count reflects what's loaded, not the whole history.
    const matchedRuns = $derived(
        outputMatches ? items.filter((r: Run) => outputMatches.has(r.id)) : [],
    );

    // Split the matching line into [before, match, after] around the first
    // occurrence of the query, windowed to keep the match in view — mirrors the
    // artifact's snippet. Rendered as plain text spans (Svelte auto-escapes), so
    // no untrusted HTML ever reaches the DOM.
    interface HighlightParts {
        before: string;
        match: string;
        after: string;
    }

    function highlightParts(text: string, query: string): HighlightParts {
        const q = query.trim();
        const idx = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1;
        if (idx === -1) return { before: text, match: "", after: "" };
        const start = Math.max(0, idx - 14);
        const lead = start > 0 ? "…" : "";
        const windowed = text.slice(start);
        const fi = windowed.toLowerCase().indexOf(q.toLowerCase());
        return {
            before: lead + windowed.slice(0, fi),
            match: windowed.slice(fi, fi + q.length),
            after: windowed.slice(fi + q.length),
        };
    }

    // Right-hand mono readout on a task-rail row: live / queued / exit N / dur.
    function rowRightLabel(run: Run, displayed: RunStatus): string {
        if (run.status === "running") return "live";
        if (run.status === "pending") return "queued";
        if (displayed === "failed" || displayed === "crashed") {
            return "exit " + String(run.exit_code);
        }
        return runDuration(run) ?? "—";
    }

    const virtualizer = createVirtualizer<HTMLDivElement, HTMLDivElement>({
        count: 0,
        getScrollElement: () => scrollElement ?? null,
        estimateSize: () => rowHeight,
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
    // Task-rail rows overlay the checkbox on the status dot (it appears on hover
    // or once a selection exists). selectionActive forces all checkboxes visible
    // so the operator can extend the selection without hunting per-row hovers.
    let selectionActive = $derived(bulkActions && hasSelection);
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
        // Reassign the whole object (not just the property): the parent reads
        // `filters` through a multi-level `bind:`, and a fresh reference is what
        // reliably re-triggers its fetch effect.
        filters = {
            ...filters,
            sort_direction: filters.sort_direction === "asc" ? "desc" : "asc",
        };
    }

    let masterCheckboxRef: HTMLInputElement | undefined = $state();
    $effect(() => {
        if (!masterCheckboxRef) return;
        masterCheckboxRef.indeterminate = hasSelection && !allSelected;
    });
</script>

<div
    class={flush
        ? "flex h-full w-full flex-col overflow-hidden border-b border-outline bg-surface md:w-[300px] md:shrink-0 md:border-r md:border-b-0"
        : "flex flex-col overflow-hidden rounded-xl border border-outline bg-surface-raised shadow-sm md:col-span-4 lg:col-span-3"}
>
    <!-- Inline heading: master checkbox, label, selection count or controls -->
    <div
        class="flex shrink-0 items-center gap-2 border-b px-3 py-2 {hasSelection
            ? 'border-outline-faint bg-primary-soft/40'
            : flush
              ? 'border-transparent bg-surface'
              : 'border-outline-faint bg-surface-sunken'}"
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
            <span class="text-xs text-on-surface-faint">
                {#if outputSearchActive}
                    {matchedRuns.length} of {items.length}
                {:else}
                    {total}
                    {total === 1 ? "run" : "runs"}
                {/if}
            </span>
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
        <!-- Status filter. The text search that used to sit here moved to the
             app header (it filters this same list by task name or run ID). -->
        <div class="shrink-0 border-b border-outline-faint bg-surface-sunken px-3 py-2">
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
        {#if outputSearchActive}
            <!-- Output-search results: the rail filters to runs that printed the
                 query, each annotated with the matching line. -->
            {#if outputSearchPending}
                <!-- Searching: shimmer placeholders shaped like result rows. -->
                <div class="flex flex-col gap-0.5" aria-busy="true" aria-label="Searching output">
                    {#each [0, 1, 2, 3, 4] as i (i)}
                        <div class="flex items-center gap-2.5 rounded-[10px] px-3 py-2.5">
                            <span
                                class="size-[9px] shrink-0 animate-pulse rounded-full bg-surface-sunken"
                            ></span>
                            <span
                                class="h-3 animate-pulse rounded bg-surface-sunken"
                                style:width="{70 - i * 8}%"
                            ></span>
                            <span class="ml-auto h-3 w-9 animate-pulse rounded bg-surface-sunken"
                            ></span>
                        </div>
                    {/each}
                </div>
            {:else if matchedRuns.length === 0}
                <div class="px-4 py-8 text-center text-xs leading-relaxed text-on-surface-muted">
                    No output matches
                    <b class="text-on-surface">“{outputQuery.trim()}”</b>.<br />
                    Try a different term.
                </div>
            {:else}
                <div class="flex flex-col gap-0.5">
                    {#each matchedRuns as run (run.id)}
                        <div class="group/row relative flex items-stretch gap-1">
                            {#if bulkActions}
                                {@render rowCheckboxOverlay(run)}
                            {/if}
                            {@render runRowButton(
                                run,
                                selectedRunId === run.id,
                                outputMatches?.get(run.id),
                            )}
                        </div>
                    {/each}
                </div>
            {/if}
        {:else if items.length === 0 && !loading}
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
                        <div
                            class="group/row flex items-center gap-1"
                            style:position="absolute"
                            style:top="0"
                            style:left="0"
                            style:width="100%"
                            style:height="{row.size}px"
                            style:transform="translateY({row.start}px)"
                        >
                            {#if bulkActions}
                                {@render rowCheckboxOverlay(run)}
                            {/if}
                            {@render runRowButton(run, selectedRunId === run.id, undefined)}
                        </div>
                    {/if}
                {/each}
            </div>
        {/if}
    </div>
</div>

<!-- Status dot. With bulk actions on it fades out — on row hover, or whenever a
     selection exists — so the row checkbox can take its place over it. -->
{#snippet statusDot(colorClass: string, running: boolean)}
    <span
        class="{colorClass} size-[9px] shrink-0 rounded-full bg-current ring-[3px] ring-current/20 {running
            ? 'animate-pulse'
            : ''} {bulkActions
            ? selectionActive
                ? 'opacity-0'
                : 'transition-opacity group-hover/row:opacity-0'
            : ''}"
        aria-hidden="true"
    ></span>
{/snippet}

<!-- Row checkbox: sits over the status dot (which fades out beneath it) so the
     row keeps the artifact's geometry. Lives in the row wrapper (not the button)
     to keep the markup valid, positioned to land on the dot. The 14px box is
     wrapped in a 28px label so the hover/click hitbox is comfortable without
     enlarging the visible checkbox. Used by both the task rail and the
     cross-task /runs grid so selection behaves identically in each. -->
{#snippet rowCheckboxOverlay(run: Run)}
    <label
        class="absolute top-1/2 left-[2px] z-10 flex size-7 -translate-y-1/2 cursor-pointer items-center justify-center"
    >
        <input
            type="checkbox"
            checked={isRowSelected(run.id)}
            onchange={() => toggleRow(run.id)}
            onclick={(e) => e.stopPropagation()}
            aria-label={`Select run from ${formatDateTime(run.start_at ?? run.created_at)}`}
            class="size-3.5 cursor-pointer rounded border-outline accent-primary opacity-0 transition-opacity {selectionActive
                ? 'opacity-100'
                : 'group-hover/row:opacity-100'}"
        />
    </label>
{/snippet}

{#snippet runRowButton(run: Run, isActive: boolean, match: RunOutputMatch | undefined)}
    {@const dstatus = runDisplayStatus(run)}
    {@const config = getRunStatusConfig(dstatus)}
    {@const running = run.status === "running"}
    {@const spine = config.dot.replace(" animate-pulse", "")}
    {@const startedAt = run.start_at ?? run.created_at}
    {@const retry = runRetryLabel(run)}
    {@const suffix = instanceSuffix(run.instance_index, getInstanceCount(run.task_name))}
    <button
        class="btn-scale group relative w-full rounded-[10px] border text-left transition-all select-none {showTaskName
            ? 'p-3'
            : 'px-3 py-[11px]'} {isActive
            ? 'border-outline bg-surface-raised shadow-sm'
            : 'border-transparent hover:bg-surface-sunken'}"
        onclick={() => selectRun(run.id)}
        onkeydown={(e) => e.key === "Enter" && selectRun(run.id)}
    >
        {#if showTaskName}
            <!-- Cross-task /runs variant: the same readout language as the task
                 rail — status dot, status-colored outcome, mono right readout —
                 with the task name carried as the primary. -->
            <div class="flex items-center gap-2.5">
                {@render statusDot(config.color, running)}
                <span class="flex min-w-0 flex-1 flex-col gap-0.5">
                    <span class="flex items-center gap-1.5">
                        <span class="truncate text-[13px] font-semibold text-on-surface">
                            {run.task_name}{#if suffix}<span class="text-on-surface-muted"
                                    >{suffix}</span
                                >{/if}
                        </span>
                        <span class="shrink-0 text-on-surface-faint">·</span>
                        <span class="shrink-0 text-[12px] font-semibold capitalize {config.color}"
                            >{dstatus}</span
                        >
                    </span>
                    <span
                        class="flex min-w-0 items-center gap-1.5 text-2xs text-on-surface-faint"
                        title={formatFullDateTime(startedAt)}
                    >
                        <span class="truncate">{formatDateTime(startedAt)}</span>
                        <span class="shrink-0">· {formatTriggeredByLabel(run.triggered_by)}</span>
                        {#if retry}
                            <span class="shrink-0 rounded bg-surface-sunken px-1 font-mono"
                                >{retry}</span
                            >
                        {/if}
                    </span>
                </span>
                <span
                    class="shrink-0 self-start pt-0.5 font-mono text-[11.5px] text-on-surface-faint tabular-nums"
                    title={retry ?? undefined}
                >
                    {rowRightLabel(run, dstatus)}
                </span>
            </div>
        {:else}
            <!-- Task-rail variant (artifact ".run"): a single dense line —
                 time · date · outcome, with a mono exit/duration readout.
                 leading-tight matches the artifact's ~1.2 line-height so the
                 (descender-less) text optically centers instead of riding high
                 inside Tailwind's default 1.5 line box. -->
            <div class="flex items-center gap-[11px] leading-tight">
                {@render statusDot(config.color, running)}
                <span class="flex min-w-0 flex-1 items-center gap-1.5 truncate">
                    <span
                        class="text-[12.5px] font-semibold tracking-tight text-on-surface"
                        title={formatFullDateTime(startedAt)}
                    >
                        {formatTimeHM(startedAt)} · {formatDayMonth(startedAt)}
                    </span>
                    <span class="text-on-surface-faint">·</span>
                    <span class="text-[12.5px] font-semibold capitalize {config.color}"
                        >{dstatus}</span
                    >
                    {#if suffix}
                        <span class="font-mono text-2xs text-on-surface-faint">{suffix}</span>
                    {/if}
                </span>
                <span
                    class="shrink-0 font-mono text-[11.5px] text-on-surface-faint tabular-nums"
                    title={retry ?? undefined}
                >
                    {rowRightLabel(run, dstatus)}
                </span>
            </div>
            {#if match}
                {@const hl = highlightParts(match.text, outputQuery)}
                <div
                    class="mt-1.5 truncate rounded-md bg-surface-sunken px-2 py-1 font-mono text-2xs text-on-surface-muted"
                >
                    {hl.before}<mark
                        class="rounded-sm bg-primary-soft px-0.5 text-primary-soft-text"
                        >{hl.match}</mark
                    >{hl.after}
                </div>
            {/if}
        {/if}

        <div
            class="duration-normal absolute inset-y-2 left-[-6px] w-[3px] rounded-[3px] transition-all {spine} {isActive
                ? 'opacity-100'
                : 'opacity-0'}"
            aria-hidden="true"
        ></div>
    </button>
{/snippet}
