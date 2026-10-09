<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Play, Square } from "@lucide/svelte";
    import { untrack } from "svelte";
    import { SvelteMap } from "svelte/reactivity";
    import { isService, type Task } from "@runwisp/common";
    import type { RunsListFilters, RunOutputMatch } from "@runwisp/ui";
    import {
        activeFilterCount,
        RunsList,
        RunDetailPanel,
        Button,
        Modal,
        Alert,
        AlertDialog,
    } from "@runwisp/ui";
    import { tasksApi } from "$lib/api";
    import { headerSearchStore, systemStore } from "$lib/stores";
    import type { LiveRuns } from "$lib/utils/live-runs.svelte";
    import { createRunSelection } from "$lib/utils/run-selection.svelte";
    import { HistoryRail } from "$lib/utils/history-rail.svelte";
    import ParamForm from "./ParamForm.svelte";
    import { taskInstanceCount } from "$lib/utils/task";

    let {
        task,
        live,
        filters = $bindable(),
        concurrencyReached,
        triggering,
        onRun,
        onStop,
        initialRunId,
        initialHighlightLine,
        selectRunId,
        onSelectRun,
    }: {
        task: Task;
        live: LiveRuns;
        filters: RunsListFilters;
        concurrencyReached: boolean;
        triggering: boolean;
        onRun: (params?: Record<string, string | null>) => void;
        onStop: (runId: string) => void;
        initialRunId: string | null;
        initialHighlightLine: number | null;
        selectRunId: string | null;
        // Reports explicit picks (not the auto-fallback) so the URL can mirror them.
        onSelectRun: (runId: string | null) => void;
    } = $props();

    const taskIsService = $derived(isService(task.kind));
    const instanceCount = $derived(taskIsService ? taskInstanceCount(task) : 0);
    // The page stays mounted across tasks, so a phone opens each task on its
    // run list (or on the run its URL names).
    const rail = new HistoryRail();
    $effect.pre(() => {
        void task.name;
        untrack(() => rail.reset(!!initialRunId));
    });
    let confirmOpen = $state(false);
    let runParamValues = $state<Record<string, string | null>>({});
    let runParamsValid = $state(true);
    const taskParams = $derived(task.parameters ?? []);
    const hasParams = $derived(taskParams.length > 0);
    // The Run modal only needs a body when there's something to show, the
    // concurrency warning or the parameter form. Passing `children`
    // conditionally keeps Modal from rendering an empty padded band otherwise.
    const showRunBody = $derived(concurrencyReached || (hasParams && confirmOpen));

    // Seed values for the Run modal's parameter form: null = start from the
    // task defaults ("Run"); a prior run's params = pre-fill from it ("Run
    // again"). `runFormSeq` keys the form so each open (or a reset) re-mounts
    // it and re-seeds, ParamForm captures its values once at construction.
    let runSeed = $state<Record<string, string | null> | null>(null);
    let runFormSeq = $state(0);

    function openRun() {
        runSeed = null;
        runFormSeq++;
        confirmOpen = true;
    }

    function openRunAgain() {
        runSeed = selection.selectedRun?.params ?? null;
        runFormSeq++;
        confirmOpen = true;
    }

    function resetRunToDefaults() {
        runSeed = null;
        runFormSeq++;
    }
    let stopConfirmOpen = $state(false);

    // Output search filters the rail by what each run printed. The search box
    // lives in the app header now; this page owns the async query (it has the
    // API client) and the matched-run map, and feeds the header via the store.
    let outputMatches = $state<Map<string, RunOutputMatch> | null>(null);
    let outputSearchLoading = $state(false);
    let outputSearchSeq = 0;
    // The query most recently handed to the search. While the live header query
    // is ahead of it (mid-type, inside the header's debounce) the search counts
    // as pending even though no request has fired, keeps the rail in its
    // searching state instead of flashing stale results.
    let lastDispatched = $state("");

    async function handleOutputSearch(rawQuery: string) {
        const query = rawQuery.trim();
        lastDispatched = query;
        if (!query) {
            outputSearchSeq++;
            outputMatches = null;
            outputSearchLoading = false;
            return;
        }
        const seq = ++outputSearchSeq;
        outputSearchLoading = true;
        // Drop stale hits so the rail shows its searching state, not the
        // previous query's results, while this one is in flight.
        outputMatches = null;
        try {
            const hits = await tasksApi.searchLogs(task.name, {
                q: query,
                regex: false,
                case: false,
                limit: 200,
            });
            if (seq !== outputSearchSeq) return; // a newer query superseded this one
            const map = new SvelteMap<string, RunOutputMatch>();
            for (const hit of hits) {
                if (!map.has(hit.runId)) map.set(hit.runId, { line: hit.n, text: hit.text });
            }
            outputMatches = map;
        } catch {
            if (seq === outputSearchSeq) outputMatches = new SvelteMap();
        } finally {
            if (seq === outputSearchSeq) outputSearchLoading = false;
        }
    }

    // The live header query, and the rail's derived search state from it.
    const outputQuery = $derived(headerSearchStore.query);
    const outputSearchActive = $derived(outputQuery.trim().length > 0);
    const outputSearchPending = $derived(
        outputSearchActive &&
            (outputSearchLoading ||
                outputMatches === null ||
                outputQuery.trim() !== lastDispatched),
    );

    // Register the header search, and re-register on task change so a query
    // never leaks from one task to the next. The header owns the box + debounce
    // and calls back here.
    $effect(() => {
        void task.name;
        headerSearchStore.register({
            placeholder: "Search output across runs…",
            onSearch: (q) => {
                rail.searched(q);
                void handleOutputSearch(q);
            },
        });
        return () => headerSearchStore.unregister();
    });

    // Surface the async log-search progress as the header field's spinner.
    $effect(() => {
        headerSearchStore.setLoading(outputSearchLoading);
    });

    // `live` and `onSelectRun` are fixed for the page's lifetime.
    // svelte-ignore state_referenced_locally
    const selection = createRunSelection({
        live,
        getInitialRunId: () => initialRunId,
        preferRunning: true,
        getSelectRunId: () => selectRunId,
        // A run named from outside the list (a notification link, a run just
        // triggered) is picked too, so a phone shows it rather than the list.
        onSeeded: () => rail.picked(),
        onSelectRun,
    });

    // A run can always be *triggered*, at max concurrency it queues (the modal
    // says so), so concurrency must not gate the button, only its warning.
    // Disabled only when the task forbids API triggering or a trigger is mid-flight.
    const runTriggerable = $derived(!taskIsService && task.manualTrigger && !triggering);

    // In station mode the station owns scheduling/dispatch; triggering here is the
    // operator's "run it here, now" escape hatch against the local runner.
    // Frame the confirm honestly rather than implying it's the canonical trigger.
    const stationMode = $derived(systemStore.stationEnabled);
    const runConfirmLabel = $derived(stationMode ? "Run Here" : "Run Now");
    const runModalTitle = $derived(stationMode ? "Run on this runner" : "Run Task");
    const runModalDescription = $derived(
        stationMode
            ? `Run ${task.name} on this runner now? This triggers an immediate local run; scheduling stays with the station.`
            : `Trigger a new run of ${task.name}?`,
    );

    let highlightLine = $state<number | null>(null);

    $effect(() => {
        if (initialHighlightLine !== null) {
            highlightLine = initialHighlightLine;
        }
    });

    // Filters are applied server-side, so an empty list under a filter means
    // "no matches", not "never ran": keep the list and its filter on screen.
    let panes = $derived(
        rail.panes(
            !!selection.selectedRun,
            !live.loading && live.source.items.length === 0 && activeFilterCount(filters) === 0,
        ),
    );

    const envEntries = $derived(
        task.env ? Object.entries(task.env).sort(([a], [b]) => a.localeCompare(b)) : [],
    );
    const showEnvPanel = $derived(envEntries.length > 0 || !!task.envFile || !!task.secretsFile);
