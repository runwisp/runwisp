<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Clock, ArrowUpDown, Square, Trash, RotateCw } from "@lucide/svelte";
    import { untrack } from "svelte";
    import { createVirtualizer } from "@tanstack/svelte-virtual";
    import Button from "../Button.svelte";
    import { arrival, leave, prefersReducedMotion } from "../../actions/row-motion.js";
    import type { RunMotion } from "../../utils/run-motion.js";
    import EmptyState from "../EmptyState.svelte";
    import { BulkSelection } from "./bulk-selection.svelte.js";
    import RunFilterChips from "./RunFilterChips.svelte";
    import RunFilterPopover from "./RunFilterPopover.svelte";
    import RunListSkeleton from "./RunListSkeleton.svelte";
    import RunRow from "./RunRow.svelte";
    import type { RunOutputMatch } from "./types.js";
    import type { Run, RunSelector } from "@runwisp/common";
    import { runFilterParams, type RunsListFilters } from "./run-filters.js";
    import { instanceSuffix } from "./run-helpers.js";
    import { formatDateTime } from "../../utils/format.js";

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
        showTask = false,
        tasks = [],
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
        motion,
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
        // Cross-task /runs view: lets the filter popover offer a Task select.
        // On a single task's page this stays false (the task is the page scope).
        showTask?: boolean;
        // Task list backing the popover's Task select (cross-task view only).
        tasks?: { name: string }[];
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
        // Which runs arrived or were removed live moments ago: their rows animate
        // in or out. Page loads, scrolling and filtering never do.
        motion?: RunMotion;
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
        // window or request in flight), the rail shows its searching shimmer.
        outputSearchPending?: boolean;
    } = $props();

    // Task-rail rows are one dense line (44px); the cross-task /runs view adds
    // the task name on a second line (64px).
    const rowHeight = $derived(showTaskName ? 64 : 44);
    const OVERSCAN = 8;
    const LOAD_AHEAD = 10;

    const selection = new BulkSelection();

    let scrollElement: HTMLDivElement | undefined = $state();

    // Output search filters the rail by what each run printed. The query lives
    // in the app header now; this component only renders against it.
    const outputSearchActive = $derived(outputSearch && outputQuery.trim().length > 0);

    // Loaded runs that matched the output search, in list order. A match in a
    // not-yet-loaded run can't be shown until the rail scrolls far enough to
    // load it, the count reflects what's loaded, not the whole history.
    const matchedRuns = $derived(
        outputMatches ? items.filter((r: Run) => outputMatches.has(r.id)) : [],
    );

    const virtualizer = createVirtualizer<HTMLDivElement, HTMLDivElement>({
        count: 0,
        getScrollElement: () => scrollElement ?? null,
        estimateSize: () => rowHeight,
        overscan: OVERSCAN,
    });

    // `setOptions` always notifies the store (see @tanstack/svelte-virtual src),
    // so reading $virtualizer here would loop. Untrack the store read.
    // Pre-effect so the count lands in the same render as `items`: otherwise
    // the row pushed past the old count unmounts for a frame and remounts
    // without its slide. A pre-effect first runs before `bind:this`, so it also
    // re-runs once the scroll element exists or the virtualizer never attaches.
    $effect.pre(() => {
        const count = items.length;
        if (!scrollElement) return;
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

    // Scroll a programmatically-selected run (deep link, detail-panel click) into
    // view, since its row may be outside the virtual window. Tracked on
    // selectedRunId/items only; the virtualizer access is untracked to avoid the
    // setOptions-style notify loop (effect_update_depth_exceeded, see above).
    let lastScrolled: string | null = null;
    $effect(() => {
        const id = selectedRunId;
        const index = items.findIndex((r: Run) => r.id === id);
        if (!id) {
            lastScrolled = null;
            return;
        }
        // -1: not loaded yet. The effect re-runs as items grows, so a deep link
        // resolves once its page arrives; we don't chase it with onLoadMore.
        if (index === -1) return;
        if (id === lastScrolled) return; // already handled; don't re-yank on re-render
        if (!scrollElement) return;
        lastScrolled = id;
        // "auto" only scrolls when the row is off-screen, so visible selections
        // (and manual clicks) don't jump. A live arrival taking focus glides
        // there so the operator sees where it went; deep links jump.
        const behavior = motion?.arrived(id) && !prefersReducedMotion() ? "smooth" : "auto";
        untrack(() => $virtualizer.scrollToIndex(index, { align: "auto", behavior }));
    });

    function selectRun(runId: string) {
        selectedRunId = runId;
        onselect?.(runId);
    }

    let selectedRuns = $derived(items.filter((r: Run) => selection.isSelected(r.id)));
    let selectionCount = $derived(selectedRuns.length);
    let hasSelection = $derived(selectionCount > 0);
    // Task-rail rows overlay the checkbox on the status dot (it appears on hover
    // or once a selection exists). selectionActive forces all checkboxes visible
    // so the operator can extend the selection without hunting per-row hovers.
    let selectionActive = $derived(bulkActions && hasSelection);

    let anyRunning = $derived(selectedRuns.some((r: Run) => r.status === "running"));
    let anyTerminal = $derived(
        selectedRuns.some((r: Run) => r.status !== "running" && r.status !== "pending"),
    );

    // The selector's filter is the same RunFilter the list query uses, so a
    // "select all matching" bulk op targets exactly the rows on screen. The
    // popover dimensions only apply where the popover is shown; the task scope
    // (page-injected or popover-set) always applies.
    function buildSelectorFilter(): NonNullable<RunSelector["filter"]> {
        const filter: NonNullable<RunSelector["filter"]> = showFilters
            ? runFilterParams(filters)
            : {};
        const taskName = taskNameFilter || filters.taskName;
        if (taskName) filter.taskName = taskName;
        return filter;
    }

    function emitBulk(handler: BulkHandler | undefined, predicate: (r: Run) => boolean) {
        if (!handler) return;
        const affected = selectedRuns.filter(predicate);
        if (affected.length === 0) return;
        // In explicit mode the selector is narrowed to the affected rows, so
        // the server is never asked to touch rows the UI excluded.
        handler(
            selection.selector(
                buildSelectorFilter(),
                affected.map((r) => r.id),
            ),
            affected,
        );
        selection.clear();
    }

    function handleMasterToggle() {
        if (hasSelection) selection.clear();
        else selection.selectAll();
    }

    function toggleSortDirection() {
        // Reassign the whole object (not just the property): the parent reads
        // `filters` through a multi-level `bind:`, and a fresh reference is what
        // reliably re-triggers its fetch effect.
        filters = {
            ...filters,
            sortDirection: filters.sortDirection === "asc" ? "desc" : "asc",
        };
    }

    let masterCheckboxRef: HTMLInputElement | undefined = $state();
    $effect(() => {
        if (!masterCheckboxRef) return;
        masterCheckboxRef.indeterminate = hasSelection && !selection.allSelected;
    });
</script>

<!-- A borderless rail that fills its column, divided from the detail panel by
     a single right border. -->
<div
    class="flex h-full w-full flex-col overflow-hidden border-b border-outline bg-surface md:w-[300px] md:shrink-0 md:border-r md:border-b-0"
>
    <div
        class="flex shrink-0 items-center gap-2 border-b px-3 py-2 {hasSelection
            ? 'border-outline-faint bg-primary-soft/40'
            : 'border-transparent bg-surface'}"
    >
        {#if bulkActions}
            <label
                class="flex shrink-0 cursor-pointer items-center"
                title={hasSelection ? "Clear selection" : "Select all"}
            >
                <input
                    bind:this={masterCheckboxRef}
                    type="checkbox"
                    checked={selection.allSelected}
                    onchange={handleMasterToggle}
                    class="h-3.5 w-3.5 cursor-pointer rounded border-outline accent-primary"
                    aria-label={hasSelection ? "Clear selection" : "Select all"}
                />
            </label>
        {/if}

        {#if hasSelection}
            <span class="font-mono text-xs font-medium text-on-surface tabular-nums">
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
                        {#snippet icon()}<Trash size={14} />{/snippet}
                    </Button>
                {/if}
            </div>
        {:else}
            <span
                class="shrink-0 font-mono text-xs font-semibold tracking-[0.14em] whitespace-nowrap text-on-surface-muted uppercase"
            >
                {headerLabel}
            </span>
            <span
                class="shrink-0 font-mono text-xs whitespace-nowrap text-on-surface-faint tabular-nums"
            >
                {#if outputSearchActive}
                    {matchedRuns.length} of {items.length}
                {:else if loading && items.length === 0}
                    <!-- Count unknown until the first page lands. -->
                {:else}
                    {total}
                    {total === 1 ? "run" : "runs"}
                {/if}
            </span>
            <div class="ml-auto flex items-center gap-1">
                {#if showFilters}
                    <RunFilterPopover bind:filters {showTask} {tasks} />
                {/if}
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
                            class="text-on-surface-muted {filters.sortDirection === 'asc'
                                ? 'rotate-180'
                                : ''}"
                        />
                    {/snippet}
                </Button>
            </div>
        {/if}
    </div>

    {#if showFilters}
        <RunFilterChips bind:filters {showTask} />
    {/if}

    <div bind:this={scrollElement} class="min-h-0 flex-1 overflow-y-auto p-2">
        {#if outputSearchActive}
            <!-- Output-search results: the rail filters to runs that printed the
                 query, each annotated with the matching line. -->
            {#if outputSearchPending}
                <RunListSkeleton label="Searching output" {showTaskName} />
            {:else if matchedRuns.length === 0}
                <div class="px-4 py-8 text-center text-xs leading-relaxed text-on-surface-muted">
                    No output matches
                    <b class="font-mono text-on-surface">“{outputQuery.trim()}”</b>.<br />
                    Try a different term.
                </div>
            {:else}
                <div class="flex flex-col gap-0.5">
                    {#each matchedRuns as run (run.id)}
                        <div class="group/row relative flex items-stretch gap-1">
                            {#if bulkActions}
                                {@render rowCheckboxOverlay(run)}
                            {/if}
                            {@render runRow(run, outputMatches?.get(run.id))}
                        </div>
                    {/each}
                </div>
            {/if}
        {:else if items.length === 0 && loading}
            <RunListSkeleton label="Loading runs" {showTaskName} />
        {:else if items.length === 0}
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
                        <!-- The transform transition slides rows aside when a live
                             run is inserted or removed. Navigation never animates:
                             filter/sort/task changes remount rows, pagination
                             appends, and scrolling doesn't move a row. `leave`
                             is global because this {#if}, not the row's each
                             item, is the block it would otherwise wait for; it
                             only plays for runs removed live. -->
                        <div
                            data-run-id={run.id}
                            use:arrival={motion?.arrived(run.id) ?? false}
                            out:leave|global={motion?.removed}
                            class="group/row flex items-center gap-1 transition-transform duration-200 ease-out motion-reduce:transition-none"
                            style:transition-delay="var(--rw-shift-delay, 0ms)"
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
                            {@render runRow(run, undefined)}
                        </div>
                    {/if}
                {/each}
            </div>
        {/if}
    </div>
</div>

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
            checked={selection.isSelected(run.id)}
            onchange={() => selection.toggle(run.id)}
            onclick={(e) => e.stopPropagation()}
            aria-label={`Select run from ${formatDateTime(run.startedAt ?? run.createdAt)}`}
            class="size-3.5 cursor-pointer rounded border-outline accent-primary opacity-0 {selectionActive
                ? 'opacity-100'
                : 'group-hover/row:opacity-100'}"
        />
    </label>
{/snippet}

{#snippet runRow(run: Run, match: RunOutputMatch | undefined)}
    <RunRow
        {run}
        active={selectedRunId === run.id}
        suffix={instanceSuffix(run.instanceIndex, getInstanceCount(run.taskName))}
        {showTaskName}
        {match}
        {outputQuery}
        {bulkActions}
        {selectionActive}
        onselect={selectRun}
    />
{/snippet}