</script>

<!-- Card-less, full-bleed: the rail and detail panel fill the content area
     edge-to-edge (cancelling AppLayout's p-6), divided only by the rail's
     right border. The topbar/sidebar are the outer frame; no nested card. -->
<div class="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
    {#if showEnvPanel}
        <section
            aria-label="Task environment"
            class="shrink-0 border-b border-outline bg-surface-raised px-6 py-3"
        >
            <h2 class="font-mono text-2xs font-medium tracking-[0.16em] text-info uppercase">
                Environment
            </h2>
            {#if envEntries.length > 0}
                <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 font-mono text-xs">
                    {#each envEntries as [key, value] (key)}
                        <dt class="text-on-surface">{key}</dt>
                        <dd class="break-all text-on-surface-muted">{value}</dd>
                    {/each}
                </dl>
            {/if}
            {#if task.envFile}
                <p class="mt-2 font-mono text-xs text-on-surface-faint">
                    Includes values from {task.envFile}
                </p>
            {/if}
            {#if task.secretsFile}
                <p class="mt-2 font-mono text-xs text-on-surface-faint">
                    Secrets from {task.secretsFile} (values not exposed)
                </p>
            {/if}
        </section>
    {/if}

    <div class="flex min-h-0 flex-1 flex-col md:flex-row">
        {#if panes.list}
            <RunsList
                items={live.source.items}
                total={live.source.total}
                loading={live.loading}
                bind:filters
                onLoadMore={() => live.source.loadMore()}
                selectedRunId={selection.selectedRunId}
                onselect={(id) => {
                    selection.userSelectedRunId = id;
                    rail.picked();
                }}
                showFilters
                bulkActions
                taskNameFilter={task.name}
                onBulkCancel={selection.handleBulkCancel}
                onBulkDelete={selection.handleBulkDelete}
                onBulkRerun={selection.handleBulkRerun}
                getInstanceCount={() => instanceCount}
                motion={live.source.motion}
                outputSearch
                {outputQuery}
                {outputMatches}
                {outputSearchPending}
            />
        {/if}

        {#if panes.detail}
            <RunDetailPanel
                run={selection.selectedRun}
                {...live.logSession}
                onDelete={selection.deleteSingle}
                onRun={runTriggerable ? openRun : undefined}
                onRunAgain={runTriggerable && hasParams ? openRunAgain : undefined}
                onRunTask={runTriggerable ? openRun : undefined}
                onStop={!taskIsService ? () => (stopConfirmOpen = true) : undefined}
                onBack={rail.phone ? rail.back : undefined}
                onToggleList={rail.collapsible ? rail.toggleList : undefined}
                listVisible={panes.list}
                {highlightLine}
                getInstanceCount={() => instanceCount}
                getLiveUsage={(id) => systemStore.runUsage(id)}
                motion={live.source.motion}
                notFound={selection.deepLinkMissing}
                loading={(live.loading && live.source.items.length === 0) ||
                    selection.deepLinkPending}
            />
        {/if}
    </div>
</div>

<Modal
    bind:open={confirmOpen}
    title={runModalTitle}
    description={runModalDescription}
    size={hasParams ? "md" : "sm"}
    children={showRunBody ? runModalBody : undefined}
>
    {#snippet footer()}
        <div class="flex justify-end gap-2">
            <Button variant="secondary" size="sm" onclick={() => (confirmOpen = false)}>
                Cancel
            </Button>
            <Button
                variant="primary"
                size="sm"
                disabled={hasParams && !runParamsValid}
                onclick={() => {
                    confirmOpen = false;
                    onRun(hasParams ? runParamValues : undefined);
                }}
            >
                {#snippet icon()}<Play size={16} />{/snippet}
                {runConfirmLabel}
            </Button>
        </div>
    {/snippet}
</Modal>

<AlertDialog
    bind:open={stopConfirmOpen}
    title="Stop Run"
    description="Stop the current run of {task.name}?"
    confirmLabel="Stop Now"
    confirmVariant="danger"
    confirmIcon={Square}
    onConfirm={() => {
        if (selection.selectedRun) onStop(selection.selectedRun.id);
    }}
/>

{#snippet runModalBody()}
    {#if concurrencyReached}
        <Alert variant="warning">
            This task is already running at its maximum concurrency. Your run will be <strong
                >queued</strong
            > and will start automatically once a slot becomes available.
        </Alert>
    {/if}
    {#if hasParams && confirmOpen}
        {#if runSeed}
            <div class="mb-3 flex items-center justify-between gap-2">
                <span class="text-xs text-on-surface-muted">Pre-filled from the selected run.</span>
                <button
                    type="button"
                    class="text-xs font-medium text-primary hover:underline"
                    onclick={resetRunToDefaults}
                >
                    Reset to defaults
                </button>
            </div>
        {/if}
        {#key runFormSeq}
            <ParamForm
                params={taskParams}
                initial={runSeed}
                bind:value={runParamValues}
                bind:valid={runParamsValid}
            />
        {/key}
    {/if}
{/snippet}
